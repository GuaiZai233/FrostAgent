package astrbot

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

// A proactive turn that has already delivered via send_message must end without
// a second message, whether the model then returns a placeholder final or calls
// stay_silent. The delivered text must be the only assistant history entry.
func TestAstrBotDeliveredThenTerminalDoesNotDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal func(message *core.ChatMessage)
	}{
		{"placeholder final", func(message *core.ChatMessage) { message.Content = "(empty)" }},
		{"stay_silent", func(message *core.ChatMessage) {
			message.ToolCalls = []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := &mockLLMProvider{customChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				message := core.ChatMessage{Role: core.RoleAssistant}
				switch calls.Add(1) {
				case 1:
					// Earlier message: stay silent so the trigger has a context target.
					message.ToolCalls = []core.ToolCall{{ID: "wait", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
				case 2:
					message.ToolCalls = []core.ToolCall{{ID: "send", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"fixture-old"},{"type":"plain","text":"confirmed reply"}]}`}}}
				case 3:
					tc.terminal(&message)
				default:
					t.Error("unexpected model round")
				}
				return &core.ChatResponse{Message: message}, nil
			}}
			engine := newTestEngine(provider)
			engine.Scope = newTestScopeWithEnv(t, map[string]string{"PROACTIVE_REPLY_PROBABILITY": "1.00"})
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
			srv, adapter, url := startWSTestServer(engine)
			t.Cleanup(srv.Close)
			conn := dialAutoDecorationTest(t, url)
			t.Cleanup(func() {
				adapter.CloseConnections()
				engine.Scope.Cancel()
				engine.Scope.Wait()
			})

			base := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "fixture-platform", GroupID: "fixture-group", UserID: "fixture-user"}
			earlier := base
			earlier.MessageID, earlier.Content = "fixture-old", "earlier topic"
			sendAutoDecorationEvent(t, conn, earlier)
			if action := readAutoDecorationAction(t, conn, "earlier silence"); action.Action != "noop" {
				t.Fatalf("expected noop for earlier silence: %+v", action)
			}

			trigger := base
			trigger.MessageID, trigger.Content = "fixture-trigger", "later trigger"
			sendAutoDecorationEvent(t, conn, trigger)
			if action := readAutoDecorationAction(t, conn, "delivered reply"); action.Action != "send_message" || action.Content != "confirmed reply" {
				t.Fatalf("missing delivered reply: %+v", action)
			}
			if action := readAutoDecorationAction(t, conn, "terminal"); action.Action != "noop" {
				t.Fatalf("terminal produced a second message: %+v", action)
			}

			session := engine.SessionManager.GetOrCreate(sessionKey(trigger))
			deadline := time.Now().Add(3 * time.Second)
			for {
				delivered := 0
				for _, msg := range session.Snapshot() {
					if msg.Role == "assistant" && msg.Content == "confirmed reply" {
						delivered++
					}
				}
				if delivered == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("want exactly one history entry for the delivered text, got %d: %+v", delivered, session.Snapshot())
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
