package astrbot

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimeinfo"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/gorilla/websocket"
)

func runtimeStatusEvent() Event {
	return Event{Type: "event", EventType: "message", MessageType: "group", MessageID: "synthetic-message",
		Platform: "synthetic-platform", SelfID: "synthetic-bot", UserID: "synthetic-user", GroupID: "synthetic-group",
		Content: "#fa", Metadata: map[string]any{"runtime_info_command": true}}
}

func TestRuntimeStatusRequiresCurrentComponentMarker(t *testing.T) {
	if !isRuntimeStatusCandidate(runtimeStatusEvent()) {
		t.Fatal("current command not recognized")
	}
	for name, change := range map[string]func(*Event){
		"legacy-flattened-text": func(e *Event) { e.Metadata = nil },
		"quoted-only":           func(e *Event) { e.Metadata["runtime_info_command"] = false },
		"untyped-marker":        func(e *Event) { e.Metadata["runtime_info_command"] = "true" },
		"private":               func(e *Event) { e.MessageType = "private" },
		"notice":                func(e *Event) { e.EventType = "notice" },
		"delivery-result":       func(e *Event) { e.Type = "delivery_result" },
		"poke":                  func(e *Event) { e.pokeID = "synthetic-poke" },
	} {
		event := runtimeStatusEvent()
		change(&event)
		event.Messages = []string{"#fa"}
		if isRuntimeStatusCandidate(event) {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestWSRuntimeStatusBypassesConversationAndHotToggles(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newAdminTestScope(t, map[string]string{
		"GROUP_REPLY_ON_MENTION": "false", "ENABLE_PROACTIVE_REPLY": "true", "GROUP_REPLY_PROBABILITY": "1",
		"ADMIN_COMMAND_PREFIX": "#", "ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true",
		"BOT_LOOP_USER_IDS": "synthetic-user", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	defer engine.Cancel()
	engine.ModelRouter = modelrouter.New(filepath.Join(t.TempDir(), "router.json"))
	engine.Version = "1.0.0-rc.1"
	engine.StartedAt = time.Now().Add(-(233*24*time.Hour + 21*time.Hour + 16*time.Minute))
	engine.Security = security.NewController(t.TempDir())
	var classifications atomic.Int32
	engine.Security.SetClassifier(&mockClassifier{fn: func(context.Context, security.ClassificationInput) (security.ClassificationResult, error) {
		classifications.Add(1)
		return security.ClassificationResult{}, nil
	}})
	dispatcher := &pokeTestDispatcher{}
	adapter := NewAdapter(engine)
	adapter.SetIncomingDispatcher(dispatcher)
	server, url, done := startPokeAdapterServer(adapter)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, value := range []string{"", "false", "true", ""} {
		if err := engine.Scope.Config.Update(runtimeinfo.EnabledEnv, value, false); err != nil {
			t.Fatal(err)
		}
		event := runtimeStatusEvent()
		event.Content = "  #fa\n"
		event.Metadata["reply_message_id"] = "synthetic-quote"
		event.Messages = []string{"unrelated history"}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if !action.SuppressLLM || action.Echo != "reply_"+event.MessageID {
			t.Fatalf("not terminal: %+v", action)
		}
		if value == "false" {
			if action.Action != "noop" || action.Content != "" {
				t.Fatalf("disabled command replied: %+v", action)
			}
			continue
		}
		if action.Action != "send_message" || action.TargetID != event.GroupID || action.GroupID != event.GroupID || len(action.Messages) != 0 || len(action.Attachments) != 0 {
			t.Fatalf("unexpected decoration/routing: %+v", action)
		}
		if action.Content != runtimeinfo.Format(engine.Version, engine.StartedAt, time.Now()) {
			t.Fatalf("bad diagnostic: %+v", action)
		}
	}
	closePokeConnection(t, conn, done)
	if engine.SessionManager.Count() != 0 || dispatcher.calls.Load() != 0 || classifications.Load() != 0 {
		t.Fatal("diagnostic entered conversation pipeline")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 0 {
		t.Fatal("diagnostic called a model")
	}
}

func TestWSRuntimeStatusRejectedSuppressesFallback(t *testing.T) {
	for _, reason := range []string{"disabled", "locked", "unavailable-access", "missing-user", "invalid-user", "invalid-platform", "missing-group", "self", "mock", "wrong-text", "media"} {
		t.Run(reason, func(t *testing.T) {
			provider := &mockLLMProvider{}
			engine := newTestEngine(provider)
			engine.Scope = newAdminTestScope(t, map[string]string{"ENABLE_PROACTIVE_REPLY": "true", "GROUP_REPLY_PROBABILITY": "1"})
			defer engine.Cancel()
			event := runtimeStatusEvent()
			switch reason {
			case "disabled":
				if err := engine.Scope.Config.Update(runtimeinfo.EnabledEnv, "false", false); err != nil {
					t.Fatal(err)
				}
			case "locked":
				engine.Security = security.NewController(t.TempDir())
				p, _ := security.NewPrincipal(event.Platform, event.UserID)
				if err := engine.Security.Lock(p, "synthetic test lock"); err != nil {
					t.Fatal(err)
				}
			case "unavailable-access":
				engine.Security = &security.Controller{}
			case "missing-user":
				event.UserID = ""
			case "invalid-user":
				event.UserID = strings.Repeat("x", 257)
			case "invalid-platform":
				event.Platform = strings.Repeat("x", 65)
			case "missing-group":
				event.GroupID = " "
			case "self":
				event.UserID = event.SelfID
			case "wrong-text":
				event.Content = "please #fa"
			case "media":
				event.Attachments = []core.Attachment{{Type: core.AttachmentTypeImage}}
			}
			dispatcher := &pokeTestDispatcher{}
			adapter := NewAdapter(engine)
			adapter.SetIncomingDispatcher(dispatcher)
			server, url, done := startPokeAdapterServer(adapter)
			defer server.Close()
			if reason == "mock" {
				url += "?mock=true"
			}
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			var action Action
			if err := conn.ReadJSON(&action); err != nil {
				t.Fatal(err)
			}
			if action.Action != "noop" || !action.SuppressLLM || action.Content != "" || action.Echo != "reply_"+event.MessageID {
				t.Fatalf("missing suppression: %+v", action)
			}
			closePokeConnection(t, conn, done)
			if engine.SessionManager.Count() != 0 || dispatcher.calls.Load() != 0 {
				t.Fatal("rejected command entered conversation pipeline")
			}
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.requests) != 0 {
				t.Fatal("rejected command reached LLM")
			}
		})
	}
}

func TestRuntimeStatusStoppedInstanceIsConsumed(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{})
	engine.Scope = newAdminTestScope(t, nil)
	engine.Cancel()
	conn, actions, cleanup := setupTestAstrBotWS(t)
	defer cleanup()
	if !handleRuntimeStatusCommand(conn, runtimeStatusEvent(), engine) {
		t.Fatal("stopped command escaped")
	}
	select {
	case action := <-actions:
		if action.Action != "noop" || !action.SuppressLLM {
			t.Fatalf("stopped instance responded: %+v", action)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no terminal noop")
	}
	if engine.TotalMessagesProcessed.Load() != 0 || engine.SessionManager.Count() != 0 {
		t.Fatal("stopped instance processed command")
	}
}
