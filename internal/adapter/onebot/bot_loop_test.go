package onebot

import (
	"FrostAgent/internal/adapter/botloop"
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
	"FrostAgent/internal/tools"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func botLoopSilentProvider() *mockLLMProvider {
	return &mockLLMProvider{customChat: func(_ context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
		return &core.ChatResponse{Message: core.ChatMessage{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{ID: "silent", Type: "function", Function: core.ToolCallFunction{
				Name: llm.StaySilentToolName, Arguments: "{}",
			}}},
		}}, nil
	}}
}

func waitOneBotRequests(t *testing.T, provider *mockLLMProvider, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		got := len(provider.requests)
		provider.mu.Unlock()
		if got >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d model requests", count)
}

func TestOneBotBotLoopSoftPromptOnlyInRequest(t *testing.T) {
	provider := botLoopSilentProvider()
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "onebot:11", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "soft",
	})
	engine.ToolRegistry[llm.StaySilentToolName] = tools.StaySilentTool()
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for id := int32(1); id <= 2; id++ {
		message, _ := json.Marshal("hello")
		event := model.OneBotEvent{PostType: "message", MessageType: "private", UserID: 11, MessageID: id, Message: message}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
	}
	waitOneBotRequests(t, provider, 2)
	provider.mu.Lock()
	first := provider.requests[0]
	second := provider.requests[1]
	provider.mu.Unlock()
	firstText, _ := first.Messages[len(first.Messages)-1].Content.(string)
	secondText, _ := second.Messages[len(second.Messages)-1].Content.(string)
	if strings.Contains(firstText, botloop.PromptPrefix) || !strings.HasPrefix(secondText, botloop.PromptPrefix) {
		t.Fatalf("soft prompt must start only the over-limit request: first=%q second=%q", firstText, secondText)
	}
	history := engine.SessionManager.GetOrCreate("private:11").Snapshot()
	for _, message := range history {
		content, _ := message.Content.(string)
		if strings.Contains(content, botloop.PromptPrefix) {
			t.Fatal("soft prompt leaked into session history")
		}
	}
}

func TestOneBotBotLoopHardDropAndHumanInterrupt(t *testing.T) {
	provider := botLoopSilentProvider()
	engine := newTestEngine(provider)
	engine.Scope = newTestScopeWithEnv(t, map[string]string{
		"BOT_LOOP_USER_IDS": "onebot:11", "BOT_LOOP_MAX_TURNS": "1", "BOT_LOOP_MODE": "hard",
	})
	engine.ToolRegistry[llm.StaySilentToolName] = tools.StaySilentTool()
	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send := func(id int32, sender int64, wake bool, text string) {
		t.Helper()
		segments := []map[string]any{{"type": "text", "data": map[string]any{"text": text}}}
		if wake {
			segments = append([]map[string]any{{"type": "at", "data": map[string]any{"qq": "99"}}}, segments...)
		}
		raw, _ := json.Marshal(segments)
		event := model.OneBotEvent{SelfID: 99, PostType: "message", MessageType: "group", GroupID: 20, UserID: sender, MessageID: id, Message: raw}
		if err := conn.WriteJSON(event); err != nil {
			t.Fatal(err)
		}
	}
	send(1, 11, true, "first")
	waitOneBotRequests(t, provider, 1)
	send(2, 11, true, "blocked")
	send(3, 12, false, "interrupt")
	send(4, 11, true, "after interrupt")
	send(5, 12, true, "barrier")
	waitOneBotRequests(t, provider, 3)
	provider.mu.Lock()
	requests := append([]core.ChatRequest(nil), provider.requests...)
	provider.mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("hard mode allowed unexpected request: got %d, want 3", len(requests))
	}
	for _, request := range requests {
		last, _ := request.Messages[len(request.Messages)-1].Content.(string)
		if strings.Contains(last, "blocked") {
			t.Fatal("hard-dropped message reached the model")
		}
	}
}
