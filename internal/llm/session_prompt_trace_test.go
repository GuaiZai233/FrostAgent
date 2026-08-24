package llm

import (
	"context"
	"strings"
	"testing"

	"FrostAgent/internal/core"
)

type mockTraceProvider struct {
	lastRequest core.ChatRequest
}

func (m *mockTraceProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.lastRequest = req
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "Mock reply",
		},
	}, nil
}

func TestSessionContext_PromptTrace(t *testing.T) {
	sess := &SessionContext{}

	// Initial state should return empty strings
	sys, model := sess.LastPromptTrace()
	if sys != "" || model != "" {
		t.Fatalf("expected empty initial prompt trace, got sys=%q, model=%q", sys, model)
	}

	// Set and verify trace
	expectedSys := "You are FrostAgent\n[Time: 2026-08-23 12:00:00]\nFew-shot examples..."
	expectedModel := "gpt-4o-mini"

	sess.SetLastPromptTrace(expectedSys, expectedModel)

	sys, model = sess.LastPromptTrace()
	if sys != expectedSys {
		t.Errorf("expected sys %q, got %q", expectedSys, sys)
	}
	if model != expectedModel {
		t.Errorf("expected model %q, got %q", expectedModel, model)
	}
}

func TestSessionContext_ClearResetsDerivedState(t *testing.T) {
	sess := &SessionContext{
		History: []ChatMessage{{Role: "user", Content: "hello"}},
	}
	sess.AppendGroupCompactMessage(GroupCompactMessage{
		Role:      "user",
		Content:   "group message",
		MessageID: "msg_1",
	}, 20)
	snapshot, ok := sess.SnapshotGroupCompact(1)
	if !ok {
		t.Fatal("expected group compact snapshot")
	}
	if !sess.CommitGroupCompact(snapshot, "summary") {
		t.Fatal("expected group compact commit")
	}
	sess.SetDeliveryFailure(DeliveryFailure{Platform: "onebot", Message: "failed"})
	sess.SetLastPromptTrace("system prompt", "model")

	sess.Clear()

	if len(sess.History) != 0 {
		t.Fatalf("expected history to be cleared, got %d entries", len(sess.History))
	}
	groupContext := sess.SnapshotGroupContext(20, 4096, "")
	if groupContext.RunningSummary != "" || len(groupContext.SummaryGroups) != 0 || len(groupContext.RecentMessages) != 0 {
		t.Fatalf("expected group context to be cleared, got %+v", groupContext)
	}
	if failure := sess.TakeDeliveryFailure(); failure != nil {
		t.Fatalf("expected delivery failure to be cleared, got %+v", failure)
	}
	systemPrompt, model := sess.LastPromptTrace()
	if systemPrompt != "" || model != "" {
		t.Fatalf("expected prompt trace to be cleared, got system=%q model=%q", systemPrompt, model)
	}
}

func TestEngine_RunMessagesWithContext_SetsPromptTrace(t *testing.T) {
	sessionManager := NewSessionManager()
	sessionID := "private:10001"
	sess := sessionManager.GetOrCreate(sessionID)

	provider := &mockTraceProvider{}
	engine := &Engine{
		Provider:       provider,
		SessionManager: sessionManager,
		ModelName:      "test-llm-model",
		DialoguePrompt: "Few-shot dialogue examples",
	}

	msgs := []ChatMessage{
		{Role: "user", Content: "Hello world"},
	}

	runCtx := RunContext{
		SessionID: sessionID,
		Owner:     "10001",
	}

	res := engine.RunMessagesWithContext(msgs, runCtx)
	if res.Error != nil {
		t.Fatalf("RunMessagesWithContext failed: %v", res.Error)
	}

	// Verify that LastPromptTrace was set on the session
	savedSys, savedModel := sess.LastPromptTrace()
	if savedModel != "test-llm-model" {
		t.Errorf("expected model 'test-llm-model', got %q", savedModel)
	}
	if !strings.Contains(savedSys, "Few-shot dialogue examples") {
		t.Errorf("expected saved system prompt to contain 'Few-shot dialogue examples', got %q", savedSys)
	}
	if !strings.Contains(savedSys, "当前系统时间：") {
		t.Errorf("expected saved system prompt to start with '当前系统时间：', got %q", savedSys)
	}
	if !strings.Contains(savedSys, "星期") {
		t.Errorf("expected saved system prompt to contain dynamic time, got %q", savedSys)
	}
}
