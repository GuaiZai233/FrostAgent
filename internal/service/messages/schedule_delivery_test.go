package messages_test

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/service/messages"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceExplicitAdapterPreservesUpstreamPlatform(t *testing.T) {
	for _, messageType := range []string{"private", "group"} {
		t.Run(messageType, func(t *testing.T) {
			dispatcher := core.NewDefaultDispatcher()
			onebot, astrbot := &mockAdapter{id: "onebot"}, &mockAdapter{id: "astrbot"}
			dispatcher.RegisterAdapter(onebot)
			dispatcher.RegisterAdapter(astrbot)
			service := messages.New(dispatcher, "fixture-instance", authedGetenv)
			body := fmt.Sprintf(`{"platform":"fixture-platform","message_type":%q,"target_id":"fixture-target","session":"fixture-session","metadata":{"adapter_id":"astrbot"},"messages":[{"type":"plain","text":"scheduled result"}]}`, messageType)
			w := httptest.NewRecorder()
			service.ServeHTTP(w, newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if len(onebot.messages) != 0 || len(astrbot.messages) != 1 {
				t.Fatalf("wrong adapter: onebot=%d astrbot=%d", len(onebot.messages), len(astrbot.messages))
			}
			msg := astrbot.messages[0]
			if msg.Platform != "fixture-platform" || msg.MessageType != messageType || msg.TargetID != "fixture-target" || msg.Content != "scheduled result" {
				t.Fatalf("wrong outbound routing: %+v", msg)
			}
		})
	}
}

func TestServiceRejectsInvalidExplicitAdapter(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "onebot"}
	dispatcher.RegisterAdapter(adapter)
	service := messages.New(dispatcher, "fixture-instance", authedGetenv)
	for _, value := range []string{`"missing-adapter"`, `42`, `null`, `""`} {
		body := fmt.Sprintf(`{"platform":"onebot","message_type":"private","target_id":"fixture-user","content":"scheduled result","metadata":{"adapter_id":%s}}`, value)
		w := httptest.NewRecorder()
		service.ServeHTTP(w, newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid adapter %s: %d", value, w.Code)
		}
	}
	if len(adapter.messages) != 0 {
		t.Fatal("invalid adapter delivered a message")
	}
}
