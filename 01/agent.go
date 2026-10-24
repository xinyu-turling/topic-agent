package main

import (
	"context"
	"fmt"
)

// agent 就是文章里那个 for 循环，加上“要不要循环”的开关，用来做对照实验。
type agent struct {
	llm      llm
	reg      *registry
	maxSteps int
	loop     bool // false 模拟“去掉循环”：只跑一轮
	verbose  bool
}

func (a *agent) Run(ctx context.Context, question string) string {
	messages := []chatMessage{
		{Role: "system", Content: "你是圈子里的助手。需要查数据时必须申请调用工具，不要凭空编造。"},
		{Role: "user", Content: question},
	}

	for step := 1; step <= a.maxSteps; step++ {
		resp, err := a.llm.Chat(ctx, &chatRequest{
			Model:    modelName,
			Messages: messages,
			Tools:    a.reg.schemas(),
		})
		if err != nil {
			return fmt.Sprintf("（请求失败：%v）", err)
		}
		msg := resp.Choices[0].Message

		// 退出条件：模型不再申请工具。
		if len(msg.ToolCalls) == 0 {
			if a.verbose {
				fmt.Printf("[第 %d 步] 模型不再申请工具，输出答案\n", step)
			}
			return msg.Content
		}

		if a.verbose {
			for _, tc := range msg.ToolCalls {
				fmt.Printf("[第 %d 步] 模型申请调用：%s(%s)   # %s\n",
					step, tc.Function.Name, tc.Function.Arguments, tc.ID)
			}
		}

		// 带 tool_calls 的 assistant 消息要原样回传，模型才知道自己申请过什么。
		messages = append(messages, msg)

		for _, tc := range msg.ToolCalls {
			obs, runErr := a.reg.run(ctx, tc.Function.Name, tc.Function.Arguments)
			if runErr != nil {
				// 工具失败不中断：错误本身也是观察结果。
				obs = errorJSON(runErr)
				if a.verbose {
					fmt.Printf("         观察结果：%s   <-- 工具失败\n", obs)
				}
			} else if a.verbose {
				fmt.Printf("         观察结果：%s\n", truncate(obs, 90))
			}

			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    obs,
			})
		}

		if !a.loop {
			return "" // 由调用方打印说明
		}
	}

	return "（达到步数上限，强制停止）"
}

func errorJSON(err error) string {
	return fmt.Sprintf(`{"error":%q}`, err.Error())
}

// truncate 按字符截断，别把中文砍成半个字。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
