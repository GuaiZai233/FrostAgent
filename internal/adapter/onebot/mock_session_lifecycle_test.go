package onebot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

func TestOneBotMockSessionContextLifetime(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			provider := &mockLLMProvider{}
			engine := newTestEngine(provider)
			srv, url := startWSTestServer(engine)
			defer srv.Close()
			dial := func() *websocket.Conn {
				t.Helper()
				conn, _, err := websocket.DefaultDialer.Dial(url+"?mock=true", nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				return conn
			}
			waitCount := func(want int) {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for engine.SessionManager.Count() != want && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if got := engine.SessionManager.Count(); got != want {
					t.Fatalf("sessions = %d, want %d", got, want)
				}
			}
			var sequence int32
			send := func(conn *websocket.Conn, text string) string {
				t.Helper()
				sequence++
				segments := []map[string]any{{"type": "text", "data": map[string]any{"text": text}}}
				if kind == "group" {
					segments = append([]map[string]any{{"type": "at", "data": map[string]any{"qq": "99"}}}, segments...)
				}
				message, _ := json.Marshal(segments)
				event := model.OneBotEvent{SelfID: 99, PostType: "message", MessageType: kind,
					UserID: 11, GroupID: 20, MessageID: sequence, Message: message}
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				for {
					var action model.OneBotAction
					if err := conn.ReadJSON(&action); err != nil {
						t.Fatal(err)
					}
					if err := conn.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo,
						"data": map[string]any{"message_id": sequence, "group_id": 20, "group_name": "Synthetic group"}}); err != nil {
						t.Fatal(err)
					}
					if action.Action == "get_group_info" {
						continue
					}
					if action.Action != "send_"+kind+"_msg" {
						t.Fatalf("unexpected action: %+v", action)
					}
					break
				}
				// Locate this connection's isolated session by its current input.
				var session *llm.SessionContext
				for _, candidate := range engine.SessionManager.ListSessions(0, 0) {
					if strings.Contains(fmt.Sprint(candidate.Snapshot()), text) {
						session = candidate
					}
				}
				if session == nil || !strings.HasPrefix(session.ID(), "mock:") {
					t.Fatal("missing isolated mock session")
				}
				barrier := session.ReserveSequenceTurn("sender:11", false, false)
				done := make(chan struct{})
				go func() { barrier.Wait(); barrier.Done(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("mock turn did not finish")
				}
				if _, ok := engine.SessionManager.Get(historyKey(event)); ok {
					t.Fatal("mock input created a production session")
				}
				return session.ID()
			}
			assertRequest := func(include, exclude string) {
				t.Helper()
				provider.mu.Lock()
				defer provider.mu.Unlock()
				request := fmt.Sprint(provider.requests[len(provider.requests)-1].Messages)
				if !strings.Contains(request, include) || (exclude != "" && strings.Contains(request, exclude)) {
					t.Fatalf("unexpected model context: %s", request)
				}
			}

			a := dial()
			aID := send(a, "fixture-a-first")
			if got := send(a, "fixture-a-followup"); got != aID {
				t.Fatal("open socket lost its context")
			}
			assertRequest("fixture-a-first", "")
			b := dial()
			bID := send(b, "fixture-b-first")
			assertRequest("fixture-b-first", "fixture-a-first")
			if aID == bID {
				t.Fatal("separate mock connections share context")
			}
			a.Close()
			waitCount(1)
			if _, ok := engine.SessionManager.Get(aID); ok {
				t.Fatal("closed connection retained its context")
			}
			send(b, "fixture-b-followup")
			assertRequest("fixture-b-first", "fixture-a-first")
			reconnected := dial()
			if id := send(reconnected, "fixture-reconnected"); id == aID {
				t.Fatal("reconnect reused the old mock generation")
			}
			assertRequest("fixture-reconnected", "fixture-a-first")
			b.Close()
			reconnected.Close()
			waitCount(0)
		})
	}
}
