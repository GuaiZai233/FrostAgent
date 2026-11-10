package llm

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func TestDialogueErrorsExposeOnlyTypeAndLookupUUID(t *testing.T) {
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{&url.Error{Op: "Post", URL: "https://endpoint.invalid/secret", Err: errors.New("synthetic-secret")}, "网络请求失败"},
		{&openai.HTTPError{StatusCode: 401, Body: "https://endpoint.invalid/secret synthetic-secret"}, "上游 HTTP 401"},
		{&openai.HTTPError{StatusCode: 400, Body: "https://endpoint.invalid/secret synthetic-secret"}, "上游 HTTP 400"},
		{context.DeadlineExceeded, "请求超时"},
		{context.Canceled, "请求已取消"},
		{errors.New("synthetic-secret"), "模型调用错误"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			store := logs.New("test", "test", 50)
			engine := &Engine{Scope: runtimescope.New(nil, nil, store), MaxIterations: 1, Provider: &failedDialogueProvider{err: tc.err}}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{SessionID: "synthetic-session"})
			assertReferencedError(t, store, result.Content, "LLM 响应失败："+tc.kind)
			if !result.LLMFailed || !errors.Is(result.Error, tc.err) || !strings.Contains(result.Content, tc.kind) || strings.Contains(result.Content, "synthetic-secret") || strings.Contains(result.Content, "endpoint.invalid") {
				t.Fatalf("unsafe error reply: %+v", result)
			}
			_, suffix, ok := strings.Cut(result.Content, " (Ref_ID: ")
			id := strings.TrimSuffix(suffix, ")")
			entry, found := store.FindError(id)
			if !ok || !found || !strings.Contains(entry.Content, tc.err.Error()) || entry.TraceID != "synthetic-session" {
				t.Fatalf("diagnostics missing: %+v", entry)
			}
		})
	}
}

func assertReferencedError(t *testing.T, store *logs.Store, reply, summary string) logs.LogEntry {
	t.Helper()
	prefix := "[FrostAgent 错误] " + summary + " (Ref_ID: "
	if !strings.HasPrefix(reply, prefix) || !strings.HasSuffix(reply, ")") || strings.ContainsAny(reply, "\r\n") {
		t.Fatalf("unexpected error format: %q", reply)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(reply, prefix), ")")
	if _, valid := logs.NormalizeErrorUUID(id); !valid {
		t.Fatalf("invalid error reference: %q", reply)
	}
	entry, found := store.FindError(id)
	if !found || !strings.HasPrefix(entry.Content, summary) {
		t.Fatalf("missing error diagnostics: %q, %+v", reply, entry)
	}
	return entry
}

func TestToolErrorsRetainDiagnosticsInOwningStore(t *testing.T) {
	const raw = "Post https://endpoint.invalid/private: synthetic-secret"
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			store := logs.New("tool-instance", "test", 100)
			tool := &structuredResultTool{staticTool: staticTool{name: "synthetic_tool"}, err: errors.New(raw)}
			registry := map[string]ToolExecutor{}
			if !missing {
				registry[tool.Name()] = tool
			}
			engine := &Engine{Scope: runtimescope.New(nil, nil, store), ToolRegistry: registry, MaxIterations: 2}
			calls := 0
			engine.Provider = &mockMultiStepProvider{chatFunc: func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				calls++
				if calls == 1 {
					return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "call", Type: "function", Function: core.ToolCallFunction{Name: tool.Name(), Arguments: "{}"}}}}}, nil
				}
				for _, message := range req.Messages {
					if message.Role == core.RoleTool {
						return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: message.Content}}, nil
					}
				}
				return nil, errors.New("missing tool result")
			}}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "run"}}, RunContext{SessionID: "tool-session"})
			summary := "工具执行失败"
			if missing {
				summary = "工具调用失败：工具未找到"
			}
			entry := assertReferencedError(t, store, result.Content, summary)
			if strings.Contains(result.Content, "endpoint.invalid") || strings.Contains(result.Content, "synthetic-secret") || entry.TraceID != "tool-session" {
				t.Fatalf("unsafe or misplaced tool diagnostics: %+v", result)
			}
			if !missing && !strings.Contains(entry.Content, raw) {
				t.Fatalf("lost raw tool error: %+v", entry)
			}
			if _, found := logs.General.FindError(entry.ErrorUUID); found {
				t.Fatal("tool error crossed instance stores")
			}
		})
	}
}

func TestDeliveryErrorRetainsDiagnosticsInOwningStore(t *testing.T) {
	store := logs.New("delivery-instance", "test", 10)
	engine := &Engine{Scope: runtimescope.New(nil, nil, store)}
	raw := errors.New("Post https://endpoint.invalid/private: synthetic-secret")
	ctx := WithRunContext(engine.Context(), RunContext{SessionID: "delivery-session", SendHook: func(string) error { return raw }})
	reply, delivered := engine.deliverToolResult(ctx, core.ToolExecutionResult{MessagePayload: "payload"}, "prepared")
	entry := assertReferencedError(t, store, reply, "消息发送失败")
	if delivered || !strings.Contains(entry.Content, raw.Error()) || strings.Contains(reply, "endpoint.invalid") {
		t.Fatalf("unsafe delivery failure: %s", reply)
	}
}

func TestVisionFailuresKeepLegacyFallbackAndInteractiveDiagnostics(t *testing.T) {
	store := logs.New("vision-instance", "test", 10)
	scope := runtimescope.New(nil, nil, store)
	raw := errors.New("Post https://endpoint.invalid/private: synthetic-secret")
	provider := &failedDialogueProvider{err: raw}
	if got := CallVisionModel(scope.Context(), provider, core.RouteContext{}, "[]"); got != "" {
		t.Fatalf("legacy vision fallback changed: %q", got)
	}
	_, err := CallVisionModelWithError(scope.Context(), provider, core.RouteContext{}, "[]")
	if !errors.Is(err, raw) {
		t.Fatalf("interactive caller lost vision diagnostics: %v", err)
	}
	providerOK := &staticProvider{response: &core.ChatResponse{Message: core.ChatMessage{Content: "synthetic description"}}}
	if got := CallVisionModel(scope.Context(), providerOK, core.RouteContext{}, "[]"); got != "synthetic description" {
		t.Fatalf("successful vision description changed: %q", got)
	}
}

func TestStickerPairFailuresNeverDeliverAndKeepReferences(t *testing.T) {
	const raw = "Post https://endpoint.invalid/private: synthetic-secret"
	for _, tc := range []struct {
		name, summary, diagnostic string
		stage                     security.WatchdogStage
		policy                    bool
	}{
		{name: "missing", summary: "工具调用失败：工具未找到", diagnostic: "send_message"},
		{name: "execution", summary: "工具执行失败", diagnostic: raw},
		{name: "oversize", summary: "工具执行失败：工具结果过大，配对消息未发送"},
		{name: "invalid pair", summary: "配对消息发送失败：消息无效", diagnostic: "文字消息无效"},
		{name: "argument failure", summary: "安全审查失败：服务暂时不可用，该工具调用已被阻止", stage: security.StageToolArgument, diagnostic: "eval_id="},
		{name: "argument block", summary: "安全审查拦截：该工具调用已被阻止", stage: security.StageToolArgument, policy: true, diagnostic: "eval_id="},
		{name: "result failure", summary: "安全审查失败：服务暂时不可用，外部工具结果已隔离", stage: security.StageToolResult, diagnostic: "eval_id="},
		{name: "result block", summary: "安全审查拦截：外部工具结果已隔离", stage: security.StageToolResult, policy: true, diagnostic: "eval_id="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := logs.New("pair-instance", "test", 30)
			engine := &Engine{Scope: runtimescope.New(nil, nil, store), ToolRegistry: map[string]ToolExecutor{}}
			output := core.ToolExecutionResult{Content: "prepared", MessagePayload: `{"messages":[{"type":"plain","text":"synthetic"}]}`}
			textTool := &structuredResultTool{staticTool: staticTool{name: "send_message"}, output: output}
			stickerTool := &structuredResultTool{staticTool: staticTool{name: "send_sticker"}, output: output}
			switch tc.name {
			case "execution":
				textTool.err = errors.New(raw)
			case "oversize":
				textTool.output.Content = strings.Repeat("x", MaxToolOutputBytes+1)
			case "invalid pair":
				textTool.output.MessagePayload = "invalid"
			}
			if tc.name != "missing" {
				engine.ToolRegistry[textTool.Name()] = textTool
			}
			engine.ToolRegistry[stickerTool.Name()] = stickerTool
			if tc.stage != "" {
				engine.Security = security.NewController(t.TempDir())
				engine.Security.SetMode(security.ControlModeAggressive)
				engine.Security.Watchdog.SetClassifier(&mockGateClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if input.Stage != tc.stage {
						return security.ClassificationResult{RiskLevel: security.RiskLevelNone}, nil
					}
					if !tc.policy {
						return security.ClassificationResult{}, errors.New(raw)
					}
					return security.ClassificationResult{Category: security.RiskCategoryMaliciousExecution, RiskLevel: security.RiskLevelCritical}, nil
				}})
			}
			deliveries := 0
			run := RunContext{SessionID: "pair-session", ActorPlatform: "test", ActorUserID: "synthetic-actor", SendHook: func(string) error { deliveries++; return nil }}
			call := func(name string) ToolCall { return ToolCall{Function: ToolCallFunction{Name: name, Arguments: "{}"}} }
			reply, _ := engine.deliverStickerPair(WithRunContext(engine.Context(), run), &run, call("send_message"), call("send_sticker"))
			entry := assertReferencedError(t, store, reply, tc.summary)
			if deliveries != 0 || strings.Contains(reply, "endpoint.invalid") || !strings.Contains(entry.Content, tc.diagnostic) {
				t.Fatalf("unsafe pair failure: delivered=%d reply=%q diagnostic=%+v", deliveries, reply, entry)
			}
		})
	}
}
