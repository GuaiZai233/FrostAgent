package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const DefaultMaxMessages = 20

// TrimMessages 保留 system 消息，并裁剪最近 limit 条非 system 消息。
func TrimMessages(messages []ChatMessage, limit int) []ChatMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}

	var systemMessages []ChatMessage
	var normalMessages []ChatMessage
	for _, msg := range messages {
		if msg.Role == "system" {
			systemMessages = append(systemMessages, msg)
		} else {
			normalMessages = append(normalMessages, msg)
		}
	}

	if len(normalMessages) > limit {
		normalMessages = normalMessages[len(normalMessages)-limit:]
	}

	trimmed := make([]ChatMessage, 0, len(systemMessages)+len(normalMessages))
	trimmed = append(trimmed, systemMessages...)
	trimmed = append(trimmed, normalMessages...)
	return trimmed
}

// ApproxTokenCount 使用轻量级估算，便于后续按 token 裁剪。
func ApproxTokenCount(messages []ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += 4 // role / message overhead
		total += approxTokens(msg.Role)
		total += approxTokens(contentToText(msg.Content))
		for _, tc := range msg.ToolCalls {
			total += approxTokens(tc.ID) + approxTokens(tc.Type) + approxTokens(tc.Function.Name) + approxTokens(tc.Function.Arguments)
		}
		total += approxTokens(msg.ToolCallID)
	}
	return total
}

func approxTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// 混合中英文场景下的粗略估算：平均 4 字符约 1 token，至少 1 token。
	return len([]rune(s))/4 + 1
}

func contentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(bytes)
	}
}
