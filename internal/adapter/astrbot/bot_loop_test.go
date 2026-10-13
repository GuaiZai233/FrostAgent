package astrbot

import (
	"FrostAgent/internal/adapter/botloop"
	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
	"context"
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

func TestAstrBotBotLoopFilteredHumanInterrupt(t *testing.T) {
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
	sendBot := func(id string, interrupted bool) {
		t.Helper()
		if err := conn.WriteJSON(Event{Type: "event", EventType: "message", Platform: "astrbot",
			MessageType: "group", GroupID: "room-a", UserID: "bot-a", MessageID: id,
			Content: id, IsWake: true, BotLoopInterrupted: interrupted}); err != nil {
			t.Fatal(err)
		}
	}
	sendBot("first", false)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" {
		t.Fatalf("first turn: %+v", action)
	}
	sendBot("blocked", false)
	if action := readBotLoopAction(t, conn); action.Action != "noop" || !action.SuppressLLM {
		t.Fatalf("second turn must be dropped: %+v", action)
	}
	sendBot("resumed", true)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" || action.Echo != "reply_resumed" {
		t.Fatalf("in-band filtered-message marker must reset the streak: %+v", action)
	}
}

func TestAstrBotBotLoopHardDropAlsoSuppressesSecurityRejection(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "astrbot:bot-a", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	engine.Security = security.NewController(t.TempDir())
	engine.Security.SetMode(security.ControlModeAggressive)
	engine.Security.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
		if input.Content == "blocked" {
			return security.ClassificationResult{Category: security.RiskCategoryPromptInjection,
				RiskLevel: security.RiskLevelCritical, Reason: "blocked"}, nil
		}
		return security.ClassificationResult{Category: security.RiskCategoryNone,
			RiskLevel: security.RiskLevelNone, Reason: "clean"}, nil
	}})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send := func(id, content string) Action {
		t.Helper()
		if err := conn.WriteJSON(Event{Type: "event", EventType: "message", Platform: "astrbot",
			MessageType: "private", UserID: "bot-a", MessageID: id, Content: content}); err != nil {
			t.Fatal(err)
		}
		return readBotLoopAction(t, conn)
	}
	if action := send("blocked-1", "blocked"); action.Action != "send_message" {
		t.Fatalf("first security rejection: %+v", action)
	}
	if action := send("blocked-2", "blocked"); action.Action != "noop" || !action.SuppressLLM {
		t.Fatalf("second rejection must be hard-dropped: %+v", action)
	}
	if action := send("clean", "clean"); action.Action != "noop" || !action.SuppressLLM {
		t.Fatalf("later request must remain hard-dropped: %+v", action)
	}
	provider.mu.Lock()
	count := len(provider.requests)
	provider.mu.Unlock()
	if count != 0 {
		t.Fatalf("hard-dropped turns must not call the model: got %d requests", count)
	}
}

func TestAstrBotBotLoopCrossConnectionSecurityDelayPreservesOrder(t *testing.T) {
	provider := &mockLLMProvider{}
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "astrbot:bot-a", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	entered := make(chan struct{})
	humanEntered := make(chan struct{})
	release := make(chan struct{})
	engine.Security = security.NewController(t.TempDir())
	engine.Security.SetMode(security.ControlModeAggressive)
	engine.Security.SetClassifier(&mockClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
		if input.Content == "slow" {
			close(entered)
			<-release
			return security.ClassificationResult{Category: security.RiskCategoryPromptInjection,
				RiskLevel: security.RiskLevelCritical, Reason: "blocked"}, nil
		}
		if input.Content == "human" {
			close(humanEntered)
		}
		return security.ClassificationResult{Category: security.RiskCategoryNone,
			RiskLevel: security.RiskLevelNone, Reason: "clean"}, nil
	}})
	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()
	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	send := func(conn *websocket.Conn, id, user, content string, wake bool) {
		t.Helper()
		if err := conn.WriteJSON(Event{Type: "event", EventType: "message", Platform: "astrbot",
			MessageType: "group", GroupID: "room-a", UserID: user, MessageID: id,
			Content: content, IsWake: wake}); err != nil {
			t.Fatal(err)
		}
	}
	send(first, "slow", "bot-a", "slow", true)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first connection did not enter security check")
	}
	send(second, "human", "human-a", "human", true)
	select {
	case <-humanEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("second connection did not enter security check before first was released")
	}
	send(second, "resumed", "bot-a", "resumed", true)
	close(release)
	if action := readBotLoopAction(t, first); action.Action != "send_message" {
		t.Fatalf("first security rejection: %+v", action)
	}
	if action := readBotLoopAction(t, second); action.Action != "send_message" || action.Echo != "reply_human" {
		t.Fatalf("human interruption: %+v", action)
	}
	if action := readBotLoopAction(t, second); action.Action != "send_message" || action.Echo != "reply_resumed" {
		t.Fatalf("new bot turn after human interruption: %+v", action)
	}
}
