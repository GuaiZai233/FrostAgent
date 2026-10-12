package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/sticker"
	"context"
	"strings"
	"testing"
)

type stickerActivityProvider struct {
	responses []core.ChatMessage
	requests  []core.ChatRequest
}

func (p *stickerActivityProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.requests = append(p.requests, req)
	idx := len(p.requests) - 1
	if idx >= len(p.responses) {
		target := core.ChatMessage{Role: core.RoleAssistant, Content: "done"}
		return &core.ChatResponse{Message: target}, nil
	}
	return &core.ChatResponse{Message: p.responses[idx]}, nil
}

func TestStickerActivityIntervalsAndGrace(t *testing.T) {
	if !IsProactiveAllowedTool("send_sticker") || IsProactiveAllowedTool("send_message") {
		t.Fatal("proactive sticker exception changed the surrounding tool boundary")
	}
	for _, tc := range []struct{ level, interval int }{{0, 0}, {1, 50}, {9, 10}, {10, 5}} {
		if got := sticker.ActivityInterval(tc.level); got != tc.interval {
			t.Errorf("level %d: interval = %d, want %d", tc.level, got, tc.interval)
		}
	}
	for _, raw := range []string{"", "-1", "11", "1.5", "bad"} {
		if got := sticker.ActivityLevel(raw); got != 0 {
			t.Errorf("invalid level %q = %d, want disabled", raw, got)
		}
	}
	t.Setenv(sticker.ActivityEnvKey, "10")
	e := &Engine{}
	for range 4 {
		e.RecordBotMessage(false)
	}
	if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 1 || grace != 0 {
		t.Fatalf("before quota: remaining=%d grace=%d", remaining, grace)
	}
	if !strings.Contains(e.stickerActivityPrompt(), "还剩 1 轮回复") {
		t.Fatalf("unexpected prompt: %s", e.stickerActivityPrompt())
	}
	e.grantStickerGrace()
	e.RecordBotMessage(false)
	e.RecordBotMessage(false)
	if _, _, grace := e.stickerActivitySnapshot(); grace != 0 {
		t.Fatalf("grace = %d, want 0", grace)
	}
	e.RecordBotMessage(true)
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 5 {
		t.Fatalf("sticker did not reset quota: remaining=%d", remaining)
	}
}

func TestStickerEnforcementAutoForgivesAfterThreeAttempts(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, Content: "first"},
		{Role: core.RoleAssistant, Content: "second"},
		{Role: core.RoleAssistant, Content: "third"},
		{Role: core.RoleAssistant, Content: "after forgiveness"},
	}}
	e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	for range 4 {
		e.RecordBotMessage(false)
	}
	input := []ChatMessage{{Role: "user", Content: "hello"}}
	result := e.RunMessagesWithContext(input, RunContext{})
	if result.Content != "after forgiveness" || len(p.requests) != 4 {
		t.Fatalf("result=%q calls=%d", result.Content, len(p.requests))
	}
	if input[0].Content != "hello" {
		t.Fatalf("caller history was changed: %v", input[0].Content)
	}
	if !strings.Contains(p.requests[0].Messages[len(p.requests[0].Messages)-1].Content.(string), "还剩 1 轮回复") {
		t.Fatal("missing transient countdown")
	}
	last := p.requests[3]
	if len(last.Tools) != 0 {
		t.Fatal("internal forgiveness changed tool schemas")
	}
	var sawToolCall, sawReply bool
	for _, msg := range last.Messages {
		if len(msg.ToolCalls) > 0 && msg.ToolCalls[0].Function.Name == "ignore_enforce_sticker" {
			sawToolCall = true
		}
		if msg.Role == core.RoleTool && msg.Content == ignoreEnforceStickerReply {
			sawReply = true
		}
	}
	if !sawToolCall || !sawReply {
		t.Fatalf("missing forgiveness pair: call=%v reply=%v", sawToolCall, sawReply)
	}
	if _, _, grace := e.stickerActivitySnapshot(); grace != 2 {
		t.Fatalf("grace=%d, want 2", grace)
	}
}

func TestStickerEnforcementMarkerForgivesImmediately(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, Content: ignoreEnforceStickerMarker},
		{Role: core.RoleAssistant, Content: "ordinary reply"},
	}}
	e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	for range 4 {
		e.RecordBotMessage(false)
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
	if result.Content != "ordinary reply" || len(p.requests) != 2 {
		t.Fatalf("result=%q calls=%d", result.Content, len(p.requests))
	}
}

func TestStickerEnforcementConflictStillReachesForgiveness(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	conflict := core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
		{ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: "stay_silent", Arguments: `{}`}},
		{ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`}},
	}}
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		conflict, conflict, conflict,
		{Role: core.RoleAssistant, Content: "after forgiveness"},
	}}
	e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	for range 5 {
		e.RecordBotMessage(false)
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
	if result.Content != "after forgiveness" || len(p.requests) != 4 {
		t.Fatalf("result=%q calls=%d", result.Content, len(p.requests))
	}
	if _, _, grace := e.stickerActivitySnapshot(); grace != 2 {
		t.Fatalf("grace=%d, want 2", grace)
	}
}

func TestProactiveStickerDeliverySatisfiesQuota(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	const payload = `{"messages":[{"type":"image","path":"/test/sticker.png","is_sticker":true}]}`
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "sticker_call", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{"query":"开心"}`},
		}}},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_sticker": &staticTool{name: "send_sticker", result: payload},
	}}
	for range 4 {
		e.RecordBotMessage(false)
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		Proactive: true,
		SendHook: func(got string) error {
			if got != payload {
				t.Fatalf("payload = %q", got)
			}
			return nil
		},
	})
	if result.Content != "done" || len(p.requests) != 2 {
		t.Fatalf("result=%q calls=%d", result.Content, len(p.requests))
	}
	if len(p.requests[0].Tools) != 1 || p.requests[0].Tools[0].Name != "send_sticker" {
		t.Fatalf("proactive sticker tool unavailable: %+v", p.requests[0].Tools)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 5 {
		t.Fatalf("remaining=%d after confirmed sticker", remaining)
	}
}

func TestStickerQuotaBlocksSecondMessageInSameToolBatch(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	const payload = `{"messages":[{"type":"plain","text":"hello","is_sticker":true}]}`
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "first", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "second", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
		}},
		{Role: core.RoleAssistant, Content: ignoreEnforceStickerMarker},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 3, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: payload},
	}}
	for range 4 {
		e.RecordBotMessage(false)
	}
	delivered := 0
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(string) error { delivered++; return nil },
	})
	if result.Content != "done" || delivered != 1 {
		t.Fatalf("result=%q delivered=%d, want one delivery", result.Content, delivered)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 0 {
		t.Fatalf("plain message with is_sticker flag reset quota: remaining=%d", remaining)
	}
	var blocked bool
	for _, msg := range p.requests[1].Messages {
		if msg.Role == core.RoleTool && msg.ToolCallID == "second" && strings.Contains(msg.Content.(string), "额度已到期") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("second send_message was not blocked at quota")
	}
}

func TestFailedStickerDeliveryDoesNotResetQuota(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	const payload = `{"messages":[{"type":"image","path":"/test/sticker.png","is_sticker":true}]}`
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`},
		}}},
		{Role: core.RoleAssistant, Content: ignoreEnforceStickerMarker},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 3, ToolRegistry: map[string]ToolExecutor{
		"send_sticker": &staticTool{name: "send_sticker", result: payload},
	}}
	for range 4 {
		e.RecordBotMessage(false)
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(string) error { return context.DeadlineExceeded },
	})
	if result.Content != "done" {
		t.Fatalf("result=%q", result.Content)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 1 {
		t.Fatalf("failed delivery changed quota: remaining=%d", remaining)
	}
}
