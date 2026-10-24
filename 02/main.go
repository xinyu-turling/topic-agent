package main

// 第 2 章 demo：Function Calling 的一次完整往返。
//
// 这一章只讲一件事：模型返回的 tool_calls 是“申请书”，不是调用。
// 真正执行的是本文件里的 registry.run —— 参数校验、查数据、把结果回填，全在你的代码里。
//
// 运行：
//   go run ./02                        # 内置 Mock 模型，无需 api key
//   go run ./02 -real -api-key $KEY    # 换成真实模型（任何 OpenAI 兼容接口）

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

var modelName string

func main() {
	real := flag.Bool("real", false, "使用真实模型；不加则用内置 Mock 剧本")
	baseURL := flag.String("base-url", "https://api.deepseek.com/v1", "OpenAI 兼容接口地址（不带 /chat/completions）")
	apiKey := flag.String("api-key", os.Getenv("LLM_API_KEY"), "接口 api key，也可用环境变量 LLM_API_KEY")
	model := flag.String("model", "deepseek-chat", "模型名")
	flag.Parse()

	modelName = *model

	var client llm = &mockLLM{}
	if *real {
		if *apiKey == "" {
			fmt.Println("✗ 使用 -real 时必须提供 -api-key 或设置 LLM_API_KEY")
			os.Exit(1)
		}
		client = newOpenAIClient(*baseURL, *apiKey)
		modelName = *model
		fmt.Printf(">>> 真实模型：%s @ %s\n", *model, *baseURL)
	} else {
		fmt.Println(">>> 未指定 -real，使用内置 Mock 模型（无需 api key）")
	}

	reg := newRegistry(newTopicDetailTool(), newTopicCommentsTool(), newUserProfileTool())
	ctx := context.Background()

	runRoundTrip(ctx, client, reg)
	runParsingChecks(reg)
}

// runRoundTrip 是最小闭环：问模型 → 模型申请工具 → 本地执行 → 回填 → 再问。
// 完整的 ReAct 循环（步数上限、并发、超时）在第 3 章，这里先只跑通一条链路。
func runRoundTrip(ctx context.Context, client llm, reg *registry) {
	messages := []chatMessage{
		{Role: "system", Content: "你是圈子里的助手。查数据必须申请调用工具，不要凭空编造。"},
		{Role: "user", Content: "帮我看看主题 1001 大家在聊什么，给个速览。"},
	}

	var total chatUsage
	var warnedEmptyContent bool
	const maxSteps = 6

	for step := 1; step <= maxSteps; step++ {
		fmt.Printf("\n========== 第 %d 轮 ==========\n", step)

		resp, err := client.Chat(ctx, &chatRequest{
			Model:    modelName,
			Messages: messages,
			Tools:    reg.definitions(),
		})
		if err != nil {
			fmt.Printf("✗ 请求模型失败：%v\n", err)
			return
		}
		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens

		choice, err := resp.firstChoice()
		if err != nil {
			fmt.Printf("✗ %v\n", err)
			return
		}

		msg := choice.Message
		fmt.Printf("[模型返回] finish_reason=%s content=%q tool_calls=%d\n",
			choice.FinishReason, msg.Content, len(msg.ToolCalls))

		if msg.Content == "" && len(msg.ToolCalls) > 0 && !warnedEmptyContent {
			warnedEmptyContent = true
			fmt.Println("  ⚠ 此刻 content 是空串。如果用 content == \"\" 判断收敛，这里就会误判成“模型没回答”。")
		}

		if choice.FinishReason == finishLength {
			fmt.Println("  ⚠ finish_reason=length：输出被 max_tokens 截断了，内容不完整")
		}

		// 唯一的收敛判据：没有工具申请了。
		// 不要用 msg.Content == "" 判断 —— 模型申请工具时 content 就是空的。
		if len(msg.ToolCalls) == 0 {
			fmt.Println("\n========== 收敛：模型不再申请工具 ==========")
			fmt.Println(msg.Content)
			fmt.Printf("\n[usage] prompt=%d completion=%d total=%d\n",
				total.PromptTokens, total.CompletionTokens, total.TotalTokens)
			return
		}

		// 带 tool_calls 的 assistant 消息必须原样回传，否则下一轮模型不知道自己申请过什么。
		messages = append(messages, msg)

		for i, tc := range msg.ToolCalls {
			fmt.Printf("[申请书 %d/%d] id=%s 工具=%s 参数=%s\n",
				i+1, len(msg.ToolCalls), tc.ID, tc.Function.Name, tc.Function.Arguments)
			fmt.Println("            ↳ 这只是一段 JSON。到此为止，没有任何代码被执行。")

			obs, runErr := reg.run(ctx, tc.Function.Name, tc.Function.Arguments.String())
			if runErr != nil {
				// 失败也是观察结果：回灌给模型，让它自己改。
				obs = errorJSON(runErr)
				fmt.Printf("[执行]      失败：%v\n", runErr)
			} else {
				fmt.Printf("[执行]      registry.run(...) → %s\n", obs)
			}

			fmt.Printf("[回填]      role=tool tool_call_id=%s content=%s\n", tc.ID, obs)
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    obs,
			})
		}
	}

	fmt.Println("\n✗ 达到步数上限，停止")
}

// runParsingChecks 不经过模型，直接把几种边界报文喂给解析器。
// 这两类坑在 mock 剧本里未必每次都出现，单独验一遍更踏实。
func runParsingChecks(reg *registry) {
	fmt.Println("\n\n========== 解析自检：null 与字符串 ==========")

	samples := []struct {
		title string
		raw   string
	}{
		{
			"content 是 null，arguments 是字符串（标准形态）",
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_topic_detail","arguments":"{\"topic_id\":1001}"}}]},"finish_reason":"tool_calls"}]}`,
		},
		{
			"arguments 是对象（部分网关 / 本地模型）",
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c2","type":"function","function":{"name":"get_topic_detail","arguments":{"topic_id":1001}}}]},"finish_reason":"tool_calls"}]}`,
		},
		{
			"arguments 是空串",
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c3","type":"function","function":{"name":"get_topic_comments","arguments":""}}]},"finish_reason":"tool_calls"}]}`,
		},
		{
			"arguments 是坏 JSON（少右括号）",
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c4","type":"function","function":{"name":"get_topic_detail","arguments":"{\"topic_id\":1001"}}]},"finish_reason":"tool_calls"}]}`,
		},
	}

	for i, s := range samples {
		fmt.Printf("\n--- 样例 %d：%s ---\n", i+1, s.title)

		var resp chatResponse
		if err := json.Unmarshal([]byte(s.raw), &resp); err != nil {
			fmt.Printf("  解析失败：%v\n", err)
			continue
		}
		choice, err := resp.firstChoice()
		if err != nil {
			fmt.Printf("  %v\n", err)
			continue
		}
		tc := choice.Message.ToolCalls[0]
		fmt.Printf("  content=%q（null 解成空串，不报错）\n", choice.Message.Content)
		fmt.Printf("  arguments（原始文本）=%s\n", tc.Function.Arguments)

		obs, runErr := reg.run(context.Background(), tc.Function.Name, tc.Function.Arguments.String())
		if runErr != nil {
			fmt.Printf("  registry 兜底：%v\n", runErr)
			fmt.Printf("  → 这段错误会作为观察结果回灌，模型据此改正\n")
			continue
		}
		fmt.Printf("  registry 执行成功：%s\n", truncate(obs, 120))
	}
}

func errorJSON(err error) string {
	b, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return `{"error":"未知错误"}`
	}
	return string(b)
}
