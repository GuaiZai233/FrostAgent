package astrbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"github.com/gorilla/websocket"
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
		{"second tool call", true, ""},
		{"stay silent after delivery", true, ""},
		{"provider error after delivery", true, ""},
		{"security block after delivery", true, "blocked final"},
		{"iteration limit after delivery", true, ""},
		{"structured text stays literal", true, `{"messages":[{"type":"plain","text":"reply about earlier topic"}]}`},
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
					if tc.name == "second tool call" {
						message.ToolCalls = []core.ToolCall{{ID: "second", Type: "function", Function: core.ToolCallFunction{
							Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"msg_earlier"},{"type":"plain","text":"second reply"}]}`,
						}}}
					}
					result, _ := req.Messages[len(req.Messages)-1].Content.(string)
					if (result == "消息已发送") != sameGroup {
						t.Errorf("incorrect tool confirmation: %s", result)
					}
					if tc.name == "provider error after delivery" {
						return nil, errors.New("fixture provider failure")
					}
					if tc.name == "stay silent after delivery" {
						message.ToolCalls = []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: "stay_silent", Arguments: `{}`}}}
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
			if tc.name == "iteration limit after delivery" {
				engine.MaxIterations = 1
			}
			if tc.name == "security block after delivery" {
				engine.Security = security.NewController(t.TempDir())
				engine.Security.SetMode(security.ControlModeAggressive)
				engine.Security.Watchdog.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if input.Stage == security.StageModelOutput {
						return security.ClassificationResult{Category: security.RiskCategoryViolenceTerrorism, RiskLevel: security.RiskLevelCritical}, nil
					}
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				}})
			}
			srv, _, url := startWSTestServer(engine)
			defer srv.Close()
			conn := dialAutoDecorationTest(t, url)
			older := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "fixture-platform", GroupID: "fixture-group", UserID: "fixture-author", MessageID: "msg_earlier", Content: "earlier central topic"}
			sendAutoDecorationEvent(t, conn, older)
			if action := readAutoDecorationAction(t, conn, "first silence"); action.Action != "noop" {
				t.Fatalf("expected initial silence, got %+v", action)
			}
			waitForTurn := func(event Event) *llm.SessionContext {
				t.Helper()
				session := engine.SessionManager.GetOrCreate(sessionKey(event))
				barrier := session.ReserveSequenceTurn("proactive:"+event.Platform, false, true)
				completed := make(chan struct{})
				go func() { barrier.Wait(); barrier.Done(); close(completed) }()
				select {
				case <-completed:
				case <-time.After(5 * time.Second):
					t.Fatal("reply turn did not finish")
				}
				return session
			}
			// The terminal frame precedes turn cleanup. Wait before the next
			// proactive event can cancel this turn and emit another noop.
			waitForTurn(older)
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
			if tc.final == "different follow-up" || tc.name == "structured text stays literal" {
				if terminal.Action != "send_message" || terminal.Content != tc.final {
					t.Fatalf("different final reply was lost: %+v", terminal)
				}
			} else if terminal.Action != "noop" || !terminal.SuppressLLM {
				t.Fatalf("duplicate or visible error instead of terminal noop: %+v", terminal)
			}
			session := waitForTurn(trigger)
			if sameGroup && tc.final != "different follow-up" && tc.name != "structured text stays literal" {
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
			wantCalls := 3
			if tc.name == "iteration limit after delivery" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("model calls = %d", calls)
			}
		})
	}
}

func TestAstrBotProactiveSpecificReplyLateTerminal(t *testing.T) {
	for _, final := range []string{"", "different final"} {
		t.Run("final="+final, func(t *testing.T) {
			gate := &injectionWriteGate{started: make(chan struct{}), release: make(chan struct{})}
			var releaseOnce sync.Once
			var calls atomic.Int32
			engine := newTestEngine(runtimeInjectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				message := core.ChatMessage{Role: core.RoleAssistant}
				switch calls.Add(1) {
				case 1:
					message.ToolCalls = []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: "stay_silent", Arguments: `{}`}}}
				case 2:
					message.ToolCalls = []core.ToolCall{{ID: "specific", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{"messages":[{"type":"mention_specific","message_id":"fixture-old"},{"type":"plain","text":"confirmed reply"}]}`}}}
				case 3:
					// The tool write has returned successfully. Only the terminal
					// operation is now held inside the real WebSocket transport.
					gate.enabled.Store(true)
					message.Content = final
				default:
					t.Error("unexpected model round")
				}
				return &core.ChatResponse{Message: message}, nil
			}))
			engine.Scope = newTestScopeWithEnv(t, map[string]string{
				"PROACTIVE_REPLY_PROBABILITY": "1.00", "MEMORY_EXTRACT_BATCH_MIN": "100", "MEMORY_EXTRACT_BATCH_MAX": "100",
			})
			engine.MemoryWriter = memory.NewWriter(nil)
			engine.ToolRegistry["send_message"] = tools.SendMsgTool()
			engine.ToolRegistry["stay_silent"] = tools.StaySilentTool()
			srv := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
			srv.Listener = injectionGatedListener{srv.Listener, gate}
			srv.Start()
			defer srv.Close()
			defer releaseOnce.Do(func() { close(gate.release) })
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			event := Event{Type: "event", EventType: "message", MessageType: "group", Platform: "fixture-platform", GroupID: "fixture-group", UserID: "fixture-user", MessageID: "fixture-old", Content: "earlier topic"}
			sendAutoDecorationEvent(t, conn, event)
			if action := readAutoDecorationAction(t, conn, "first silence"); action.Action != "noop" {
				t.Fatalf("expected noop: %+v", action)
			}
			session := engine.SessionManager.GetOrCreate(sessionKey(event))
			waitTurn := func(turn *llm.SessionTurn) {
				t.Helper()
				done := make(chan struct{})
				go func() { turn.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("turn did not finish")
				}
			}
			lane := "proactive:" + event.Platform
			barrier := session.ReserveSequenceTurn(lane, false, true)
			waitTurn(barrier)
			barrier.Done()
			initialPending := session.PendingTurnCount()
			event.MessageID, event.Content = "fixture-trigger", "later trigger"
			sendAutoDecorationEvent(t, conn, event)
			if action := readAutoDecorationAction(t, conn, "specific reply"); action.Action != "send_message" || action.Content != "confirmed reply" {
				t.Fatalf("missing confirmed reply: %+v", action)
			}
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal write not blocked")
			}
			// Interrupt without resetting the epoch. Waiting for this turn proves
			// the previous reply's deferred CommitDelivered ran before release.
			next := session.ReserveSequenceTurn(lane, true, true)
			defer next.Done()
			waitTurn(next)
			countReply := func() (int, int) {
				history, compact := 0, 0
				for _, message := range session.Snapshot() {
					if message.Role == "assistant" && message.Content == "confirmed reply" {
						history++
					}
				}
				for _, message := range session.GroupCompactBufferMessages() {
					if message.Role == "assistant" && message.Content == "confirmed reply" {
						compact++
					}
				}
				return history, compact
			}
			if history, compact := countReply(); history != 1 || compact != 1 {
				t.Fatalf("fallback history=%d compact=%d", history, compact)
			}
			releaseOnce.Do(func() { close(gate.release) })
			terminal := readAutoDecorationAction(t, conn, "late terminal")
			if final == "" && terminal.Action != "noop" || final != "" && terminal.Content != final {
				t.Fatalf("late terminal lost: %+v", terminal)
			}
			// The terminal frame precedes history/automatic extraction. Wait for
			// the callback's queue admission, then synchronize its epoch commit.
			deadline := time.Now().Add(3 * time.Second)
			for session.PendingTurnCount() == initialPending && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			session.WithEpochCommit(session.Epoch(), func() {})
			if history, compact := countReply(); history != 1 || compact != 1 {
				t.Fatalf("late callback duplicated reply: history=%d compact=%d", history, compact)
			}
			if got := session.PendingTurnCount() - initialPending; got != 1 {
				t.Fatalf("automatic extraction queued %d times, want 1", got)
			}
			if final != "" {
				history, compact := 0, 0
				for _, message := range session.Snapshot() {
					if message.Role == "assistant" && message.Content == final {
						history++
					}
				}
				for _, message := range session.GroupCompactBufferMessages() {
					if message.Role == "assistant" && message.Content == final {
						compact++
					}
				}
				if history != 1 || compact != 1 {
					t.Fatalf("different delivered final lost: history=%d compact=%d", history, compact)
				}
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
