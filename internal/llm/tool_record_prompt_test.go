package llm

import (
	"context"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// capturingProvider records the last request and ends the turn quietly.
type capturingProvider struct{ last core.ChatRequest }

func (p *capturingProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.last = req
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
		ID: "silent", Type: "function", Function: core.ToolCallFunction{Name: StaySilentToolName, Arguments: "{}"},
	}}}}, nil
}

// After trimming, a tool-execution record can be the first message. It must not
// count as the assembled system prompt, or the persona and system instructions
// are silently dropped from the request.
func TestTrimmedToolRecordFirstStillAssemblesSystemPrompt(t *testing.T) {
	t.Setenv("SYSTEM_PROMPT", "固定系统设定")
	provider := &capturingProvider{}
	engine := &Engine{Provider: provider, DialoguePrompt: "固定人设示例", MaxIterations: 1, ToolRegistry: map[string]ToolExecutor{}}
	messages := []ChatMessage{
		{Role: "system", Content: "【工具执行记录】\n- 工具调用: dummy (ref_id: fa_toolcall_x)", ToolHistoryRunID: "run-1"},
		{Role: "user", Content: "你好"},
	}
	engine.RunMessagesWithContext(messages, RunContext{})
	if len(provider.last.Messages) == 0 {
		t.Fatal("provider received no messages")
	}
	first := string(provider.last.Messages[0].Role) + "|" + anyText(provider.last.Messages[0].Content)
	if !strings.Contains(first, "固定系统设定") || !strings.Contains(first, "固定人设示例") {
		t.Fatalf("system prompt missing from request after trim: %q", first)
	}
}

func anyText(v any) string {
	s, _ := v.(string)
	return s
}

// The session keeps tool notes as system; the wire layer decides the final role
// per endpoint, so the conversion must carry the marker and only for these notes.
func TestConvertToCoreMessagesMarksToolRecords(t *testing.T) {
	out := convertToCoreMessages([]ChatMessage{
		{Role: "system", Content: "record", ToolHistoryRunID: "run-1"},
		{Role: "system", Content: "plain system"},
		{Role: "assistant", Content: "reply"},
	})
	if !out[0].ToolRecord || out[1].ToolRecord || out[2].ToolRecord {
		t.Fatalf("unexpected ToolRecord flags: %v %v %v", out[0].ToolRecord, out[1].ToolRecord, out[2].ToolRecord)
	}
}
