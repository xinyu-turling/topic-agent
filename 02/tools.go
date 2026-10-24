package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// tool 是模型能“申请”调用的东西。
// 注意：run 只会被本地代码调用，模型碰不到它，它最多能让模型看见 description 和参数 schema。
type tool struct {
	name        string
	description string
	parameters  map[string]any
	run         func(ctx context.Context, args json.RawMessage) (string, error)
}

type registry struct {
	order []string
	tools map[string]*tool
}

func newRegistry(tools ...*tool) *registry {
	r := &registry{tools: make(map[string]*tool, len(tools))}
	for _, t := range tools {
		r.tools[t.name] = t
		r.order = append(r.order, t.name)
	}
	return r
}

// definitions 是发给模型的那份“工具清单”。
func (r *registry) definitions() []chatTool {
	defs := make([]chatTool, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		var def chatTool
		def.Type = "function"
		def.Function.Name = t.name
		def.Function.Description = t.description
		def.Function.Parameters = t.parameters
		defs = append(defs, def)
	}
	return defs
}

// run 执行模型申请的这一次调用。
//
// 模型给的参数不可信：可能是空串、可能是坏 JSON、也可能把数字写成字符串。
// 这里全部兜住，出错就返回 error —— 调用方会把 error 转成观察结果回灌给模型，
// 模型看到“你给的参数不合法”，才有机会自己改。
func (r *registry) run(ctx context.Context, name, rawArgs string) (string, error) {
	t, ok := r.tools[strings.TrimSpace(name)]
	if !ok {
		return "", fmt.Errorf("未知工具 %q，可用工具：%s", name, strings.Join(r.order, ", "))
	}

	rawArgs = strings.TrimSpace(rawArgs)
	if rawArgs == "" {
		rawArgs = "{}" // 空参数当成没有参数
	}
	if !json.Valid([]byte(rawArgs)) {
		return "", fmt.Errorf("参数不是合法 JSON：%s", rawArgs)
	}
	return t.run(ctx, json.RawMessage(rawArgs))
}

// int64Arg 同时接受 1001 和 "1001"。
// 模型“自由发挥”时把数字写成字符串非常常见，直接声明成 int64 会整条调用失败。
type int64Arg int64

func (n *int64Arg) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("需要整数，收到 %s", string(data))
	}
	*n = int64Arg(v)
	return nil
}

type topic struct {
	ID       int64
	Title    string
	Author   string
	Content  string
	Comments []string
}

var fakeTopics = map[int64]*topic{
	1001: {
		ID:      1001,
		Title:   "最近想转 Go，求劝退",
		Author:  "楼主·小林",
		Content: "写了三年 Java，最近在做一个高并发的网关，感觉 Go 的模型更顺手，但团队里没人写过。要不要转，还是继续深耕 Java？",
		Comments: []string{
			"阿杰：先把手写 HTTP 服务练熟，再谈转不转。",
			"Mia：语言不是瓶颈，工程习惯才是。",
			"老陈：并发模型差异比语法差异大得多。",
		},
	},
	1002: {
		ID:      1002,
		Title:   "圈子该不该开放匿名发帖",
		Author:  "楼主·阿真",
		Content: "匿名能提高表达欲，但也会带来垃圾内容。有没有中间方案？",
		Comments: []string{
			"kee：先做匿名+可追溯，出问题能查。",
		},
	},
}

var fakeUsers = map[int64]struct{ Name, Intro string }{
	999: {Name: "楼主·小林", Intro: "三年 Java，正在纠结 Go"},
}

const (
	topicIDParam = "topic_id"
)

func newTopicDetailTool() *tool {
	return &tool{
		name:        "get_topic_detail",
		description: "按主题 ID 读取主题的标题、作者与正文。用户提到某条主题、需要了解其内容时调用。",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				topicIDParam: map[string]any{
					"type":        "integer",
					"description": "主题 ID，19 位以内的整数",
				},
			},
			"required": []string{topicIDParam},
		},
		run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				TopicID int64Arg `json:"topic_id"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败：%w", err)
			}
			if p.TopicID == 0 {
				return "", fmt.Errorf("缺少参数 topic_id")
			}
			t, ok := fakeTopics[int64(p.TopicID)]
			if !ok {
				return "", fmt.Errorf("主题 %d 不存在", int64(p.TopicID))
			}
			return toJSON(map[string]any{
				"topic_id": t.ID,
				"title":    t.Title,
				"author":   t.Author,
				"content":  t.Content,
			}), nil
		},
	}
}

func newTopicCommentsTool() *tool {
	return &tool{
		name:        "get_topic_comments",
		description: "读取某条主题下的评论，用于了解大家的观点分布。limit 为返回条数，默认 3，最大 20。",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				topicIDParam: map[string]any{"type": "integer", "description": "主题 ID"},
				"limit":      map[string]any{"type": "integer", "description": "返回条数，默认 3，最大 20"},
			},
			"required": []string{topicIDParam},
		},
		run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				TopicID int64Arg `json:"topic_id"`
				Limit   int64Arg `json:"limit"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败：%w", err)
			}
			if p.TopicID == 0 {
				return "", fmt.Errorf("缺少参数 topic_id")
			}
			t, ok := fakeTopics[int64(p.TopicID)]
			if !ok {
				return "", fmt.Errorf("主题 %d 不存在", int64(p.TopicID))
			}
			limit := int(p.Limit)
			if limit <= 0 {
				limit = 3
			}
			if limit > 20 {
				limit = 20
			}
			comments := t.Comments
			if limit < len(comments) {
				comments = comments[:limit]
			}
			return toJSON(map[string]any{
				"topic_id": t.ID,
				"total":    len(t.Comments),
				"comments": comments,
			}), nil
		},
	}
}

func newUserProfileTool() *tool {
	return &tool{
		name:        "get_user_profile",
		description: "按用户 ID 读取用户昵称与简介。只有拿到用户 ID 时才调用。",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"uid": map[string]any{"type": "integer", "description": "用户 ID"},
			},
			"required": []string{"uid"},
		},
		run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				UID int64Arg `json:"uid"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("参数解析失败：%w", err)
			}
			if p.UID == 0 {
				return "", fmt.Errorf("缺少参数 uid")
			}
			u, ok := fakeUsers[int64(p.UID)]
			if !ok {
				return "", fmt.Errorf("用户 %d 不存在", int64(p.UID))
			}
			return toJSON(map[string]any{"uid": int64(p.UID), "name": u.Name, "intro": u.Intro}), nil
		},
	}
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"结果序列化失败"}`
	}
	return string(b)
}
