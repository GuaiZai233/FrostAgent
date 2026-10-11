package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/security"
	"context"
	"errors"
	"strings"
	"testing"
)

type structuredResultTool struct {
	staticTool
	output core.ToolExecutionResult
	err    error
}

func (t *structuredResultTool) ExecuteResult(context.Context, string) (core.ToolExecutionResult, error) {
	return t.output, t.err
}

func runResultTool(t *testing.T, tool ToolExecutor, hook func(string) error, controllers ...*security.Controller) AgentRunResult {
	t.Helper()
	engine := &Engine{
		MaxIterations: 3,
		ToolRegistry:  map[string]ToolExecutor{tool.Name(): tool},
		Provider: &sequentialProvider{responses: []core.ChatResponse{{Message: core.ChatMessage{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{ID: "call_test", Type: "function", Function: core.ToolCallFunction{
				Name: tool.Name(), Arguments: `{"action":"write","content":"test memory"}`,
			}}},
		}}}},
	}
	if len(controllers) > 0 {
		engine.Security = controllers[0]
	}
	return engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "test"}}, RunContext{SendHook: hook, ActorPlatform: "test-platform", ActorUserID: "test_actor"})
}

func TestStructuredMemoryEffectDoesNotDependOnResultWording(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		written bool
		err     error
		want    bool
	}{
		{"changed wording", "persisted with different wording", true, nil, true},
		{"text alone", "记忆已写入", false, nil, false},
		{"failed execution", "failed", true, errors.New("test failure"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &structuredResultTool{staticTool: staticTool{name: "memory", result: "legacy method must not determine the result"}, output: core.ToolExecutionResult{Content: tc.content, MemoryWritten: tc.written}, err: tc.err}
			if result := runResultTool(t, tool, nil); result.MemoryWritten != tc.want {
				t.Fatalf("MemoryWritten = %t, want %t", result.MemoryWritten, tc.want)
			}
		})
	}
}

func TestStructuredMessageDeliveryUsesExplicitPayload(t *testing.T) {
	payload := `{"messages":[{"type":"plain","text":"test delivery"}]}`
	for _, tc := range []struct {
		name   string
		output core.ToolExecutionResult
		err    error
		want   int
	}{
		{"explicit payload", core.ToolExecutionResult{Content: "prepared", MessagePayload: payload}, nil, 1},
		{"JSON text is not a delivery", core.ToolExecutionResult{Content: payload}, nil, 0},
		{"failed execution", core.ToolExecutionResult{Content: "failed", MessagePayload: payload}, errors.New("test failure"), 0},
		{"oversize payload", core.ToolExecutionResult{Content: "oversize", MessagePayload: strings.Repeat("x", MaxToolOutputBytes+1)}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			tool := &structuredResultTool{staticTool: staticTool{name: "send_message"}, output: tc.output, err: tc.err}
			runResultTool(t, tool, func(got string) error {
				calls++
				if got != payload {
					t.Errorf("delivered content instead of structured payload: %q", got)
				}
				return nil
			})
			if calls != tc.want {
				t.Fatalf("delivery calls = %d, want %d", calls, tc.want)
			}
		})
	}
}

func TestLegacyMemoryAndMessageResultsRemainCompatible(t *testing.T) {
	if result := runResultTool(t, &staticTool{name: "memory", result: "记忆已写入"}, nil); !result.MemoryWritten {
		t.Fatal("legacy memory execution lost its compatibility path")
	}
	calls := 0
	runResultTool(t, &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"legacy"}]}`}, func(string) error { calls++; return nil })
	if calls != 1 {
		t.Fatal("legacy message execution lost its compatibility path")
	}
}

func TestStructuredPayloadIsInspectedAndFilteredBeforeDelivery(t *testing.T) {
	controller := security.NewController(t.TempDir())
	controller.SetMode(security.ControlModeAggressive)
	inspected := false
	controller.Watchdog.SetClassifier(&mockGateClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
		if input.Stage == security.StageToolResult && strings.Contains(input.Content, "test-payload-marker") {
			inspected = true
			return security.ClassificationResult{Category: security.RiskCategoryDataExfiltration, RiskLevel: security.RiskLevelHigh}, nil
		}
		return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
	}})
	tool := &structuredResultTool{staticTool: staticTool{name: "send_message"}, output: core.ToolExecutionResult{
		Content:        "prepared",
		MessagePayload: `{"messages":[{"type":"plain","text":"test-payload-marker"}]}`,
	}}
	calls := 0
	runResultTool(t, tool, func(string) error { calls++; return nil }, controller)
	if !inspected || calls != 0 {
		t.Fatalf("payload security gate was bypassed: inspected=%t, deliveries=%d", inspected, calls)
	}
}
