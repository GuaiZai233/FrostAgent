package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/model"
	"encoding/json"
	"strconv"
)

func IsMentionedBot(event model.OneBotEvent) bool {
	if event.MessageType != "group" {
		return false
	}

	selfIDStr := strconv.FormatInt(event.SelfID, 10)
	for _, raw := range EventRawMessages(event) {
		segments := ParseMessageSegments(raw)
		/*
			In OneBot V11 protocol, underlying implementation of `at` component is different,
			so use type assertion here.
		*/
		for _, seg := range segments {
			if seg.Type == "at" {
				qqVal := seg.Data["qq"]
				var atQQ string

				switch v := qqVal.(type) {
				case string:
					atQQ = v
				case float64:
					atQQ = strconv.FormatFloat(v, 'f', -1, 64)
				case json.Number:
					atQQ = v.String()
				}

				if atQQ == selfIDStr {
					return true
				}
			}
		}
	}

	return false
}

// EventRawMessages returns all raw messages carried by a OneBot event.
// `messages` is a FrostAgent extension for multi-context input; `message` is
// kept as the standard OneBot fallback.
func EventRawMessages(event model.OneBotEvent) []json.RawMessage {
	if len(event.Messages) > 0 && string(event.Messages) != "null" {
		var raws []json.RawMessage
		if err := json.Unmarshal(event.Messages, &raws); err == nil {
			return raws
		}
	}
	if len(event.Message) > 0 && string(event.Message) != "null" {
		return []json.RawMessage{event.Message}
	}
	return nil
}

// ParseMessageSegments 兼容解析 OneBot 消息字段。
// 标准 OneBot 消息是 []MessageSegment；部分实现或上游适配层会传入纯字符串，
// 这里统一转换成 text 消息段，避免多上下文/连续消息场景下解析失败后把 JSON 原文发给模型。
func ParseMessageSegments(raw json.RawMessage) []content.MessageSegment {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var segments []content.MessageSegment
	if err := json.Unmarshal(raw, &segments); err == nil {
		return segments
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []content.MessageSegment{{Type: "text", Data: map[string]any{"text": text}}}
	}

	return nil
}
