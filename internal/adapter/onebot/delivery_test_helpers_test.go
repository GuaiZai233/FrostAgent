package onebot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

// installTestSendMessage registers the production delivery tool in a fixture
// engine so the model has a channel for user-visible text.
func installTestSendMessage(engine *llm.Engine) {
	if engine.ToolRegistry == nil {
		engine.ToolRegistry = make(map[string]llm.ToolExecutor)
	}
	if _, ok := engine.ToolRegistry["send_message"]; !ok {
		engine.ToolRegistry["send_message"] = tools.SendMsgTool()
	}
}

// sendMessageFinalMessage returns a model message that delivers one plain text
// component and ends the turn after the confirmed delivery.
func sendMessageFinalMessage(text string) core.ChatMessage {
	args, _ := json.Marshal(map[string]any{
		"messages":              []map[string]any{{"type": "plain", "text": text}},
		"declare_final_message": true,
	})
	return core.ChatMessage{
		Role: core.RoleAssistant,
		ToolCalls: []core.ToolCall{{
			ID:   "fixture-final-send",
			Type: "function",
			Function: core.ToolCallFunction{
				Name:      "send_message",
				Arguments: string(args),
			},
		}},
	}
}

// proactiveSpecificReplyMessage returns the only send_message shape allowed in
// proactive turns: a quote target followed by the reply text.
func proactiveSpecificReplyMessage(messageID, text string) core.ChatMessage {
	return proactiveSpecificReplyFinal(messageID, text, false)
}

// proactiveSpecificReplyFinal builds a specific reply. When final is true the
// call sets declare_final_message, so the turn ends after its delivery.
func proactiveSpecificReplyFinal(messageID, text string, final bool) core.ChatMessage {
	payload := map[string]any{
		"messages": []map[string]any{
			{"type": "mention_specific", "message_id": messageID},
			{"type": "plain", "text": text},
		},
	}
	if final {
		payload["declare_final_message"] = true
	}
	args, _ := json.Marshal(payload)
	return core.ChatMessage{
		Role: core.RoleAssistant,
		ToolCalls: []core.ToolCall{{
			ID:   "fixture-specific-send",
			Type: "function",
			Function: core.ToolCallFunction{
				Name:      "send_message",
				Arguments: string(args),
			},
		}},
	}
}

// outboundText flattens either a plain string message body or a OneBot segment
// chain back into its visible text.
func outboundText(body any) string {
	if text, ok := body.(string); ok {
		return text
	}
	segments, ok := body.([]any)
	if !ok {
		return ""
	}
	text := ""
	for _, segment := range segments {
		part, ok := segment.(map[string]any)
		if !ok || part["type"] != "text" {
			continue
		}
		if data, ok := part["data"].(map[string]any); ok {
			if value, ok := data["text"].(string); ok {
				text += value
			}
		}
	}
	return text
}

// deliverMockReply makes fixture responses comply with the delivery contract:
// plain assistant text is never delivered, so an ordinary text reply is sent
// through send_message instead. Tool calls, silence markers and placeholder
// finals are left untouched.
func deliverMockReply(resp *core.ChatResponse) *core.ChatResponse {
	if resp == nil || len(resp.Message.ToolCalls) > 0 {
		return resp
	}
	text, ok := resp.Message.Content.(string)
	if !ok || strings.TrimSpace(text) == "" || text == llm.AssistantSilentMarker || llm.IsPlaceholderFinal(text) {
		return resp
	}
	resp.Message = sendMessageFinalMessage(text)
	return resp
}

// readOutboundText reads the next outbound chat action and returns its visible
// text. send_message deliveries arrive as a OneBot segment chain, while direct
// replies and receipts may still be plain strings.
func readOutboundText(t *testing.T, conn *websocket.Conn, timeout time.Duration) (model.OneBotAction, string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	for {
		var action model.OneBotAction
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatalf("read outbound action: %v", err)
		}
		if action.Action == "get_group_info" || action.Action == "set_input_status" {
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 13, "group_name": "synthetic-group"}}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		params, _ := action.Params.(map[string]any)
		return action, outboundText(params["message"])
	}
}

// collectOutboundText reads outbound chat actions until stop accepts the
// accumulated text or the deadline expires, acknowledging each action so the
// adapter can continue.
func collectOutboundText(t *testing.T, conn *websocket.Conn, timeout time.Duration, stop func(string) bool) (string, []model.OneBotAction) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var all strings.Builder
	var actions []model.OneBotAction
	for time.Now().Before(deadline) {
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var action model.OneBotAction
		if err := conn.ReadJSON(&action); err != nil {
			break
		}
		if action.Action == "get_group_info" || action.Action == "set_input_status" {
			if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"group_id": 13, "group_name": "synthetic-group"}}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 1}}); err != nil {
			t.Fatal(err)
		}
		params, _ := action.Params.(map[string]any)
		if text := outboundText(params["message"]); text != "" {
			all.WriteString(text)
		}
		actions = append(actions, action)
		if stop != nil && stop(all.String()) {
			break
		}
	}
	return all.String(), actions
}

// sendMessageArgsMessage delivers raw send_message arguments, letting a fixture
// choose the component chain (for example a sticker image) itself.
func sendMessageArgsMessage(args string) core.ChatMessage {
	return core.ChatMessage{
		Role: core.RoleAssistant,
		ToolCalls: []core.ToolCall{{
			ID:   "fixture-args-send",
			Type: "function",
			Function: core.ToolCallFunction{
				Name:      "send_message",
				Arguments: args,
			},
		}},
	}
}

// proactiveSpecificReplySchema reports whether the request exposes the narrowed
// proactive send_message schema instead of the ordinary delivery schema.
func proactiveSpecificReplySchema(available []core.Tool) bool {
	for _, tool := range available {
		if tool.Name != "send_message" {
			continue
		}
		encoded, _ := json.Marshal(tool.Parameters)
		// 主动回复的窄 schema 只允许 mention_specific 与 plain 两种组件。
		return strings.Contains(string(encoded), `"enum":["mention_specific","plain"]`)
	}
	return false
}

// deliveryCapture collects every payload handed to a transport SendHook so
// fixtures can assert on what the model actually delivered.
type deliveryCapture struct {
	payloads []string
}

func (c *deliveryCapture) hook(payload string) error {
	c.payloads = append(c.payloads, payload)
	return nil
}

// plainText returns the plain text of the most recent delivery.
func (c *deliveryCapture) plainText() string {
	if len(c.payloads) == 0 {
		return ""
	}
	decoded, _ := json.Marshal(c.payloads[len(c.payloads)-1])
	_ = decoded
	var payload struct{ Messages []map[string]any }
	if json.Unmarshal([]byte(c.payloads[len(c.payloads)-1]), &payload) != nil {
		return ""
	}
	text := ""
	for _, message := range payload.Messages {
		if message["type"] == "plain" {
			if value, ok := message["text"].(string); ok {
				text += value
			}
		}
	}
	return text
}
