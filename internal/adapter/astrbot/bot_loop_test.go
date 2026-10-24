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
			GroupID: "room-a", UserID: user, MessageID: id, Content: id, IsAt: wake}
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
	send("still-blocked", "bot-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "noop" || !action.SuppressLLM {
		t.Fatalf("ordinary human chatter must not restore Bot budgets: %+v", action)
	}
	send("explicit-wake", "human-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" {
		t.Fatalf("explicit human wake: %+v", action)
	}
	send("resumed", "bot-a", true)
	if action := readBotLoopAction(t, conn); action.Action != "send_message" || action.Echo != "reply_resumed" {
		t.Fatalf("bot turn after interruption: %+v", action)
	}
	provider.mu.Lock()
	count := len(provider.requests)
	provider.mu.Unlock()
	if count != 3 {
		t.Fatalf("expected three model requests, got %d", count)
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
		// These are separate soft-budget turns, not an injection while the
		// previous terminal write is still being finalized.
		waitAstrBotTurnCompletion(t, engine, event)
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

func TestAstrBotBotLoopIgnoresLegacyInterruption(t *testing.T) {
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
	sendBot("still-blocked", true)
	if action := readBotLoopAction(t, conn); action.Action != "noop" || !action.SuppressLLM {
		t.Fatalf("legacy filtered-message marker must not restore the budget: %+v", action)
	}
}

func TestAstrBotBotLoopIndependentBudgetsAndExplicitWake(t *testing.T) {
	for _, wake := range []string{"at", "alias", "poke"} {
		t.Run(wake, func(t *testing.T) {
			provider := &mockLLMProvider{}
			engine := newTestEngine(provider)
			engine.Scope = newTestScopeWithEnv(t, map[string]string{
				"BOT_LOOP_USER_IDS": "aiocqhttp:11,aiocqhttp:13", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
				"BOT_ALIASES": "synthetic-assistant", "ENABLE_POKE": "true", "POKE_COOLDOWN_SECONDS": "0",
				"ENABLE_PROACTIVE_REPLY": "true", "PROACTIVE_REPLY_PROBABILITY": "1",
			})
			srv, _, wsURL := startWSTestServer(engine)
			defer srv.Close()
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			send := func(id, sender, trigger, want string) {
				t.Helper()
				event := Event{Type: "event", EventType: "message", Platform: "aiocqhttp", MessageType: "group",
					SelfID: "99", GroupID: "20", UserID: sender, MessageID: id, Content: id, BotLoopInterrupted: true}
				switch trigger {
				case "at":
					event.IsAt = true
				case "alias":
					event.Content += " synthetic-assistant"
				case "quote":
					event.IsWake = true
				case "poke":
					event.EventType, event.NoticeType, event.SubType, event.TargetID = "notice", "notify", "poke", "99"
				}
				if err := conn.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				action := readBotLoopAction(t, conn)
				if action.Action != want || (want == "noop" && !action.SuppressLLM) {
					t.Fatalf("%s: got %+v, want %s", id, action, want)
				}
				if want == "send_message" && trigger != "" {
					waitAstrBotTurnCompletion(t, engine, event)
				}
			}
			send("first-a", "11", "at", "send_message")
			send("first-b", "13", "at", "send_message")
			send("proactive-human-chatter", "12", "", "send_message")
			send("quote-only-human-wake", "12", "quote", "send_message")
			send("blocked-a", "11", "at", "noop")
			send("blocked-b", "13", "at", "noop")
			send("human-wake", "12", wake, "send_message")
			send("resumed-a", "11", "at", "send_message")
			send("resumed-b", "13", "at", "send_message")
			send("blocked-again", "11", "at", "noop")
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if len(provider.requests) != 7 {
				t.Fatalf("model requests=%d, want 7", len(provider.requests))
			}
		})
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
			Content: content, IsAt: wake}); err != nil {
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
	// Budget admission remains ordered, but different senders execute
	// independently. Both replies must arrive exactly once in either order.
	replies := map[string]bool{"reply_human": false, "reply_resumed": false}
	for range 2 {
		action := readBotLoopAction(t, second)
		seen, expected := replies[action.Echo]
		if action.Action != "send_message" || !expected || seen {
			t.Fatalf("unexpected or duplicate reply after human budget reset: %+v", action)
		}
		replies[action.Echo] = true
	}
}
