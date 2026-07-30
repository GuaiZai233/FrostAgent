package llm

import (
	"FrostAgent/internal/core"
	"testing"
)

// TestBuildExtractionContext verifies that the recent-turn picker gives the
// background extractor enough context to detect implicit corrections to a
// previously saved memory (PR #65 review feedback #5), without dragging
// in the entire session history (the 9ad77fe guarantee).
func TestBuildExtractionContext(t *testing.T) {
	// Reproduce the structure of a session in which the user first
	// produced a claim, the agent acknowledged it (potentially via a
	// memory.write tool call), and the user then corrected themselves.
	toolCallText := ""
	// runLoop appends an assistant message with tool calls (Content == ""),
	// a tool result, and the final assistant text reply. The tool-call
	// assistant message has empty Content and must be skipped by the
	// extraction context picker.
	correctionScenario := []ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "我想玩世终黑盒"},
		{Role: "assistant", Content: toolCallText, ToolCalls: []ToolCall{{ID: "1", Type: "function", Function: ToolCallFunction{Name: "memory", Arguments: `{"action":"write","content":"用户对游戏《世终黑盒》感兴趣"}`}}}},
		{Role: "tool", Content: "记忆已写入"},
		{Role: "assistant", Content: "好的，已经记下来啦~"},
		{Role: "user", Content: "世终黑盒是World's End BLACKBOX"},
	}
	finalReply := "喵呜～对不起对不起！原来指的是 SEKAI NO OWARI 的那首《World's End BLACKBOX》呀！"

	got := buildExtractionContext(correctionScenario, finalReply)
	if len(got) == 0 {
		t.Fatalf("expected extraction context, got empty")
	}

	// Walk the resulting context and assert it is chronological, only
	// contains user/assistant text, and covers both the prior claim and
	// the correction. The final reply must anchor the tail of the slice.
	wantSubstrings := []string{
		"我想玩世终黑盒",
		"好的，已经记下来啦~",
		"World's End BLACKBOX",
		"对不起",
	}
	var joined string
	for i, m := range got {
		if m.Role != core.RoleUser && m.Role != core.RoleAssistant {
			t.Errorf("context[%d] has unexpected role %q", i, m.Role)
		}
		s, ok := m.Content.(string)
		if !ok || s == "" {
			t.Errorf("context[%d] has empty/non-string content", i)
		}
		joined += s + "\n"
	}
	for _, sub := range wantSubstrings {
		if !contains(joined, sub) {
			t.Errorf("extraction context missing %q\nfull context:\n%s", sub, joined)
		}
	}
	if got[len(got)-1].Role != core.RoleAssistant {
		t.Errorf("expected last message to be the agent's final reply, got role %q", got[len(got)-1].Role)
	}
	if s, _ := got[len(got)-1].Content.(string); s != finalReply {
		t.Errorf("expected last message content to be the final reply, got %q", s)
	}
}

func TestBuildExtractionContext_NoPriorTurn(t *testing.T) {
	// First turn of a brand-new session: only the system prompt and the
	// user's first message are present. The context should contain just
	// that user message and the final reply.
	msgs := []ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "你好"},
	}
	got := buildExtractionContext(msgs, "你好呀~")
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (user + final reply), got %d", len(got))
	}
	if got[0].Role != core.RoleUser || got[1].Role != core.RoleAssistant {
		t.Errorf("unexpected role order: %q -> %q", got[0].Role, got[1].Role)
	}
}

func TestBuildExtractionContext_EmptyFinalReply(t *testing.T) {
	// When the agent didn't produce a final text reply (e.g. the run
	// exited with an error), there is nothing meaningful for the
	// extractor to see and we skip the call entirely.
	msgs := []ChatMessage{
		{Role: "user", Content: "你好"},
	}
	if got := buildExtractionContext(msgs, ""); got != nil {
		t.Errorf("expected nil context when finalReply is empty, got %d messages", len(got))
	}
}

func TestBuildExtractionContext_SkipsToolCallAssistantMessages(t *testing.T) {
	// Assistant messages that only contain tool calls (no text) should
	// not be surfaced — they are procedural, not conversation narrative.
	// Expected window: prior user, prior text assistant, current user,
	// final reply (the tool-call assistant between them is dropped).
	msgs := []ChatMessage{
		{Role: "user", Content: "第一句"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "1"}}},
		{Role: "tool", Content: "ok"},
		{Role: "assistant", Content: "完成"},
		{Role: "user", Content: "第二句"},
	}
	got := buildExtractionContext(msgs, "回复二")
	if len(got) != 4 {
		t.Fatalf("expected 4 messages (prior user + prior text + current user + final reply), got %d", len(got))
	}
	for i, m := range got {
		if s, _ := m.Content.(string); s == "" {
			t.Errorf("context[%d] is empty (tool-call assistant leaked through)", i)
		}
	}
	// The chronological order must be prior user → prior assistant →
	// current user → current assistant (final reply).
	wantOrder := []string{"第一句", "完成", "第二句", "回复二"}
	for i, want := range wantOrder {
		if s, _ := got[i].Content.(string); s != want {
			t.Errorf("context[%d] = %q, want %q", i, s, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
