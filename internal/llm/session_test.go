package llm

import (
	"FrostAgent/internal/core"
	"fmt"
	"testing"
)

func TestSessionManagerSingleCanonicalEntry(t *testing.T) {
	sm := NewSessionManager()

	// 1. GetOrCreate via legacy AstrBot alias
	s1 := sm.GetOrCreate("aiocqhttp:group:10001")
	if s1 == nil {
		t.Fatalf("expected s1 to not be nil")
	}
	s1.AppendGroupCompactMessage("User (10002): hello", 10)

	// Invariant: Count must be 1
	if sm.Count() != 1 {
		t.Fatalf("expected Count() == 1, got %d", sm.Count())
	}

	// 2. GetOrCreate via canonical OneBot format
	s2 := sm.GetOrCreate("group:10001")
	if s2 != s1 {
		t.Fatalf("expected s2 to be identical pointer to s1, got s2=%p s1=%p", s2, s1)
	}

	// Invariant: Count must still be 1
	if sm.Count() != 1 {
		t.Fatalf("expected Count() == 1 after alias access, got %d", sm.Count())
	}

	// 3. GetOrCreate via qq alias
	s3 := sm.GetOrCreate("qq:group:10001")
	if s3 != s1 {
		t.Fatalf("expected s3 to be identical pointer to s1, got s3=%p s1=%p", s3, s1)
	}
	if sm.Count() != 1 {
		t.Fatalf("expected Count() == 1 after qq alias access, got %d", sm.Count())
	}

	// 4. GetOrCreate via onebot alias
	s4 := sm.GetOrCreate("onebot:group:10001")
	if s4 != s1 {
		t.Fatalf("expected s4 to be identical pointer to s1, got s4=%p s1=%p", s4, s1)
	}
	if sm.Count() != 1 {
		t.Fatalf("expected Count() == 1 after onebot alias access, got %d", sm.Count())
	}

	// 5. ListSessions must return exactly 1 session, no duplicates
	sessions := sm.ListSessions(0, 0)
	if len(sessions) != 1 {
		t.Fatalf("expected ListSessions to return exactly 1 session, got %d", len(sessions))
	}
	if sessions[0] != s1 {
		t.Fatalf("expected returned session to match s1")
	}

	// 6. Get via any alias must succeed
	for _, key := range []string{"aiocqhttp:group:10001", "group:10001", "qq:group:10001", "onebot:group:10001"} {
		got, ok := sm.Get(key)
		if !ok || got == nil {
			t.Errorf("Get(%q) failed: ok=%v got=%v", key, ok, got)
		}
	}

	// 7. Delete via alias must remove the single canonical entry without orphaned keys
	sm.Delete("qq:group:10001")
	if sm.Count() != 0 {
		t.Fatalf("expected Count() == 0 after Delete, got %d", sm.Count())
	}
	if len(sm.ListSessions(0, 0)) != 0 {
		t.Fatalf("expected 0 sessions in ListSessions after Delete, got %d", len(sm.ListSessions(0, 0)))
	}
	for _, key := range []string{"aiocqhttp:group:10001", "group:10001", "qq:group:10001", "onebot:group:10001"} {
		if _, ok := sm.Get(key); ok {
			t.Errorf("Get(%q) succeeded after Delete, expected not found", key)
		}
	}
}

func TestSessionManagerCrossAliasResetCompact(t *testing.T) {
	sm := NewSessionManager()

	sess := sm.GetOrCreate("aiocqhttp:group:20001")
	sess.SetGroupRunningSummary("initial summary")

	if sess.GroupRunningSummary() != "initial summary" {
		t.Fatalf("expected initial summary")
	}

	// Reset via canonical format
	if ok := sm.ResetGroupCompact("group:20001"); !ok {
		t.Fatalf("ResetGroupCompact via canonical format failed")
	}

	if sess.GroupRunningSummary() != "" {
		t.Fatalf("expected empty summary after ResetGroupCompact, got %q", sess.GroupRunningSummary())
	}
}

func TestSessionManagerPrivateSessionAliases(t *testing.T) {
	sm := NewSessionManager()

	s1 := sm.GetOrCreate("aiocqhttp:private:30001")
	if s1 == nil {
		t.Fatalf("s1 is nil")
	}

	s2 := sm.GetOrCreate("private:30001")
	if s2 != s1 {
		t.Fatalf("expected private alias resolution to match: %p vs %p", s1, s2)
	}

	if sm.Count() != 1 {
		t.Fatalf("expected Count() == 1, got %d", sm.Count())
	}

	sm.Delete("onebot:private:30001")
	if sm.Count() != 0 {
		t.Fatalf("expected Count() == 0 after Delete, got %d", sm.Count())
	}
}

func TestSessionManagerMultipleDistinctSessions(t *testing.T) {
	sm := NewSessionManager()

	for i := 1; i <= 5; i++ {
		sm.GetOrCreate(fmt.Sprintf("aiocqhttp:group:%d", 40000+i))
	}

	if sm.Count() != 5 {
		t.Fatalf("expected 5 distinct sessions, got %d", sm.Count())
	}

	list := sm.ListSessions(0, 0)
	if len(list) != 5 {
		t.Fatalf("expected ListSessions to return 5 sessions, got %d", len(list))
	}
}

func TestCommitPrivateCompact_PreservesNewMessagesUnderConcurrentTrim(t *testing.T) {
	s := &SessionContext{
		ConversationID: "private:test_user_seq",
	}

	// 1. Initial messages
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "msg 1"})
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "msg 2"})
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "msg 3"})
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "msg 4"})

	// 2. Snapshot
	snap, err := s.SnapshotPrivateCompact()
	if err != nil {
		t.Fatalf("SnapshotPrivateCompact failed: %v", err)
	}
	if snap.Count != 4 {
		t.Fatalf("expected snapshot count 4, got %d", snap.Count)
	}
	if snap.LastSeq == 0 {
		t.Fatalf("expected non-zero LastSeq in snapshot")
	}

	// 3. New messages arrive while LLM compaction is in flight
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "msg 5 new"})
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "msg 6 new"})

	// 4. Concurrent history trim occurs (e.g. MaxHistory constraint triggered)
	s.TrimHistory(3)

	// 5. Another message arrives after trim
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "msg 7 new"})

	// 6. Commit private compact
	summaryText := "前4条消息的要点总结"
	ok := s.CommitPrivateCompact(snap, summaryText)
	if !ok {
		t.Fatalf("CommitPrivateCompact failed")
	}

	// 7. Verify: summary is injected and all new messages (5, 6, 7) are preserved
	history := s.Snapshot()
	if len(history) != 5 { // 2 summary msgs (user+assistant) + 3 new msgs (5, 6, 7)
		t.Fatalf("expected 5 messages in history, got %d", len(history))
	}

	if history[0].Role != "user" || history[0].Content != "[先前对话总结] "+summaryText {
		t.Errorf("unexpected summary msg 0: %+v", history[0])
	}
	if history[1].Role != "assistant" {
		t.Errorf("unexpected summary msg 1: %+v", history[1])
	}
	if history[2].Content != "msg 5 new" {
		t.Errorf("expected msg 5 preserved, got: %+v", history[2])
	}
	if history[3].Content != "msg 6 new" {
		t.Errorf("expected msg 6 preserved, got: %+v", history[3])
	}
	if history[4].Content != "msg 7 new" {
		t.Errorf("expected msg 7 preserved, got: %+v", history[4])
	}

	// 8. Verify sequence slice monotonicity and length alignment
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.historySeq) != len(s.History) {
		t.Errorf("historySeq length %d != History length %d", len(s.historySeq), len(s.History))
	}
	for i := 1; i < len(s.historySeq); i++ {
		if s.historySeq[i] <= s.historySeq[i-1] {
			t.Errorf("historySeq not strictly monotonic at index %d: %d <= %d", i, s.historySeq[i], s.historySeq[i-1])
		}
	}
}

func TestCommitPrivateCompact_SessionResetRejection(t *testing.T) {
	s := &SessionContext{
		ConversationID: "private:test_user_reset",
	}
	s.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "hello"})
	s.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "hi"})

	snap, err := s.SnapshotPrivateCompact()
	if err != nil {
		t.Fatalf("SnapshotPrivateCompact failed: %v", err)
	}

	// Reset session during in-flight compaction
	if err := s.ResetSession(nil); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	// Commit must fail because epoch has advanced
	ok := s.CommitPrivateCompact(snap, "总结")
	if ok {
		t.Fatalf("expected CommitPrivateCompact to return false after session reset")
	}

	if len(s.Snapshot()) != 0 {
		t.Errorf("expected empty history after reset and rejected compact, got %d messages", len(s.Snapshot()))
	}
}
