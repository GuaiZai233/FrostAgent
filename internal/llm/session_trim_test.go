package llm

import "testing"

func TestTrimHistory(t *testing.T) {
	s := &SessionContext{
		History: []ChatMessage{
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
			{Role: "user", Content: "3"},
			{Role: "assistant", Content: "4"},
			{Role: "user", Content: "5"},
		},
	}

	// 超过上限时，从头部丢弃最旧的消息，保留最近 max 条。
	s.TrimHistory(4)
	if len(s.History) != 4 {
		t.Fatalf("expected 4 messages after trim, got %d", len(s.History))
	}
	if got := s.History[0].Content; got != "2" {
		t.Errorf("expected oldest message dropped, first = %v", got)
	}
	if got := s.History[3].Content; got != "5" {
		t.Errorf("expected newest message kept, last = %v", got)
	}

	// 未超过上限时保持不变。
	s.TrimHistory(10)
	if len(s.History) != 4 {
		t.Errorf("expected no-op when within limit, got %d messages", len(s.History))
	}
}

func TestEffectiveMaxHistory(t *testing.T) {
	fallback := 20

	t.Setenv("MAX_CONTEXT_MESSAGES", "30")
	if got := effectiveMaxHistory(fallback); got != 30 {
		t.Errorf("expected 30 for valid env, got %d", got)
	}

	// 低于下限的配置应回退默认。
	t.Setenv("MAX_CONTEXT_MESSAGES", "2")
	if got := effectiveMaxHistory(fallback); got != fallback {
		t.Errorf("expected fallback %d for below-min value, got %d", fallback, got)
	}

	// 非法数字回退。
	t.Setenv("MAX_CONTEXT_MESSAGES", "abc")
	if got := effectiveMaxHistory(fallback); got != fallback {
		t.Errorf("expected fallback %d for non-numeric value, got %d", fallback, got)
	}

	// 空值回退。
	t.Setenv("MAX_CONTEXT_MESSAGES", "")
	if got := effectiveMaxHistory(fallback); got != fallback {
		t.Errorf("expected fallback %d for empty value, got %d", fallback, got)
	}
}

func TestEngineTrimSession(t *testing.T) {
	t.Setenv("MAX_CONTEXT_MESSAGES", "")
	sm := NewSessionManager()
	sm.MaxHistory = 4

	session := &SessionContext{
		History: []ChatMessage{
			{Role: "user", Content: "1"},
			{Role: "assistant", Content: "2"},
			{Role: "user", Content: "3"},
			{Role: "assistant", Content: "4"},
			{Role: "user", Content: "5"},
		},
	}

	e := &Engine{SessionManager: sm}
	e.TrimSession(session)
	if len(session.History) != 4 {
		t.Fatalf("expected 4 messages after Engine.TrimSession, got %d", len(session.History))
	}

	// nil 会话不 panic。
	e.TrimSession(nil)
}
