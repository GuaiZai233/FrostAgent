package onebot

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimeinfo"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/gorilla/websocket"
)

// Numeric identities are deliberately small synthetic transport fixtures.
func runtimeStatusEvent() model.OneBotEvent {
	return model.OneBotEvent{SelfID: 101, UserID: 202, GroupID: 303, MessageID: 1,
		PostType: "message", MessageType: "group", Message: json.RawMessage(`"#fa"`)}
}

func TestRuntimeStatusRecognizesOnlyCurrentCommand(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		want          bool
	}{
		{"plain", `"#fa"`, true},
		{"trimmed", `" \n#fa\t "`, true},
		{"split-text", `[{"type":"text","data":{"text":"#"}},{"type":"text","data":{"text":"fa"}}]`, true},
		{"self-at", `[{"type":"at","data":{"qq":"101"}},{"type":"text","data":{"text":" #fa"}}]`, true},
		{"reply-and-current", `[{"type":"reply","data":{"id":"synthetic-quote","text":"not the command"}},{"type":"text","data":{"text":"#fa"}}]`, true},
		{"quote-only", `[{"type":"reply","data":{"id":"synthetic-quote","text":"#fa"}}]`, false},
		{"other-at", `[{"type":"at","data":{"qq":"404"}},{"type":"text","data":{"text":"#fa"}}]`, false},
		{"media", `[{"type":"text","data":{"text":"#fa"}},{"type":"image","data":{"file":"synthetic"}}]`, false},
		{"invalid-text", `[{"type":"text","data":{"text":123}}]`, false},
		{"substring", `"please #fa"`, false},
		{"suffix", `"#false"`, false},
		{"uppercase", `"#FA"`, false},
		{"fake-at", `"@Bot #fa"`, false},
		{"cq-string", `"[CQ:reply,id=1]#fa"`, false},
		{"empty", `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := runtimeStatusEvent()
			event.Message = json.RawMessage(tc.message)
			event.Messages = json.RawMessage(`["#fa"]`)
			if got := isRuntimeStatusCommand(event); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	for _, change := range []func(*model.OneBotEvent){
		func(e *model.OneBotEvent) { e.MessageType = "private" },
		func(e *model.OneBotEvent) { e.PostType = "notice" },
		func(e *model.OneBotEvent) { e.PokeID = "synthetic-poke" },
	} {
		e := runtimeStatusEvent()
		change(&e)
		if isRuntimeStatusCommand(e) {
			t.Fatalf("non-message command accepted: %+v", e)
		}
	}
}

func TestWSRuntimeStatusBypassesConversationAndHotToggles(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newAdminTestScope(t, map[string]string{
		"GROUP_REPLY_ON_MENTION": "false", "ENABLE_PROACTIVE_REPLY": "true",
		"GROUP_REPLY_PROBABILITY": "1", "ADMIN_COMMAND_PREFIX": "#",
		"ENABLE_AT_IN_GROUP_MSG": "true", "ENABLE_REPLY_IN_GROUP_MSG": "true",
		"BOT_LOOP_USER_IDS": "202", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	defer engine.Cancel()
	engine.ModelRouter = modelrouter.New(filepath.Join(t.TempDir(), "router.json"))
	engine.Version = "a1b2c3d"
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
	read := func(id int32) {
		t.Helper()
		event := runtimeStatusEvent()
		event.MessageID = id
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var got model.OneBotAction
		if err := conn.ReadJSON(&got); err != nil {
			t.Fatal(err)
		}
		if got.Action != "send_group_msg" {
			t.Fatalf("unexpected action: %+v", got)
		}
		params := got.Params.(map[string]any)
		segments := params["message"].([]any)
		if len(segments) != 1 || params["group_id"] != float64(303) {
			t.Fatalf("decorated or misrouted reply: %+v", got)
		}
		segment := segments[0].(map[string]any)
		text := segment["data"].(map[string]any)["text"]
		if segment["type"] != "text" || text != runtimeinfo.Format(engine.Version, engine.StartedAt, time.Now()) {
			t.Fatalf("bad diagnostic: %+v", got)
		}
	}
	read(1)
	read(2) // Marked bot budgets do not apply to this independent diagnostic.
	if err := engine.Scope.Config.Update(runtimeinfo.EnabledEnv, "false", false); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(runtimeStatusEvent()); err != nil {
		t.Fatal(err)
	}
	closeRuntimeStatusQuietly(t, conn, done)
	if engine.SessionManager.Count() != 0 || dispatcher.calls.Load() != 0 || classifications.Load() != 0 {
		t.Fatal("diagnostic entered conversation pipeline")
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 0 {
		t.Fatal("diagnostic called a model")
	}
}

func TestWSRuntimeStatusRejectedIsConsumed(t *testing.T) {
	for _, reason := range []string{"disabled", "locked", "unavailable-access", "invalid-user", "invalid-group", "self", "mock"} {
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
				p, _ := security.NewPrincipal("qq", "202")
				if err := engine.Security.Lock(p, "synthetic test lock"); err != nil {
					t.Fatal(err)
				}
			case "unavailable-access":
				engine.Security = &security.Controller{}
			case "invalid-user":
				event.UserID = 0
			case "invalid-group":
				event.GroupID = 0
			case "self":
				event.UserID = event.SelfID
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
			// A close frame is an ordered barrier: no session/dispatch work may survive it.
			closeRuntimeStatusQuietly(t, conn, done)
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
	if !handleRuntimeStatusCommand(&wsConnection{}, runtimeStatusEvent(), engine) {
		t.Fatal("stopped command escaped")
	}
	if engine.TotalMessagesProcessed.Load() != 0 || engine.SessionManager.Count() != 0 {
		t.Fatal("stopped instance processed command")
	}
}

func closeRuntimeStatusQuietly(t *testing.T, conn *websocket.Conn, done <-chan struct{}) {
	t.Helper()
	if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, raw, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Fatalf("expected silent close, got %s, err=%v", raw, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not terminate")
	}
}
