# 02 · Function Calling：模型返回的是申请书

对应文章：

- 2.1 模型返回的不是调用，是申请书 —— `02-01-模型返回的不是调用是申请书.md`
- 2.2 两个必踩的坑：null 与字符串 —— `02-02-两个必踩的坑-null与字符串.md`

演示的是**一次完整往返**，不是 ReAct 循环：

```
你  → 模型：这是对话历史，这是你能用的工具
模型 → 你  ：我要调 get_topic_detail，参数 {"topic_id":1001}，编号 call_001
你  → 代码：registry.run(...) 真正查数据
你  → 模型：编号 call_001 的结果是 {...}
模型 → 你  ：基于这个结果，我的速览是……
```

## 运行

```bash
go run ./02

# 换成真实模型
go run ./02 -real -base-url https://api.deepseek.com/v1 -api-key $LLM_API_KEY -model deepseek-chat
```

不需要 api key。默认走内置 Mock 剧本，Mock 返回的是**原始报文**（不是构造好的结构体），所以解析路径和真实调用完全一样。

## 文件导读

| 文件 | 作用 |
|---|---|
| `protocol.go` | OpenAI 线格式结构体；`content` 用 `string`，`arguments` 用自定义 `rawArguments` 兼容字符串/对象两种形态 |
| `tools.go` | `registry` + 三个只读工具；参数校验、空串/坏 JSON/类型不符的兜底 |
| `client.go` | `llm` 接口、真实 HTTP 客户端、Mock 剧本 |
| `main.go` | 四轮往返 + 解析自检 |

## 预期输出（节选）

```
========== 第 1 轮 ==========
[模型返回] finish_reason=tool_calls content="" tool_calls=1
  ⚠ 此刻 content 是空串。如果用 content == "" 判断收敛，这里就会误判成“模型没回答”。
[申请书 1/1] id=call_001 工具=get_topic_detail 参数={"topic_id":1001}
            ↳ 这只是一段 JSON。到此为止，没有任何代码被执行。
[执行]      registry.run(...) → {"author":"楼主·小林",...}
[回填]      role=tool tool_call_id=call_001 content={"author":"楼主·小林",...}

========== 第 2 轮 ==========
[申请书 1/1] id=call_002 工具=get_topic_comments 参数={"topic_id":1001,"limit":
[执行]      失败：参数不是合法 JSON：{"topic_id":1001,"limit":
[回填]      role=tool tool_call_id=call_002 content={"error":"参数不是合法 JSON：..."}

========== 第 4 轮 ==========
[模型返回] finish_reason=stop content="【AI 速览】..." tool_calls=0

========== 收敛：模型不再申请工具 ==========
```

## 四个观察点

### 1. 申请书不是调用

`[申请书]` 那行只是一段 JSON。`registry.run` 之前，数据库连接没被打开、SQL 没被发送。
真正执行的是 `main.go` 里这几行，模型碰不到：

```go
obs, runErr := reg.run(ctx, tc.Function.Name, tc.Function.Arguments.String())
```

### 2. `content: null` 不会解析失败

Go 把 `null` 解到 `string` 是“忽略”，得到空串且不报错。所以 `protocol.go` 里没有一行是在“防 null”。

真正的坑是**判断方式**：用 `Content == ""` 判断收敛，第一轮就会误判成“模型没回答”。
唯一正确的判据是 `len(msg.ToolCalls) == 0`。

### 3. `arguments` 是字符串，而且不可信

三层处理，分别对应三种翻车方式：

| 层 | 代码 | 挡的是 |
|---|---|---|
| 反序列化 | `rawArguments.UnmarshalJSON` | 标准形态是字符串，个别网关给对象 |
| 执行前 | `registry.run` 的空串兜底 + `json.Valid` | 空串、坏 JSON |
| 参数解码 | `int64Arg` | 把 `1001` 写成 `"1001"` |

失败一律返回 error，由调用方转成观察结果回灌——模型看到“参数不合法”，下一轮会自己改（第 2 轮的坏 JSON 就是这么被纠正的）。

### 4. 回填必须带 `tool_call_id`

```go
messages = append(messages, chatMessage{
    Role:       "tool",
    ToolCallID: tc.ID,   // 漏了不会报错，但模型会答非所问
    Content:    obs,
})
```

同理，带 `tool_calls` 的 assistant 消息要**原样回传**，不要为了省 token 压成纯文本。

## 与真实项目的差异

| Demo | 生产 |
|---|---|
| 内存里的假数据 | `group_agg.GroupQueryRepoImpl` / `CommentQuerySvc` |
| 固定 Mock 剧本 | 真实模型；同一份 prompt 每次结果可能不同 |
| 单轮无步数、无并发 | 第 3 章的 ReAct 循环 + 并发 + 超时 |
| 直接打印观察结果 | 观察结果落 `agent_message`，可回放 |
| `fmt.Errorf` | `biz/err_code` 统一错误码 |

## 自测清单

- [ ] 判断收敛用的是 `len(ToolCalls) == 0`，不是 `Content == ""`
- [ ] `arguments` 当字符串收，执行前再解析
- [ ] 空串、坏 JSON、类型不符三种情况都有兜底
- [ ] 错误也回填成观察结果，而不是直接 throw
- [ ] 回填带上 `tool_call_id`
