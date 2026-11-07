package astrbot

import (
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/gorilla/websocket"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Only the second reply frame fails; the HTTP upgrade is left untouched.
type segmentFailureConn struct {
	net.Conn
	writes int
}

func (c *segmentFailureConn) Write(data []byte) (int, error) {
	if strings.Contains(string(data), `"action":"send_message"`) {
		c.writes++
		if c.writes == 2 {
			return 0, fmt.Errorf("synthetic second-segment write failure")
		}
	}
	return c.Conn.Write(data)
}

type segmentFailureListener struct {
	net.Listener
}

func (l *segmentFailureListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &segmentFailureConn{Conn: conn}, nil
}

func TestSegmentedReplyPartialWriteFailure(t *testing.T) {
	engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "first" + replyformat.SegmentMark + "second" + replyformat.SegmentMark + "third"}}}})
	engine.Scope = newTestScopeWithEnv(t, map[string]string{replyformat.EnvKey: "true", "STICKER_ACTIVITY": "0"})
	server := httptest.NewUnstartedServer(NewAdapter(engine).Handler())
	listener := &segmentFailureListener{Listener: server.Listener}
	server.Listener = listener
	server.Start()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); server.Close() })
	event := Event{Type: "event", EventType: "message", Platform: "synthetic", MessageType: "private", UserID: "synthetic-user", MessageID: "synthetic-message", Content: "hello"}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	if action.Content != "first" || !action.IsIntermediate {
		t.Fatalf("unexpected prefix: %+v", action)
	}
	waitAstrBotTurnCompletion(t, engine, event)
	session := engine.SessionManager.GetOrCreate(sessionKey(event))
	history := session.Snapshot()
	if len(history) != 2 || history[1].Content != "first" {
		t.Fatalf("unwritten suffix in history: %+v", history)
	}
	conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("unexpected suffix after write failure")
	}
	epoch := session.Epoch()
	if err := session.ResetSession(nil); err != nil {
		t.Fatal(err)
	}
	event.MessageType = "group"
	recordPartialReply(session, engine, epoch, event, "old delivered prefix")
	if len(session.Snapshot()) != 0 || len(session.SnapshotGroupContext(20, 4096, "").RecentStructuredMessages) != 0 {
		t.Fatal("old prefix restored after reset")
	}
}

func TestSegmentedReplyDelivery(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
			if err != nil {
				t.Fatal(err)
			}
			if err := config.Update(replyformat.EnvKey, fmt.Sprint(enabled), false); err != nil {
				t.Fatal(err)
			}
			if err := config.Update("STICKER_ACTIVITY", "0", false); err != nil {
				t.Fatal(err)
			}
			output := "first" + replyformat.SegmentMark + "second\n\n`" + replyformat.SegmentMark + "`"
			engine := newTestEngine(&mockLLMProvider{responses: []*core.ChatResponse{{Message: core.ChatMessage{Role: core.RoleAssistant, Content: output}}}})
			engine.Scope = runtimescope.New(config, nil, nil)
			srv, _, url := startWSTestServer(engine)
			conn, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				srv.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); srv.Close() })
			event := Event{Type: "event", EventType: "message", Platform: "astrbot", MessageType: "private", MessageID: "synthetic-msg", UserID: "synthetic-user", Content: "hello"}
			if err := conn.WriteJSON(event); err != nil {
				t.Fatal(err)
			}
			want := []string{"firstsecond\n\n`" + replyformat.SegmentMark + "`"}
			if enabled {
				want = []string{"first", "second", "`" + replyformat.SegmentMark + "`"}
			}
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for index, part := range want {
				var action Action
				if err := conn.ReadJSON(&action); err != nil {
					t.Fatal(err)
				}
				if action.Content != part || action.IsIntermediate != (index < len(want)-1) {
					t.Fatalf("part %d: %+v", index, action)
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				history := engine.SessionManager.GetOrCreate(sessionKey(event)).Snapshot()
				if len(history) == 2 && history[1].Content == strings.Join(want, "\n\n") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("history %+v", history)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
