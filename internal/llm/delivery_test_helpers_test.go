package llm

import (
	"context"
	"encoding/json"

	"github.com/GuaiZai233/FrostAgent/internal/core"
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

// sendMessageFinalMessage returns an assistant message that speaks through
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

// sendMessageTextMessage returns an assistant turn that sends one plain text
// component without ending the turn. Segmented replies use extra calls like this
// one before the declared final message.
func sendMessageTextMessage(text string) core.ChatMessage {
	args, _ := json.Marshal(map[string]any{
		"messages": []map[string]any{{"type": "plain", "text": text}},
	})
	return core.ChatMessage{
		Role: core.RoleAssistant,
		ToolCalls: []core.ToolCall{{
			ID:   "fixture-send",
			Type: "function",
			Function: core.ToolCallFunction{
				Name:      "send_message",
				Arguments: string(args),
			},
		}},
	}
}

// sendMessageFinalResponse wraps sendMessageFinalMessage as a provider response.
func sendMessageFinalResponse(text string) *core.ChatResponse {
	return &core.ChatResponse{Message: sendMessageFinalMessage(text)}
}

// echoSendMessageTool delivers its own arguments as the outbound payload, so a
// single plain component is delivered exactly as the model wrote it.
type echoSendMessageTool struct {
	staticTool
	executed int
}

func (t *echoSendMessageTool) ExecuteResult(_ context.Context, args string) (core.ToolExecutionResult, error) {
	t.executed++
	return core.ToolExecutionResult{Content: args, MessagePayload: args}, nil
}

// IsProactiveAllowed lets proactive fixtures deliver their specific reply.
func (t *echoSendMessageTool) IsProactiveAllowed() bool { return true }

// installSendMessage registers the delivery tool used by converted fixtures.
func installSendMessage(e *Engine) *echoSendMessageTool {
	if e.ToolRegistry == nil {
		e.ToolRegistry = make(map[string]ToolExecutor)
	}
	tool := &echoSendMessageTool{staticTool: staticTool{name: "send_message"}}
	e.ToolRegistry["send_message"] = tool
	return tool
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
	return deliveredPlainText(c.payloads[len(c.payloads)-1])
}

// runDelivered installs the echo delivery tool and runs a turn with the capture
// hook so tests can assert on what the model actually delivered.
func runDelivered(e *Engine, messages []ChatMessage, run RunContext) (AgentRunResult, *deliveryCapture) {
	installSendMessage(e)
	capture := &deliveryCapture{}
	run.SendHook = capture.hook
	return e.RunMessagesWithContext(messages, run), capture
}

// deliveredPlainText extracts plain text components from a captured SendHook
// payload.
func deliveredPlainText(payload string) string {
	var decoded struct {
		Messages []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if json.Unmarshal([]byte(payload), &decoded) != nil {
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
