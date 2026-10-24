# 01 · Agent = 模型 + 工具 + 循环

对应文章：

- 1.2 Agent = 模型 + 工具 + 循环 —— `01-02-Agent-模型加工具加循环.md`

这一章不碰协议细节，只讲三样东西怎么配合。所以 demo 的做法是**同一个问题跑四个版本**，少一个零件就少一种能力：

| 模式 | 有什么 | 表现 |
|---|---|---|
| `full` | 模型 + 工具 + 循环 | 四步查完，自己收敛 |
| `no-loop` | 模型 + 工具 | 数据查到了，但没人递给模型 |
| `no-tool` | 模型 + 循环 | 只能说“我没有查询能力” |
| `no-model` | 只有规则 | 词表没覆盖的说法直接兜底 |

## 运行

```bash
go run ./01                 # 四个版本一起跑
go run ./01 -mode no-loop   # 只跑一个：all | full | no-loop | no-tool | no-model

# 换成真实模型
go run ./01 -real -base-url https://api.deepseek.com/v1 -api-key $LLM_API_KEY -model deepseek-chat
```

默认走内置 Mock，不需要 api key。换成 `-real` 后，① 里的工具选择会由真模型决定，`description` 写得好不好的差别才真正显现。

## 文件导读

| 文件 | 作用 |
|---|---|
| `tools.go` | `Tool` 接口（Name / Description / Parameters / Run）、`registry`、三个只读工具、写好与写坏的描述对比 |
| `agent.go` | 那个 `for` 循环；`loop: false` 用来模拟“去掉循环” |
| `llm.go` | 最小版 OpenAI 协议结构体 + 真实客户端 + Mock 剧本 |
| `main.go` | 四个版本的对照实验 |

## 预期输出（节选）

```
========== ① 模型 + 工具 + 循环 ==========
模型这一轮看到的工具清单（它只能读到这三段文字）：
  - get_topic_detail：查询圈子主题的标题、正文纯文本、作者。需要了解主题说了什么时必须先调用它。

对比一下写坏的样子："查询主题详情" —— 模型不知道“主题”是什么，也不知道什么时候该用。

[第 1 步] 模型申请调用：get_topic_detail({"topic_id":1001})   # call_001
         观察结果：{"author":"楼主·小林","content":"写了三年 Java，…"}
[第 2 步] 模型申请调用：get_user_profile({"uid":999})   # call_002
         观察结果：{"error":"用户 999 不存在"}   <-- 工具失败
[第 3 步] 模型申请调用：get_topic_comments({"topic_id":1001,"limit":5})   # call_003
         观察结果：{"comments":["阿杰：先把手写 HTTP 服务练熟…"]}
[第 4 步] 模型不再申请工具，输出答案
```

## 四个观察点

### 1. 工具描述是写给模型看的

`Description` 里两句话各管一件事：

```go
"查询圈子主题的标题、正文纯文本、作者。" +      // 返回什么
"需要了解主题说了什么时必须先调用它。"          // 什么时候用
```

第二句比第一句重要。模型看不到你的代码，只能读到这一句——写得含糊，它要么不用，要么乱用。

### 2. 循环不是“重复调用”，是“把结果变成下一次输入”

看 `agent.go` 里这段：

```go
messages = append(messages, msg)                      // 带上模型的申请
for _, tc := range msg.ToolCalls {
    obs, _ := a.reg.run(...)                          // 真的执行
    messages = append(messages, chatMessage{
        Role: "tool", ToolCallID: tc.ID, Content: obs, // 把结果塞回对话
    })
}
```

`-mode no-loop` 就是把“再请求一次”这一步去掉。工具照样执行，数据照样查到，但模型永远看不到——它拿不到结果，也就接不上下一句。

### 3. 工具失败是观察结果，不是流程中断

第 2 步查一个不存在的用户，`registry.run` 返回 error，`agent.go` 把它转成 `{"error":...}` 回灌。模型看到这个结果，第 3 步换了个方向去查评论。

如果把 error 直接 `return`，整个对话就断在第 2 步了——那是脚本，不是 Agent。

### 4. 参数越窄越好

三个工具的参数只有 `topic_id` / `uid` / `limit`，没有一个参数能让模型决定“查什么表、怎么过滤”。模型能给的最大破坏是传一个不存在的 id。

## 与真实项目的差异

| Demo | 生产 |
|---|---|
| 内存假数据 | `group_agg.GroupQueryRepoImpl` / `CommentQuerySvc` |
| 固定 Mock 剧本 | 真实模型，同一 prompt 每次可能不同 |
| 单线程顺序执行 | 第 3 章：并发、单工具超时、整任务超时 |
| 打印到 stdout | 步骤与工具调用序列落日志，badcase 可回放 |
| 没有幂等与收口 | 第 6 章：只读工具 + 副作用收口 |
