package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// llm 是这一章唯一需要的外部依赖：发一次请求，拿一次响应。
// 真正的“调工具”不在这里，也不在模型那边。
type llm interface {
	Chat(ctx context.Context, req *chatRequest) (*chatResponse, error)
}

type openaiClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func newOpenAIClient(baseURL, apiKey string) *openaiClient {
	return &openaiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *openaiClient) Chat(ctx context.Context, req *chatRequest) (*chatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败：%w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求失败：%w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("接口返回 %d：%s", resp.StatusCode, truncate(string(raw), 300))
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}
	return &out, nil
}

// mockLLM 返回的是**原始报文**，不是构造好的结构体。
// 这样 demo 走的是和真实调用完全一样的解析路径，null 与字符串这两个坑才看得见。
type mockLLM struct {
	step int
}

func (m *mockLLM) Chat(ctx context.Context, req *chatRequest) (*chatResponse, error) {
	m.step++
	if m.step > len(mockScript) {
		return nil, fmt.Errorf("mock 剧本只写了 %d 步，第 %d 步没有内容", len(mockScript), m.step)
	}
	return parseResponse(mockScript[m.step-1])
}

func parseResponse(raw string) (*chatResponse, error) {
	var out chatResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// 剧本：第 1 步查主题 → 第 2 步参数写坏（演示回灌）→ 第 3 步改正并查评论 → 第 4 步收敛。
// 注意第 3 步的 arguments 用了对象形态（非标准），同时 limit 是字符串 "2"。
var mockScript = []string{
	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": null,
	      "tool_calls": [{
	        "id": "call_001",
	        "type": "function",
	        "function": {"name": "get_topic_detail", "arguments": "{\"topic_id\":1001}"}
	      }]
	    },
	    "finish_reason": "tool_calls"
	  }],
	  "usage": {"prompt_tokens": 420, "completion_tokens": 31, "total_tokens": 451}
	}`,

	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": null,
	      "tool_calls": [{
	        "id": "call_002",
	        "type": "function",
	        "function": {"name": "get_topic_comments", "arguments": "{\"topic_id\":1001,\"limit\":"}
	      }]
	    },
	    "finish_reason": "tool_calls"
	  }],
	  "usage": {"prompt_tokens": 520, "completion_tokens": 28, "total_tokens": 548}
	}`,

	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": null,
	      "tool_calls": [{
	        "id": "call_003",
	        "type": "function",
	        "function": {"name": "get_topic_comments", "arguments": {"topic_id": 1001, "limit": "2"}}
	      }]
	    },
	    "finish_reason": "tool_calls"
	  }],
	  "usage": {"prompt_tokens": 610, "completion_tokens": 26, "total_tokens": 636}
	}`,

	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": "【AI 速览】楼主有三年 Java 背景，在做一个高并发网关时觉得 Go 更顺手，纠结要不要转。评论区三条意见比较一致：语言本身不是瓶颈，先把 HTTP 服务、并发模型这些基本功练熟，再决定要不要换栈。"
	    },
	    "finish_reason": "stop"
	  }],
	  "usage": {"prompt_tokens": 880, "completion_tokens": 96, "total_tokens": 976}
	}`,
}

// truncate 按字符截断，不能按字节——中文会被砍成半个字。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
