package llm

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"strings"
	"sync"
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
		e.RecordBotTurn()
	}
	if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 1 || grace != 0 {
		t.Fatalf("before quota: remaining=%d grace=%d", remaining, grace)
	}
	if !strings.Contains(e.stickerActivityPrompt(), "还剩 1 个 Bot 对话轮次") {
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
		e.RecordBotTurn()
	}
	input := []ChatMessage{{Role: "user", Content: "hello"}}
	result := e.RunMessagesWithContext(input, RunContext{})
	if result.Content != "after forgiveness" || len(p.requests) != 4 {
		t.Fatalf("result=%q calls=%d", result.Content, len(p.requests))
	}
	if input[0].Content != "hello" {
		t.Fatalf("caller history was changed: %v", input[0].Content)
	}
	sawCountdown := false
	for _, msg := range p.requests[0].Messages {
		if text, ok := msg.Content.(string); ok && strings.Contains(text, "还剩 0 个 Bot 对话轮次") {
			sawCountdown = true
		}
	}
	if !sawCountdown {
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
		e.RecordBotTurn()
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
		e.RecordBotTurn()
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
		e.RecordBotTurn()
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

func TestMultipleMessagesInOneBotTurnCountOnce(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "first", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "second", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
		}},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"hello","is_sticker":true}]}`},
	}}
	for range 3 {
		e.RecordBotTurn()
	}
	delivered := 0
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(string) error { delivered++; return nil },
	})
	if result.Content != "done" || delivered != 2 || len(p.requests) != 2 {
		t.Fatalf("result=%q delivered=%d calls=%d", result.Content, delivered, len(p.requests))
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 1 {
		t.Fatalf("one dialogue counted multiple sends or iterations: remaining=%d", remaining)
	}
}

func TestStickerQuotaAllowsOrderedTextAndStickerBatch(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "text", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`}},
		}},
		{Role: core.RoleAssistant},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"hello"}]}`},
		"send_sticker": &staticTool{name: "send_sticker", result: `{"messages":[{"type":"image","path":"/test/sticker.png","is_sticker":true}]}`},
	}}
	for range 5 {
		e.RecordBotTurn()
	}
	var delivered []string
	e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(payload string) error { delivered = append(delivered, payload); return nil },
	})
	if len(delivered) != 1 || strings.Index(delivered[0], `"text":"hello"`) < 0 || strings.Index(delivered[0], `"text":"hello"`) >= strings.Index(delivered[0], `"is_sticker":true`) {
		t.Fatalf("ordered batch dropped a message: %v", delivered)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 5 {
		t.Fatalf("paired sticker did not reset quota: remaining=%d", remaining)
	}
}

func TestStickerPairFailureDoesNotSendText(t *testing.T) {
	const textPayload = `{"messages":[{"type":"plain","text":"hello"}]}`
	const stickerPayload = `{"messages":[{"type":"image","url":"https://example.com/sticker.png","is_sticker":true}]}`
	for _, tc := range []struct {
		name, stickerResult string
		failHook            bool
		watchdogSticker     bool
	}{
		{name: "sticker not found", stickerResult: `{"error":"查无此词条"}`},
		{name: "sticker delivery failed", stickerResult: stickerPayload, failHook: true},
		{name: "watchdog blocked sticker", stickerResult: stickerPayload, watchdogSticker: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(sticker.ActivityEnvKey, "10")
			p := &stickerActivityProvider{responses: []core.ChatMessage{
				{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
					{ID: "text", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
					{ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`}},
				}},
				{Role: core.RoleAssistant, Content: ignoreEnforceStickerMarker},
				{Role: core.RoleAssistant, Content: "done"},
			}}
			e := &Engine{Provider: p, MaxIterations: 3, ToolRegistry: map[string]ToolExecutor{
				"send_message": &staticTool{name: "send_message", result: textPayload},
				"send_sticker": &staticTool{name: "send_sticker", result: tc.stickerResult},
			}}
			if tc.watchdogSticker {
				controller := security.NewController(t.TempDir())
				controller.SetMode(security.ControlModeAggressive)
				controller.Watchdog.SetClassifier(&mockGateClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if input.Stage == security.StageToolResult && strings.Contains(input.Content, "sticker.png") {
						return security.ClassificationResult{Category: security.RiskCategoryDataExfiltration, RiskLevel: security.RiskLevelHigh}, nil
					}
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				}})
				e.Security = controller
			}
			for range 5 {
				e.RecordBotTurn()
			}
			var sends int
			e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
				ActorPlatform: "test-platform", ActorUserID: "test_actor",
				SendHook: func(string) error {
					sends++
					if tc.failHook {
						return context.DeadlineExceeded
					}
					return nil
				},
			})
			wantSends := 0
			if tc.failHook {
				wantSends = 1
			}
			if sends != wantSends {
				t.Fatalf("unexpected send attempts: %d, want %d", sends, wantSends)
			}
			if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 0 {
				t.Fatalf("failed pair changed quota: remaining=%d", remaining)
			}
		})
	}
}

func TestOptionalStickerFailurePreservesTextBeforeQuota(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "text", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`}},
		}},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"hello"}]}`},
		"send_sticker": &staticTool{name: "send_sticker", result: `{"error":"查无此词条"}`},
	}}
	var delivered []string
	e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(payload string) error { delivered = append(delivered, payload); return nil },
	})
	if len(delivered) != 1 || !strings.Contains(delivered[0], `"text":"hello"`) {
		t.Fatalf("optional sticker failure dropped text: %v", delivered)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 4 {
		t.Fatalf("ordinary text was not counted: remaining=%d", remaining)
	}
}

func TestOnlyAdjacentTextSharesStickerDelivery(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
			{ID: "first", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "second", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}},
			{ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_sticker", Arguments: `{}`}},
		}},
		{Role: core.RoleAssistant},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"hello"}]}`},
		"send_sticker": &staticTool{name: "send_sticker", result: `{"messages":[{"type":"image","url":"https://example.com/sticker.png","is_sticker":true}]}`},
	}}
	for range 5 {
		e.RecordBotTurn()
	}
	var delivered []string
	e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(payload string) error { delivered = append(delivered, payload); return nil },
	})
	if len(delivered) != 1 || !strings.Contains(delivered[0], `"is_sticker":true`) {
		t.Fatalf("unpaired message bypassed quota: %v", delivered)
	}
	var firstBlocked bool
	for _, msg := range p.requests[1].Messages {
		if msg.Role == core.RoleTool && msg.ToolCallID == "first" && strings.Contains(msg.Content.(string), "额度已到期") {
			firstBlocked = true
		}
	}
	if !firstBlocked {
		t.Fatal("first ordinary message borrowed a nonadjacent sticker")
	}
}

func TestSendMessageStickerPayloadResetsQuota(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	const payload = `{"messages":[{"type":"image","path":"/test/sticker.png","is_sticker":true}]}`
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "sticker", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`},
		}}},
		{Role: core.RoleAssistant, Content: "done"},
	}}
	e := &Engine{Provider: p, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_message": &staticTool{name: "send_message", result: payload},
	}}
	for range 4 {
		e.RecordBotTurn()
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(got string) error {
			if got != payload {
				t.Fatalf("unexpected payload: %s", got)
			}
			return nil
		},
	})
	if result.Content != "done" {
		t.Fatalf("result=%q", result.Content)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 5 {
		t.Fatalf("send_message sticker did not reset quota: remaining=%d", remaining)
	}
}

func TestConcurrentStickerTurnsCountOnceEach(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "1")
	e := &Engine{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			e.RecordBotTurn()
			e.RecordBotMessage(false)
		})
	}
	wg.Wait()
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 30 {
		t.Fatalf("concurrent turns: remaining=%d, want 30", remaining)
	}
}

func TestStickerActivityCountModesAndReset(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	t.Setenv(sticker.ActivityModeEnvKey, "")
	e := &Engine{}
	for range 3 {
		e.RecordIncomingMessage()
	}
	e.RecordBotTurn()
	for range 3 {
		e.RecordBotMessage(false)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 4 {
		t.Fatalf("default mode counted passive or outbound messages: %d", remaining)
	}
	e.grantStickerGrace()
	t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityAllMessages)
	if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 5 || grace != 0 {
		t.Fatalf("mode switch did not reset state: remaining=%d grace=%d", remaining, grace)
	}
	for range 4 {
		e.RecordIncomingMessage()
	}
	e.RecordBotTurn()
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 1 {
		t.Fatalf("all messages counted a dialogue twice: %d", remaining)
	}
	if !strings.Contains(e.stickerActivityPrompt(), "1 条传入消息") {
		t.Fatal(e.stickerActivityPrompt())
	}
	e.RecordIncomingMessage()
	if !e.stickerNeededBeforeReply() {
		t.Fatal("fifth incoming message did not reach quota")
	}
	e.RecordBotMessage(true)
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 5 {
		t.Fatal(remaining)
	}
}

func TestProactiveSilentBotTurnCountsAndMockDoesNot(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: "stay_silent", Arguments: `{}`},
		}}},
	}}
	e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{Proactive: true})
	if !result.Silent {
		t.Fatal("expected proactive silence")
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 4 {
		t.Fatal(remaining)
	}
	e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "mock"}}, RunContext{Mock: true})
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 4 {
		t.Fatal("mock advanced counter", remaining)
	}
}

func TestBillingReceiptOnlyFinalUsesStickerQuota(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	p := &stickerActivityProvider{responses: []core.ChatMessage{
		{Role: core.RoleAssistant},
		{Role: core.RoleAssistant, Content: ignoreEnforceStickerMarker},
		{Role: core.RoleAssistant},
	}}
	e := &Engine{Provider: p, MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	for range 4 {
		e.RecordBotTurn()
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		AdapterID: "astrbot", Billing: &BillingRunState{BillingActive: true},
	})
	if len(p.requests) != 3 || result.Content != "" {
		t.Fatalf("receipt-only final skipped enforcement: calls=%d content=%q", len(p.requests), result.Content)
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
		e.RecordBotTurn()
	}
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{
		SendHook: func(string) error { return context.DeadlineExceeded },
	})
	if result.Content != "done" {
		t.Fatalf("result=%q", result.Content)
	}
	if _, remaining, _ := e.stickerActivitySnapshot(); remaining != 0 {
		t.Fatalf("failed delivery changed quota: remaining=%d", remaining)
	}
}
