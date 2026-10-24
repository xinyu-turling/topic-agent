package main

// 第 1 章 demo：Agent = 模型 + 工具 + 循环。
//
// 同一个问题，跑四个版本，看少一个零件会怎样：
//   ① full      模型 + 工具 + 循环   → 四步查完，自己收敛
//   ② no-loop   模型 + 工具          → 查到数据，但没人把它递回去
//   ③ no-tool   模型 + 循环          → 只能说“我没有查询能力”
//   ④ no-model  只有规则             → 词表没覆盖的说法直接兜底
//
// 运行：
//   go run ./01                              # 全部四个版本，内置 Mock，无需 api key
//   go run ./01 -mode full                   # 只跑其中一个
//   go run ./01 -real -api-key $KEY -model deepseek-chat

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
)

var modelName string

const question = "帮我看看主题 1001 大家在聊什么，给个速览。"

func main() {
	real := flag.Bool("real", false, "使用真实模型；不加则用内置 Mock 剧本")
	baseURL := flag.String("base-url", "https://api.deepseek.com/v1", "OpenAI 兼容接口地址（不带 /chat/completions）")
	apiKey := flag.String("api-key", os.Getenv("LLM_API_KEY"), "接口 api key，也可用环境变量 LLM_API_KEY")
	model := flag.String("model", "deepseek-chat", "模型名")
	mode := flag.String("mode", "all", "all | full | no-loop | no-tool | no-model")
	flag.Parse()

	modelName = *model

	// 每个版本都拿一个新的 llm 实例：Mock 的剧本是带步数的，不能共用。
	newLLM := func() llm { return &mockLLM{script: mockFullScript} }
	if *real {
		if *apiKey == "" {
			fmt.Println("✗ 使用 -real 时必须提供 -api-key 或设置 LLM_API_KEY")
			os.Exit(1)
		}
		newLLM = func() llm { return newOpenAIClient(*baseURL, *apiKey) }
		fmt.Printf(">>> 真实模型：%s @ %s\n", *model, *baseURL)
	} else {
		fmt.Println(">>> 未指定 -real，使用内置 Mock 模型（无需 api key）")
	}

	reg := newRegistry(topicDetailTool{}, topicCommentsTool{}, userProfileTool{})
	ctx := context.Background()

	modes := []string{"full", "no-loop", "no-tool", "no-model"}
	if *mode != "all" {
		valid := false
		for _, m := range modes {
			if m == *mode {
				valid = true
			}
		}
		if !valid {
			fmt.Printf("✗ 未知模式 %q，可选：all, full, no-loop, no-tool, no-model\n", *mode)
			os.Exit(1)
		}
		modes = []string{*mode}
	}

	for _, m := range modes {
		switch m {
		case "full":
			runFull(ctx, newLLM(), reg)
		case "no-loop":
			runNoLoop(ctx, newLLM(), reg)
		case "no-tool":
			runNoTool(ctx, newLLM())
		case "no-model":
			runNoModel()
		}
	}
}

func section(title, subtitle string) {
	fmt.Printf("\n\n========== %s ==========\n%s\n\n", title, subtitle)
}

// ---------- ① 完整版 ----------

func runFull(ctx context.Context, client llm, reg *registry) {
	section("① 模型 + 工具 + 循环", "三样都在：模型决定查什么，工具真的去查，循环把结果递回去。")

	fmt.Println("模型这一轮看到的工具清单（它只能读到这三段文字）：")
	for _, s := range reg.schemas() {
		fmt.Printf("  - %s：%s\n", s.Function.Name, s.Function.Description)
	}
	fmt.Printf("\n对比一下写坏的样子：%q —— 模型不知道“主题”是什么，也不知道什么时候该用。\n\n", badDescription)

	a := &agent{llm: client, reg: reg, maxSteps: 6, loop: true, verbose: true}
	answer := a.Run(ctx, question)

	fmt.Println("\n最终答案：")
	fmt.Println(answer)
}

// ---------- ② 去掉循环 ----------

func runNoLoop(ctx context.Context, client llm, reg *registry) {
	section("② 去掉循环：模型 + 工具", "工具照样执行了，但结果只是被 append 进 messages，没有再发一次请求。")

	a := &agent{llm: client, reg: reg, maxSteps: 1, loop: false, verbose: true}
	a.Run(ctx, question)

	fmt.Println("\n✗ 到此为止。")
	fmt.Println("  数据已经查到，模型也确实申请了，但没人把结果递回去——模型永远看不到它。")
	fmt.Println("  这就是“一次性脚本”：信息不够的时候，它只能编。")
	fmt.Println("  循环的作用不是重复调用，是把上一次的结果变成下一次的输入。")
}

// ---------- ③ 去掉工具 ----------

func runNoTool(ctx context.Context, client llm) {
	section("③ 去掉工具：模型 + 循环", "请求里 tools 为空，模型没有可以申请的东西——循环一次都不会转。")

	resp, err := client.Chat(ctx, &chatRequest{
		Model:    modelName,
		Messages: []chatMessage{{Role: "user", Content: question}},
	})
	if err != nil {
		fmt.Printf("✗ 请求失败：%v\n", err)
		return
	}
	fmt.Println("模型回复：")
	fmt.Println(resp.Choices[0].Message.Content)
}

// ---------- ④ 去掉模型 ----------

func runNoModel() {
	section("④ 去掉模型：只有规则", "关键词命中就返回固定话术，稳定、便宜、可解释——但词表永远不够全。")

	rules := []struct {
		name     string
		keywords []string
	}{
		{"主题详情", []string{"主题", "1001"}},
		{"评论列表", []string{"评论"}},
	}

	for _, q := range []string{"主题 1001 说了什么？", "我那个帖子咋没人回呢？"} {
		matched := ""
		for _, r := range rules {
			for _, kw := range r.keywords {
				if strings.Contains(q, kw) {
					matched = r.name
					break
				}
			}
			if matched != "" {
				break
			}
		}

		fmt.Printf("问：%s\n", q)
		if matched == "" {
			fmt.Println("→ 没命中任何关键词，落到兜底话术：「抱歉，这个问题我还不会回答」")
		} else {
			fmt.Printf("→ 命中规则「%s」，直接调用对应函数，跳过一切理解\n", matched)
		}
		fmt.Println()
	}

	fmt.Println("第二种说法人是能听懂的：他在问自己的帖子为什么没人回复。规则听不懂，模型能。")
}
