package astrbot

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/service/messages"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestScheduledMessageHTTPToAstrBotWebSocket(t *testing.T) {
	server, adapter, url := startWSTestServer(newTestEngine(&mockLLMProvider{}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		adapter.mu.RLock()
		ready := len(adapter.conns) > 0
		adapter.mu.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("connection not registered")
		}
		time.Sleep(time.Millisecond)
	}
	dispatcher := core.NewDefaultDispatcher()
	dispatcher.RegisterAdapter(adapter)
	service := messages.New(dispatcher, "fixture-instance", func(k string) string {
		if k == "FROSTAGENT_ACTIONSCAT_TOKEN" {
			return "fixture-proxy-token"
		}
		return ""
	})
	for _, messageType := range []string{"private", "group"} {
		body := fmt.Sprintf(`{"platform":"fixture-platform","message_type":%q,"target_id":"fixture-target","metadata":{"adapter_id":"astrbot"},"messages":[{"type":"plain","text":"scheduled result"}]}`, messageType)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer fixture-proxy-token")
		w := httptest.NewRecorder()
		service.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("send %d: %s", w.Code, w.Body.String())
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var action Action
		if err := conn.ReadJSON(&action); err != nil {
			t.Fatal(err)
		}
		if action.Platform != "fixture-platform" || action.MessageType != messageType || action.Action != "send_message" || len(action.Messages) != 1 || action.Messages[0].Text != "scheduled result" {
			t.Fatalf("lost route: %+v", action)
		}
		if messageType == "group" {
			if action.GroupID != "fixture-target" || action.UserID != "" {
				t.Fatalf("wrong group: %+v", action)
			}
		} else if action.UserID != "fixture-target" || action.GroupID != "" {
			t.Fatalf("wrong private: %+v", action)
		}
	}
}
