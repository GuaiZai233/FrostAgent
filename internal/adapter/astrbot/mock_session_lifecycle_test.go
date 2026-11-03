package astrbot

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/gorilla/websocket"
)

func TestAstrBotMockSessionContextLifetime(t *testing.T) {
	for _, kind := range []string{"private", "group"} {
		t.Run(kind, func(t *testing.T) {
			provider := &mockLLMProvider{}
			engine := newTestEngine(provider)
			srv, _, url := startWSTestServer(engine)
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
			send := func(conn *websocket.Conn, text string) string {
				t.Helper()
				event := Event{Type: "event", EventType: "message", Platform: "telegram", MessageType: kind,
					UserID: "fixture-user", GroupID: "fixture-group", GroupName: "Synthetic group",
					MessageID: text, Content: text, IsAt: true}
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				action := readBotLoopAction(t, conn)
				if action.Action != "send_message" || action.Echo != "reply_"+text {
					t.Fatalf("unexpected reply: %+v", action)
				}
				var session *llm.SessionContext
				for _, candidate := range engine.SessionManager.ListSessions(0, 0) {
					if strings.Contains(fmt.Sprint(candidate.Snapshot()), text) {
						session = candidate
					}
				}
				if session == nil || !strings.HasPrefix(session.ID(), "mock:") {
					t.Fatal("missing isolated mock session")
				}
				// The shared helper uses the production key; reserve on the actual
				// connection-scoped mock session instead.
				barrier := session.ReserveSequenceTurn("sender:telegram:fixture-user", false, false)
				done := make(chan struct{})
				go func() { barrier.Wait(); barrier.Done(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("mock turn did not finish")
				}
				if _, ok := engine.SessionManager.Get(sessionKey(event)); ok {
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
