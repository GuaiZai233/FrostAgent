package astrbot

import (
	"FrostAgent/internal/adapter/botloop"
	"FrostAgent/internal/core"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func readBotLoopAction(t *testing.T, conn *websocket.Conn) Action {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var action Action
	if err := json.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	return action
}

func TestAstrBotBotLoopHardDropAndHumanInterrupt(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "astrbot:bot-a", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send := func(id, user string, wake bool) {
		t.Helper()
		event := Event{Type: "event", EventType: "message", Platform: "astrbot", MessageType: "group",
			GroupID: "room-a", UserID: user, MessageID: id, Content: id, IsWake: wake}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
	}
	send("first", "bot-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" {
		t.Fatalf("first turn: %+v", action)
	}
	send("blocked", "bot-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "noop" || !action.SuppressLLM || action.Echo != "reply_blocked" {
		t.Fatalf("hard drop must suppress downstream LLM: %+v", action)
	}
	send("interrupt", "human-a", false)
	if action := readBotLoopAction(t, conn); action.Action != "noop" {
		t.Fatalf("unwoken human turn: %+v", action)
	}
	send("resumed", "bot-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" || action.Echo != "reply_resumed" {
		t.Fatalf("bot turn after interruption: %+v", action)
	}
	provider.mu.Lock()
	count := len(provider.requests)
	provider.mu.Unlock()
	if count != 2 {
		t.Fatalf("expected two model requests, got %d", count)
	}
}

func TestAstrBotBotLoopSoftPromptOnlyInRequest(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "bot-a", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "soft",
	})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, id := range []string{"first", "second"} {
		event := Event{Type: "event", EventType: "message", Platform: "astrbot", MessageType: "private",
			UserID: "bot-a", MessageID: id, Content: id}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
		if action := readBotLoopAction(t, conn); action.Action != "send_message" {
			t.Fatalf("turn %s: %+v", id, action)
		}
	}
	provider.mu.Lock()
	requests := append([]core.ChatRequest(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("expected two model requests, got %d", len(requests))
	}
	first, _ := requests[0].Messages[len(requests[0].Messages)-1].Content.(string)
	second, _ := requests[1].Messages[len(requests[1].Messages)-1].Content.(string)
	if strings.Contains(first, botloop.PromptPrefix) || !strings.HasPrefix(second, botloop.PromptPrefix) {
		t.Fatalf("soft prompt must start only the second request: first=%q second=%q", first, second)
	}
	history := engine.SessionManager.GetOrCreate("astrbot:private:bot-a").Snapshot()
	for _, message := range history {
		content, _ := message.Content.(string)
		if strings.Contains(content, botloop.PromptPrefix) {
			t.Fatal("soft prompt leaked into session history")
		}
	}
}
