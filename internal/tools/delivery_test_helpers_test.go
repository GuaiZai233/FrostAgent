package tools

import (
	"encoding/json"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// sendMessageFinalArgs builds send_message arguments that deliver one plain text
// component and end the turn after the confirmed delivery.
func sendMessageFinalArgs(text string) string {
	args, _ := json.Marshal(map[string]any{
		"messages":              []map[string]any{{"type": "plain", "text": text}},
		"declare_final_message": true,
	})
	return string(args)
}

// sendMessageFinalMessage returns a model message that speaks through
// send_message. Plain assistant text is never delivered by the agent loop.
func sendMessageFinalMessage(text string) core.ChatMessage {
	return core.ChatMessage{
		Role: core.RoleAssistant,
		ToolCalls: []core.ToolCall{{
			ID:   "fixture-final-send",
			Type: "function",
			Function: core.ToolCallFunction{
				Name:      "send_message",
				Arguments: sendMessageFinalArgs(text),
			},
		}},
	}
}

// installSendMessage ensures the delivery tool exists so fixtures can reply.
func installSendMessage(e *llm.Engine) {
	if e.ToolRegistry == nil {
		e.ToolRegistry = make(map[string]llm.ToolExecutor)
	}
	if _, ok := e.ToolRegistry["send_message"]; !ok {
		e.ToolRegistry["send_message"] = SendMsgTool()
	}
}

// deliveryCapture records every payload handed to the transport hook.
type deliveryCapture struct {
	payloads []string
	fail     error
}

func (c *deliveryCapture) hook(payload string) error {
	c.payloads = append(c.payloads, payload)
	return c.fail
}

func (c *deliveryCapture) count() int { return len(c.payloads) }

// plainText joins the plain components of the most recent delivery.
func (c *deliveryCapture) plainText() string {
	if len(c.payloads) == 0 {
		return ""
	}
	var decoded struct {
		Messages []Msg `json:"messages"`
	}
	if json.Unmarshal([]byte(c.payloads[len(c.payloads)-1]), &decoded) != nil {
		return ""
	}
	text := ""
	for _, message := range decoded.Messages {
		if message.Type == "plain" {
			text += message.Text
		}
	}
	return text
}

// lastToolContent returns the content of the last tool message the model saw.
func lastToolContent(messages []llm.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			if text, ok := messages[i].Content.(string); ok {
				return text
			}
			return ""
		}
	}
	return ""
}

// silentFinal keeps a turn free of plain text while allowing tool use.
func silentFinal() llm.ChatMessage {
	return llm.ChatMessage{Role: "assistant"}
}
