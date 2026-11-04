package astrbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestAstrBotProactiveSpecificReplyEarlierMessage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sameGroup bool
		final     string
	}{
		{"empty final", true, ""},
		{"duplicate final", true, "reply about earlier topic"},
		{"whitespace duplicate", true, " \nreply about earlier topic\n"},
		{"different final", true, "different follow-up"},
		{"foreign target", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sameGroup := tc.sameGroup
			calls := 0
			provider := &mockLLMProvider{customChat: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				calls++
				message := core.ChatMessage{Role: core.RoleAssistant}
				switch calls {
				case 1:
					message.ToolCalls = []core.ToolCall{{ID: "wait", Type: "function", Function: core.ToolCallFunction{Name: llm.StaySilentToolName, Arguments: `{}`}}}
				case 2:
					prompt, _ := req.Messages[len(req.Messages)-1].Content.(string)
					if !strings.Contains(prompt, proactive.PromptPrefix) {
						t.Error("missing proactive guidance")
					}
					if sameGroup && (!strings.Contains(prompt, `"message_id":"msg_earlier"`) || !strings.Contains(prompt, "earlier central topic")) {
						t.Errorf("earlier message is not searchable in model context: %s", prompt)
					}
					message.ToolCalls = []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{
						Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"msg_earlier"},{"type":"plain","text":"reply about earlier topic"}]}`,
					}}}
				case 3:
					message.Content = tc.final
					result, _ := req.Messages[len(req.Messages)-1].Content.(string)
					if (result == "消息已发送") != sameGroup {
						t.Errorf("incorrect tool confirmation: %s", result)
					}
				default:
					t.Errorf("unexpected model round %d", calls)
				}
				return &core.ChatResponse{Message: message}, nil
			}}
			engine := newTestEngine(provider)
			engine.Scope = newTestScopeWithEnv(t, map[string]string{
				"PROACTIVE_REPLY_PROBABILITY": "1.00",
				"ENABLE_REPLY_IN_GROUP_MSG":   "true", "ENABLE_AT_IN_GROUP_MSG": "true",
			})
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
			srv, _, url := startWSTestServer(engine)
			defer srv.Close()
			conn := dialAutoDecorationTest(t, url)
			older := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "fixture-platform", GroupID: "fixture-group", UserID: "fixture-author", MessageID: "msg_earlier", Content: "earlier central topic"}
			sendAutoDecorationEvent(t, conn, older)
			if action := readAutoDecorationAction(t, conn, "first silence"); action.Action != "noop" {
				t.Fatalf("expected initial silence, got %+v", action)
			}
			trigger := older
			trigger.MessageID, trigger.UserID, trigger.Content = "msg_trigger", "fixture-other-author", "later unrelated trigger"
			if !sameGroup {
				trigger.GroupID = "fixture-other-group"
			}
			sendAutoDecorationEvent(t, conn, trigger)
			if sameGroup {
				action := readAutoDecorationAction(t, conn, "specific reply")
				if action.Action != "send_message" || len(action.Messages) != 2 || action.Messages[0].Type != "quote" || action.Messages[0].MessageID != older.MessageID || action.Messages[1].Type != "plain" || action.Messages[1].Text != "reply about earlier topic" {
					t.Fatalf("wrong quote, trigger mention, or lost text: %+v", action)
				}
			}
			terminal := readAutoDecorationAction(t, conn, "terminal noop")
			if tc.final == "different follow-up" {
				if terminal.Action != "send_message" || terminal.Content != tc.final {
					t.Fatalf("different final reply was lost: %+v", terminal)
				}
			} else if terminal.Action != "noop" || !terminal.SuppressLLM {
				t.Fatalf("duplicate or visible error instead of terminal noop: %+v", terminal)
			}
			session := engine.SessionManager.GetOrCreate(sessionKey(trigger))
			barrier := session.ReserveSequenceTurn("proactive:"+trigger.Platform, false, true)
			completed := make(chan struct{})
			go func() { barrier.Wait(); barrier.Done(); close(completed) }()
			select {
			case <-completed:
			case <-time.After(5 * time.Second):
				t.Fatal("reply turn did not finish")
			}
			if sameGroup && tc.final != "different follow-up" {
				historyCopies, compactCopies := 0, 0
				for _, message := range session.Snapshot() {
					if message.Role == "assistant" && message.Content == "reply about earlier topic" {
						historyCopies++
					}
				}
				for _, message := range session.GroupCompactBufferMessages() {
					if message.Role == "assistant" && message.Content == "reply about earlier topic" {
						compactCopies++
					}
				}
				if historyCopies != 1 || compactCopies != 1 {
					t.Fatalf("confirmed reply history copies=%d compact copies=%d, want 1 each", historyCopies, compactCopies)
				}
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if calls != 3 {
				t.Fatalf("model calls = %d", calls)
			}
		})
	}
}

func TestAstrBotSpecificReplyObservationIsolation(t *testing.T) {
	conn, otherConn := newWSConn(nil), newWSConn(nil)
	defer conn.Close()
	defer otherConn.Close()
	event := Event{Type: "event", EventType: "message", Platform: "fixture-platform", SessionID: "fixture-session", MessageType: "group", GroupID: "fixture-group", UserID: "fixture-user", MessageID: "fixture-message"}
	conn.rememberReplyTarget(event)
	if !conn.observedReplyTarget(event, event.MessageID) || otherConn.observedReplyTarget(event, event.MessageID) {
		t.Fatal("observation crossed connections or was not recorded")
	}
	for _, alter := range []func(*Event){
		func(e *Event) { e.GroupID = "other-group" },
		func(e *Event) { e.Platform = "other-platform" },
		func(e *Event) { e.MessageType = "private" },
		func(e *Event) { e.SessionID = "other-session" },
	} {
		other := event
		alter(&other)
		if conn.observedReplyTarget(other, event.MessageID) {
			t.Fatalf("cross-scope message trusted: %+v", other)
		}
	}
	for i := 0; i < maxReplyTargets; i++ {
		next := event
		next.MessageID = fmt.Sprintf("fixture-%d", i)
		conn.rememberReplyTarget(next)
	}
	if conn.observedReplyTarget(event, event.MessageID) {
		t.Fatal("old target not evicted")
	}
	if len(conn.replyTargets) != maxReplyTargets {
		t.Fatal("observation cache is not bounded")
	}
}

func TestAstrBotSpecificReplyPreservesExplicitMention(t *testing.T) {
	t.Setenv("ENABLE_AT_IN_GROUP_MSG", "true")
	t.Setenv("ENABLE_REPLY_IN_GROUP_MSG", "true")
	action := Action{Action: "send_message", MessageType: "group", UserID: "trigger-user", ReplyMessageID: "trigger-message", suppressAutoMention: true,
		Messages: []ActionMessage{{Type: "quote", MessageID: "older-message"}, {Type: "mention_user", MentionUserID: "chosen-user"}, {Type: "plain", Text: "reply"}},
	}
	data, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	var normalized Action
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatal(err)
	}
	if len(normalized.Messages) != 3 || normalized.Messages[1].MentionUserID != "chosen-user" {
		t.Fatalf("explicit mention changed or trigger mentioned: %+v", normalized)
	}
}
