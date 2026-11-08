package onebot

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/model"
	"github.com/gorilla/websocket"
)

// A Direct Chat event that never reaches the LLM (unwoken group message) must
// still produce a session that is hidden from session visibility from the start.
func TestOneBotMockIntakeSessionHiddenWithoutLLM(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	srv, url := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(url+"?mock=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	segments, _ := json.Marshal([]map[string]any{{"type": "text", "data": map[string]any{"text": "unwoken"}}})
	event := model.OneBotEvent{SelfID: 99, PostType: "message", MessageType: "group",
		UserID: 11, GroupID: 20, MessageID: 1, Message: segments}
	if err := conn.WriteJSON(event); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for engine.SessionManager.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if engine.SessionManager.Count() == 0 {
		t.Fatal("mock intake did not create a session")
	}
	if got := engine.SessionManager.CountVisible(); got != 0 {
		t.Fatalf("CountVisible = %d, want 0 for mock-only intake", got)
	}
	if n := len(provider.requests); n != 0 {
		t.Fatalf("unwoken message reached the LLM %d times", n)
	}
}
