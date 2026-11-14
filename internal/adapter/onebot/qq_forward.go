package onebot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/onebot/content"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

const (
	maxQQForwardResponse = 8 << 20
	maxQQForwardPage     = 32 << 10
	maxQQForwardText     = 2048
)

type qqForwardReference struct {
	id      string
	content json.RawMessage
}

type qqReadForwardNode struct {
	Index  int `json:"index"`
	Sender struct {
		UserID   string `json:"user_id,omitempty"`
		Nickname string `json:"nickname,omitempty"`
	} `json:"sender"`
	Time          int64  `json:"time,omitempty"`
	Text          string `json:"text"`
	TextTruncated bool   `json:"text_truncated,omitempty"`
	ForwardCount  int    `json:"forward_count"`
}

type qqForwardRecord struct {
	senderID string
	name     string
	time     int64
	content  json.RawMessage
}

func (c *wsConnection) readQQForward(ctx context.Context, event model.OneBotEvent, turn *llm.SessionTurn, a tools.QQArguments) (core.PlatformActionResult, error) {
	if turn.Interrupted() {
		return core.PlatformActionResult{}, context.Canceled
	}
	if event.MessageType != "group" && event.MessageType != "private" {
		return core.PlatformActionResult{}, fmt.Errorf("读取转发记录需要群或私聊会话")
	}
	mid, ok := numericMessageID(a.MessageID)
	if !ok || !c.messageSessionMatches(mid, c.historyKey(event)) {
		return core.PlatformActionResult{}, fmt.Errorf("message_id 不在当前连接、当前会话的可信上下文中")
	}
	call := func(action model.OneBotAction) (oneBotAPIResponse, error) {
		if turn.Interrupted() {
			return oneBotAPIResponse{}, context.Canceled
		}
		return c.sendActionAndWaitContext(action, actionACKTimeout(c.Scope), turn, ctx)
	}
	raw := event.Message
	if mid != int64(event.MessageID) {
		response, err := call(model.OneBotAction{Action: "get_msg", Params: map[string]any{"message_id": mid}})
		if err != nil {
			return core.PlatformActionResult{}, err
		}
		resolved := c.resolveReplyResponse(event, mid, response)
		if resolved.MessageID == "" {
			return core.PlatformActionResult{}, fmt.Errorf("无法读取当前会话的来源消息")
		}
		var data oneBotMessageData
		if err := json.Unmarshal(response.Data, &data); err != nil {
			return core.PlatformActionResult{}, err
		}
		raw = data.Message
	}
	if err := ctx.Err(); err != nil {
		return core.PlatformActionResult{}, err
	}
	return readQQForwardPage(raw, a, call)
}

// Only resource IDs extracted from the trusted containing message or its
// descendants reach the vendor API. Record IDs never grant live-message authority.
func readQQForwardPage(raw json.RawMessage, a tools.QQArguments, call func(model.OneBotAction) (oneBotAPIResponse, error)) (core.PlatformActionResult, error) {
	load := func(raw json.RawMessage, index int) ([]qqForwardRecord, error) {
		refs := qqForwardReferences(raw)
		if index < 0 || index >= len(refs) {
			return nil, fmt.Errorf("forward_index %d 不存在（共 %d 个转发记录）", index, len(refs))
		}
		ref := refs[index]
		if len(ref.content) > 0 && string(ref.content) != "null" {
			return decodeQQForwardRecords(ref.content)
		}
		if strings.TrimSpace(ref.id) == "" || len(ref.id) > 1024 {
			return nil, fmt.Errorf("转发记录缺少有效资源 ID")
		}
		// OneBot v11 specifies id; NapCat uses message_id for the same resource.
		// Both aliases carry the trusted resource ID, never the containing message ID.
		response, err := call(model.OneBotAction{Action: "get_forward_msg", Params: map[string]any{"id": ref.id, "message_id": ref.id}})
		if err != nil {
			return nil, err
		}
		if len(response.Data) > maxQQForwardResponse {
			return nil, fmt.Errorf("转发记录响应超过 8 MiB")
		}
		var data struct {
			Messages json.RawMessage `json:"messages"`
			Message  json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(response.Data, &data); err != nil {
			return nil, fmt.Errorf("转发记录响应无效: %w", err)
		}
		nodes := data.Messages
		if len(nodes) == 0 || string(nodes) == "null" {
			nodes = data.Message
		}
		return decodeQQForwardRecords(nodes)
	}
	records, err := load(raw, a.ForwardIndex)
	if err != nil {
		return core.PlatformActionResult{}, err
	}
	for _, step := range a.Path {
		if step.NodeIndex < 0 || step.NodeIndex >= len(records) {
			return core.PlatformActionResult{}, fmt.Errorf("嵌套 node_index %d 不存在", step.NodeIndex)
		}
		records, err = load(records[step.NodeIndex].content, step.ForwardIndex)
		if err != nil {
			return core.PlatformActionResult{}, err
		}
	}
	if a.Offset < 0 || a.Offset > len(records) {
		return core.PlatformActionResult{}, fmt.Errorf("offset 超出转发记录范围（共 %d 条）", len(records))
	}
	limit := a.Limit
	if limit == 0 {
		limit = 20
	}
	page := struct {
		Status       string                `json:"status"`
		MessageID    string                `json:"message_id"`
		ForwardIndex int                   `json:"forward_index"`
		Path         []tools.QQForwardStep `json:"path,omitempty"`
		Total        int                   `json:"total"`
		Offset       int                   `json:"offset"`
		Nodes        []qqReadForwardNode   `json:"nodes"`
		HasMore      bool                  `json:"has_more"`
		NextOffset   int                   `json:"next_offset"`
	}{Status: "ok", MessageID: a.MessageID, ForwardIndex: a.ForwardIndex, Path: a.Path, Total: len(records), Offset: a.Offset, Nodes: []qqReadForwardNode{}}
	size := 0
	for index := a.Offset; index < len(records) && len(page.Nodes) < limit; index++ {
		record := records[index]
		node := qqReadForwardNode{Index: index, Time: record.time, ForwardCount: len(qqForwardReferences(record.content))}
		node.Sender.UserID, _ = qqForwardClip(record.senderID, 128)
		node.Sender.Nickname, _ = qqForwardClip(record.name, 128)
		text, _ := historyMessageText(record.content)
		node.Text, node.TextTruncated = qqForwardClip(text, maxQQForwardText)
		encoded, _ := json.Marshal(node)
		// Reserve space for page metadata, including the bounded traversal path.
		if size+len(encoded) > maxQQForwardPage-2048 {
			break
		}
		page.Nodes = append(page.Nodes, node)
		size += len(encoded) + 1
	}
	page.NextOffset = a.Offset + len(page.Nodes)
	page.HasMore = page.NextOffset < len(records)
	encoded, err := json.Marshal(page)
	return core.PlatformActionResult{Content: string(encoded)}, err
}

func qqForwardReferences(raw json.RawMessage) []qqForwardReference {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		var refs []qqForwardReference
		for _, code := range historyCQCode.FindAllString(text, -1) {
			kind, params, _ := strings.Cut(code[len("[CQ:"):len(code)-1], ",")
			if kind == "forward" {
				refs = append(refs, qqForwardReference{id: historyCQParam(params, "id")})
			}
		}
		return refs
	}
	var segments []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &segments) != nil {
		return nil
	}
	var refs []qqForwardReference
	for _, segment := range segments {
		if segment.Type == "forward" {
			var data struct {
				ID      json.RawMessage `json:"id"`
				Content json.RawMessage `json:"content"`
			}
			_ = json.Unmarshal(segment.Data, &data)
			refs = append(refs, qqForwardReference{id: historyRawID(data.ID), content: data.Content})
		}
	}
	return refs
}

func decodeQQForwardRecords(raw json.RawMessage) ([]qqForwardRecord, error) {
	var entries []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || len(raw) > maxQQForwardResponse || json.Unmarshal(raw, &entries) != nil {
		return nil, fmt.Errorf("转发记录缺少有效的消息数组")
	}
	records := make([]qqForwardRecord, 0, len(entries))
	for _, entry := range entries {
		var wrapper struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(entry, &wrapper) != nil {
			return nil, fmt.Errorf("转发记录节点无效")
		}
		if wrapper.Type == "node" {
			entry = wrapper.Data
		}
		var node struct {
			Sender struct {
				UserID   json.RawMessage `json:"user_id"`
				Nickname string          `json:"nickname"`
				Card     string          `json:"card"`
			} `json:"sender"`
			UserID   json.RawMessage `json:"user_id"`
			UIN      json.RawMessage `json:"uin"`
			Nickname string          `json:"nickname"`
			Name     string          `json:"name"`
			Time     json.RawMessage `json:"time"`
			Message  json.RawMessage `json:"message"`
			Content  json.RawMessage `json:"content"`
		}
		if json.Unmarshal(entry, &node) != nil {
			return nil, fmt.Errorf("转发记录节点格式无效")
		}
		body := node.Message
		if len(body) == 0 || string(body) == "null" {
			body = node.Content
		}
		var text string
		if len(body) == 0 || string(body) == "null" {
			return nil, fmt.Errorf("转发记录节点缺少可读取内容")
		}
		if json.Unmarshal(body, &text) != nil {
			var segments []content.MessageSegment
			if json.Unmarshal(body, &segments) != nil {
				return nil, fmt.Errorf("转发记录节点消息段无效")
			}
			for _, segment := range segments {
				if segment.Type == "" {
					return nil, fmt.Errorf("转发记录节点消息段缺少类型")
				}
			}
		}
		id := historyRawID(node.Sender.UserID)
		if id == "" {
			id = historyRawID(node.UserID)
		}
		if id == "" {
			id = historyRawID(node.UIN)
		}
		name := node.Sender.Card
		if name == "" {
			name = node.Sender.Nickname
		}
		if name == "" {
			name = node.Nickname
		}
		if name == "" {
			name = node.Name
		}
		timestamp, _ := strconv.ParseInt(historyRawID(node.Time), 10, 64)
		records = append(records, qqForwardRecord{senderID: id, name: name, time: timestamp, content: body})
	}
	return records, nil
}

func qqForwardClip(value string, limit int) (string, bool) {
	if utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	return string([]rune(value)[:limit]), true
}
