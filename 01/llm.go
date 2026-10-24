package main

// 第 1 章只关心“协议长什么样、谁在做事”，所以这里的结构体是最小版本。
// content 为什么用 string、arguments 为什么是字符串，这些坑留到第 2 章专门讲。

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

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

type llm interface {
	Chat(ctx context.Context, req *chatRequest) (*chatResponse, error)
}

// ---------- 真实模型 ----------

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
		return nil, fmt.Errorf("接口返回 %d：%s", resp.StatusCode, raw)
	}

	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析响应失败：%w", err)
	}
	return &out, nil
}

// ---------- Mock 模型 ----------

// mockLLM 按剧本返回原始报文。
// 唯一"真实"的行为是：请求里没给工具清单时，它也确实不会申请调用工具。
type mockLLM struct {
	script []string
	step   int
}

func (m *mockLLM) Chat(ctx context.Context, req *chatRequest) (*chatResponse, error) {
	if len(req.Tools) == 0 {
		return parseResponse(mockNoTools)
	}
	m.step++
	if m.step > len(m.script) {
		return nil, fmt.Errorf("mock 剧本只有 %d 步，第 %d 步没有内容", len(m.script), m.step)
	}
	return parseResponse(m.script[m.step-1])
}

func parseResponse(raw string) (*chatResponse, error) {
	var out chatResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("响应里没有 choices")
	}
	return &out, nil
}

const mockNoTools = `{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "我没有查询能力，只能根据你告诉我的内容回答。如果你想知道主题 1001 说了什么，需要你自己打开主题页看一下。"
    },
    "finish_reason": "stop"
  }]
}`

// 完整模式（模型 + 工具 + 循环）的四步剧本：
// 查详情 → 查用户（失败）→ 查评论 → 收尾。
var mockFullScript = []string{
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
	  }]
	}`,
	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": null,
	      "tool_calls": [{
	        "id": "call_002",
	        "type": "function",
	        "function": {"name": "get_user_profile", "arguments": "{\"uid\":999}"}
	      }]
	    },
	    "finish_reason": "tool_calls"
	  }]
	}`,
	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": null,
	      "tool_calls": [{
	        "id": "call_003",
	        "type": "function",
	        "function": {"name": "get_topic_comments", "arguments": "{\"topic_id\":1001,\"limit\":5}"}
	      }]
	    },
	    "finish_reason": "tool_calls"
	  }]
	}`,
	`{
	  "choices": [{
	    "message": {
	      "role": "assistant",
	      "content": "【速览】楼主是三年 Java 背景，在写一个高并发网关时觉得 Go 更顺手，纠结要不要转栈。评论区的意见比较一致：语言本身不是瓶颈，先把 HTTP 服务、并发模型这些基本功练熟，再决定要不要换。"
	    },
	    "finish_reason": "stop"
	  }]
	}`,
}
