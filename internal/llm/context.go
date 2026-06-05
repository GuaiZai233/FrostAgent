package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const DefaultMaxMessages = 20

// TrimMessages 保留 system 消息，并裁剪最近 limit 条非 system 消息。
// It keeps assistant tool_calls and their following tool messages together so
// the OpenAI-compatible API never receives orphaned tool messages.
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

	if len(normalMessages) <= limit {
		trimmed := append([]ChatMessage{}, systemMessages...)
		return append(trimmed, normalMessages...)
	}

	start := len(normalMessages) - limit
	start = adjustToolAwareStart(normalMessages, start)

	trimmed := make([]ChatMessage, 0, len(systemMessages)+len(normalMessages)-start)
	trimmed = append(trimmed, systemMessages...)
	trimmed = append(trimmed, normalMessages[start:]...)
	return trimmed
}

func adjustToolAwareStart(messages []ChatMessage, start int) int {
	if start <= 0 || start >= len(messages) {
		return start
	}
	// Do not start with a tool response; include its assistant tool_calls owner.
	for start > 0 && messages[start].Role == "tool" {
		start--
	}
	// If an assistant with tool_calls is kept, keep all immediately following
	// tool responses even when this slightly exceeds the configured limit.
	for start > 0 && messages[start-1].Role == "assistant" && len(messages[start-1].ToolCalls) > 0 {
		start--
	}
	return start
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
