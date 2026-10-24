package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 工具就是一个普通函数，加一份给模型看的说明书。
// 前三个方法给模型看，Run 只给你自己跑——模型永远碰不到它。
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

type registry struct {
	tools []Tool
}

func newRegistry(tools ...Tool) *registry {
	return &registry{tools: tools}
}

// schemas 就是发给模型的那份清单。
func (r *registry) schemas() []chatTool {
	out := make([]chatTool, 0, len(r.tools))
	for _, t := range r.tools {
		var s chatTool
		s.Type = "function"
		s.Function.Name = t.Name()
		s.Function.Description = t.Description()
		s.Function.Parameters = t.Parameters()
		out = append(out, s)
	}
	return out
}

func (r *registry) names() string {
	names := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		names = append(names, t.Name())
	}
	return strings.Join(names, ", ")
}

// run 执行模型申请的这一次调用。找不到工具就把可用清单告诉模型。
func (r *registry) run(ctx context.Context, name, rawArgs string) (string, error) {
	for _, t := range r.tools {
		if t.Name() != name {
			continue
		}
		if strings.TrimSpace(rawArgs) == "" {
			rawArgs = "{}"
		}
		return t.Run(ctx, json.RawMessage(rawArgs))
	}
	return "", fmt.Errorf("未知工具 %q，可用工具：%s", name, r.names())
}

// ---------- 假数据 ----------

type topic struct {
	ID       int64
	Title    string
	Author   string
	Content  string
	Comments []string
}

var topics = map[int64]*topic{
	1001: {
		ID:      1001,
		Title:   "最近想转 Go，求劝退",
		Author:  "楼主·小林",
		Content: "写了三年 Java，最近在做一个高并发网关，觉得 Go 的模型更顺手，但团队里没人写过。要不要转？",
		Comments: []string{
			"阿杰：先把手写 HTTP 服务练熟，再谈转不转。",
			"Mia：语言不是瓶颈，工程习惯才是。",
			"老陈：并发模型差异比语法差异大得多。",
		},
	},
}

// 故意不含 999：Mock 剧本第 2 步会拿 999 去查，用来演示“工具失败不中断流程”。
var users = map[int64]struct{ Name, Intro string }{
	10086: {Name: "阿杰", Intro: "后端工程师，写了六年 Go"},
}

// ---------- 三个工具 ----------

type topicDetailTool struct{}

// Description 是给模型看的，不是给同事看的。
// 第一句说清返回什么，第二句说清什么时候用——第二句更重要。
func (topicDetailTool) Name() string { return "get_topic_detail" }

func (topicDetailTool) Description() string {
	return "查询圈子主题的标题、正文纯文本、作者。" +
		"需要了解主题说了什么时必须先调用它。"
}

func (topicDetailTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topic_id": map[string]any{"type": "integer", "description": "主题 ID"},
		},
		"required": []string{"topic_id"},
	}
}

func (topicDetailTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		TopicID int64 `json:"topic_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败：%w", err)
	}
	t, ok := topics[p.TopicID]
	if !ok {
		return "", fmt.Errorf("主题 %d 不存在", p.TopicID)
	}
	return toJSON(map[string]any{
		"topic_id": t.ID,
		"title":    t.Title,
		"author":   t.Author,
		"content":  t.Content,
	}), nil
}

type topicCommentsTool struct{}

func (topicCommentsTool) Name() string { return "get_topic_comments" }

func (topicCommentsTool) Description() string {
	return "查询主题下的评论，返回评论正文列表。" +
		"需要了解大家的观点或讨论走向时调用。"
}

func (topicCommentsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"topic_id": map[string]any{"type": "integer", "description": "主题 ID"},
			"limit":    map[string]any{"type": "integer", "description": "返回条数，默认 3"},
		},
		"required": []string{"topic_id"},
	}
}

func (topicCommentsTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		TopicID int64 `json:"topic_id"`
		Limit   int64 `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败：%w", err)
	}
	t, ok := topics[p.TopicID]
	if !ok {
		return "", fmt.Errorf("主题 %d 不存在", p.TopicID)
	}
	limit := int(p.Limit)
	if limit <= 0 {
		limit = 3
	}
	comments := t.Comments
	if limit < len(comments) {
		comments = comments[:limit]
	}
	return toJSON(map[string]any{"topic_id": t.ID, "comments": comments}), nil
}

type userProfileTool struct{}

func (userProfileTool) Name() string { return "get_user_profile" }

func (userProfileTool) Description() string {
	return "按用户 ID 查询昵称与个人简介。" +
		"只有已经拿到用户 ID 时才调用；主题作者只给了昵称时不要调用它。"
}

func (userProfileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uid": map[string]any{"type": "integer", "description": "用户 ID"},
		},
		"required": []string{"uid"},
	}
}

func (userProfileTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		UID int64 `json:"uid"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败：%w", err)
	}
	u, ok := users[p.UID]
	if !ok {
		return "", fmt.Errorf("用户 %d 不存在", p.UID)
	}
	return toJSON(map[string]any{"uid": p.UID, "name": u.Name, "intro": u.Intro}), nil
}

// 反面教材：同一个工具，描述写成这样等于没写。
const badDescription = "查询主题详情"

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"结果序列化失败"}`
	}
	return string(b)
}
