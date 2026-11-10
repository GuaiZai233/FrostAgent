package astrbot

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPrivateResponseGuidanceTime(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	t.Cleanup(engine.Cancel)
	server, _, url := startWSTestServer(engine)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	event := Event{Type: "event", EventType: "message", MessageType: "private", MessageID: "synthetic-message", UserID: "synthetic-user", Content: "guidance probe"}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var action Action
	if err := conn.ReadJSON(&action); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 1 {
		t.Fatalf("unexpected requests: %d", len(provider.requests))
	}
	request := provider.requests[0]
	for _, message := range request.Messages {
		content, _ := message.Content.(string)
		if message.Role == "system" && strings.Contains(content, "当前系统时间：") {
			t.Fatalf("time remained in system instructions: %s", content)
		}
	}
	content, _ := request.Messages[len(request.Messages)-1].Content.(string)
	if !strings.Contains(content, `"guidance":"请结合用户消息`) || !strings.Contains(content, "必须最终调用 stay_silent 中止此轮对话") || strings.Count(content, "当前系统时间：") != 1 {
		t.Fatalf("time must appear once in user guidance: %s", content)
	}
}
