package onebot

import (
	"FrostAgent/internal/adapter/onebot/content"
	"FrostAgent/internal/model"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

const (
	defaultBotName    = "霜降狐"
	defaultBotAliases = "霜降,FrostAgent"
)

// HasGroupWakeSignal reports whether a group message explicitly addresses the
// bot, either through a OneBot at segment or a configured literal name/alias.
// The response pipeline intentionally treats the two signals as equivalent.
func HasGroupWakeSignal(event model.OneBotEvent) bool {
	return IsMentionedBot(event) || IsBotNameMentioned(event)
}

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

// IsBotNameMentioned checks text segments only. Configuration values are
// treated as literal strings rather than regular expressions, so aliases such
// as ".*" cannot accidentally wake the bot for every group message.
func IsBotNameMentioned(event model.OneBotEvent) bool {
	if event.MessageType != "group" {
		return false
	}

	names := configuredBotNames()
	if len(names) == 0 {
		return false
	}
	for _, raw := range EventRawMessages(event) {
		if rawMessageMentionsBot(raw, names) {
			return true
		}
	}
	return false
}

func rawMessageMentionsBot(raw json.RawMessage, names []string) bool {
	for _, segment := range ParseMessageSegments(raw) {
		if segment.Type != "text" {
			continue
		}
		text, ok := segment.Data["text"].(string)
		if !ok {
			continue
		}
		for _, name := range names {
			if strings.Contains(text, name) {
				return true
			}
		}
	}
	return false
}

func configuredBotNames() []string {
	name, nameSet := os.LookupEnv("BOT_NAME")
	if !nameSet {
		name = defaultBotName
	}
	aliases, aliasesSet := os.LookupEnv("BOT_ALIASES")
	if !aliasesSet {
		aliases = defaultBotAliases
	}

	values := append([]string{name}, strings.Split(aliases, ",")...)
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
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
