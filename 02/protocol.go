package main

import (
	"encoding/json"
	"fmt"
)

// 这一层只做一件事：把 OpenAI Chat Completions 的线格式映射成 Go 结构体。
// 报文长什么样，结构体就长什么样，不做多余的“防御性设计”。

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role string `json:"role"`
	// content 在模型申请调工具时是 null。
	// Go 的 encoding/json 把 null 解到 string 是“忽略”，得到空串且不报错，
	// 所以这里用 string 就够——不需要 *string，更不需要自定义类型。
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string       `json:"name"`
		Arguments rawArguments `json:"arguments"`
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
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// finish_reason 的三个常见取值。它决定这一轮结束后该干什么。
const (
	finishStop      = "stop"       // 说完了，content 是最终答案
	finishToolCalls = "tool_calls" // 在申请调工具
	finishLength    = "length"     // 被 max_tokens 截断，内容不完整
)

// rawArguments 保留 arguments 的原始文本。
//
// 标准形态是 JSON 字符串："{\"topic_id\":1001}"。
// 但部分兼容网关、本地部署的模型（vLLM / Ollama 某些版本）会直接给对象：{"topic_id":1001}。
// 声明成 map 会在标准形态报错，声明成 string 会在对象形态报错，所以两种都收下来，
// 统一留到执行前那次解析里处理。
type rawArguments string

func (a *rawArguments) UnmarshalJSON(data []byte) error {
	switch {
	case len(data) == 0, string(data) == "null":
		*a = ""
	case data[0] == '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*a = rawArguments(s)
	default:
		// 对象形态：原样保留
		*a = rawArguments(data)
	}
	return nil
}

func (a rawArguments) String() string { return string(a) }

// firstChoice 取第一个候选，顺便挡掉空响应。
func (r *chatResponse) firstChoice() (chatChoice, error) {
	if r == nil || len(r.Choices) == 0 {
		return chatChoice{}, fmt.Errorf("响应里没有 choices")
	}
	return r.Choices[0], nil
}
