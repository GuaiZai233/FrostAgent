package onebot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/gorilla/websocket"
)

func TestAwarenessOneBotSegmentsFinalReply(t *testing.T) {
	output := "first\n\n" + replyformat.SegmentMark + "second\n\n" + replyformat.SegmentMark + "third"
	engine := segmentTestEngine(t, output, true)
	if err := engine.Scope.Config.Update(proactive.AwarenessEnabled, "true", false); err != nil {
		t.Fatal(err)
	}
	if err := engine.Scope.Config.Update(proactive.AwarenessQuietHoursEnabled, "false", false); err != nil {
		t.Fatal(err)
	}
	adapter := NewAdapter(engine)
	service := &llm.AwarenessService{Engine: engine, Resolve: func(llm.AwarenessTarget) (llm.AwarenessSender, bool) {
		return adapter.SendAwareness, adapter.AwarenessReady()
	}}
	server := httptest.NewServer(http.HandlerFunc(adapter.Handler()))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); engine.Scope.Cancel(); engine.Scope.Wait(); server.Close() })
	deadline := time.Now().Add(time.Second)
	for !adapter.AwarenessReady() {
		if time.Now().After(deadline) {
			t.Fatal("transport not ready")
		}
		time.Sleep(time.Millisecond)
	}
	// Synthetic protocol target, never a real group ID.
	target := llm.AwarenessTargetFor("qq", "group", "43")
	done := make(chan struct{})
	go func() { defer close(done); service.Trigger(engine.Context(), target) }()
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	for index, want := range []string{"first", "second", "third"} {
		var action model.OneBotAction
		if err := client.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Action != "send_group_msg" {
			t.Fatalf("unexpected route: %+v", action)
		}
		chain := action.Params.(map[string]any)["message"].([]any)
		if len(chain) != 1 {
			t.Fatalf("invented quote or mention: %+v", chain)
		}
		part := chain[0].(map[string]any)
		if part["type"] != "text" || part["data"].(map[string]any)["text"] != want {
			t.Fatalf("part %d=%+v want=%q", index, chain, want)
		}
		select {
		case <-done:
			t.Fatal("trigger finished before platform ACK")
		default:
		}
		if err := client.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action.Echo, "data": map[string]any{"message_id": 100 + index}}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("trigger did not finish")
	}
	waitSegmentHistory(t, engine, target.SessionID, []string{"first\nsecond\nthird"})
	client.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("unexpected extra segment")
	}
}
