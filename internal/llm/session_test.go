package llm

import (
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
