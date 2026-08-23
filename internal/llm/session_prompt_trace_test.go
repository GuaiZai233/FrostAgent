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

func TestEngine_RunMessagesWithContext_SetsPromptTrace(t *testing.T) {
	sessionManager := NewSessionManager()
	sessionID := "mock_platform:group:10001"
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
		Owner: sessionID,
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
