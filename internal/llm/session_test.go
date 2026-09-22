package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/runtimescope"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

type mockBlockingExtractProvider struct {
	onChat func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error)
}

func (m *mockBlockingExtractProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	if m.onChat != nil {
		return m.onChat(ctx, req)
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "[]",
		},
	}, nil
}

func TestExtractionLifecycle_ContextCancellationDirect(t *testing.T) {
	sess := &SessionContext{}
	ctx, epoch, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	if epoch != 1 {
		t.Fatalf("expected initial epoch 1, got %d", epoch)
	}
	if ctx.Err() != nil {
		t.Fatalf("expected context not cancelled initially")
	}

	if err := sess.ResetSession(nil); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	if ctx.Err() == nil {
		t.Fatalf("expected extraction context to be cancelled after ResetSession")
	}
	if sess.Epoch() <= epoch {
		t.Fatalf("expected epoch to advance after reset, got current=%d initial=%d", sess.Epoch(), epoch)
	}
}

func TestExtractionLifecycle_SessionResetInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)

	preExisting := memory.MemoryEntry{
		ID:        "mem_pre_existing_01",
		Owner:     "test_user_01",
		OwnerType: memory.OwnerUser,
		Content:   "pre-existing persistent memory",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Save(preExisting); err != nil {
		t.Fatalf("failed to save pre-existing memory: %v", err)
	}

	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})

	provider := &mockBlockingExtractProvider{
		onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			select {
			case <-startedCh:
			default:
				close(startedCh)
			}
			select {
			case <-releaseCh:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: `[{"content": "stale extracted memory", "tags": ["test"], "visibility": "private"}]`,
				},
			}, nil
		},
	}

	writer := memory.NewWriter(store)
	writer.SetLLM(provider, "mock-model")

	sm := NewSessionManager()
	engine := &Engine{
		Scope:          runtimescope.New(nil, nil, nil),
		SessionManager: sm,
		MemoryWriter:   writer,
		Provider:       provider,
	}

	sess := sm.GetOrCreate("private:test_user_01")
	batch, ready := sess.EnqueuePendingTurn([]memory.PendingExtractionItem{
		{
			Owner:     "test_user_01",
			OwnerType: memory.OwnerUser,
			Message:   core.ChatMessage{Role: core.RoleUser, Content: "stale conversation"},
		},
	}, 1, 1)
	if !ready {
		t.Fatalf("expected batch to be ready")
	}

	var extractWG sync.WaitGroup
	extractWG.Add(1)
	go func() {
		defer extractWG.Done()
		engine.extractPendingBatch(batch)
	}()

	// Wait for extraction to reach LLM Chat
	select {
	case <-startedCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for extraction to begin")
	}

	// Trigger session reset while extraction LLM call is in-flight
	if err := sess.ResetSession(nil); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	close(releaseCh)
	extractWG.Wait()

	// Verify brain.json entries: pre-existing memory must stay, stale extracted memory must NOT exist
	entries, err := store.ListByOwner("test_user_01")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 entry in store, got %d: %+v", len(entries), entries)
	}
	if entries[0].Content != "pre-existing persistent memory" {
		t.Errorf("expected pre-existing memory preserved, got: %s", entries[0].Content)
	}
}

func TestExtractionLifecycle_DeterministicSessionResetRace(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)

	preExisting := memory.MemoryEntry{
		ID:        "mem_pre_race_01",
		Owner:     "test_user_race",
		OwnerType: memory.OwnerUser,
		Content:   "pre-existing valid memory",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.Save(preExisting); err != nil {
		t.Fatalf("failed to save pre-existing memory: %v", err)
	}

	provider := &mockBlockingExtractProvider{
		onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: `[{"content": "stale race memory", "tags": ["race"], "visibility": "private"}]`,
				},
			}, nil
		},
	}

	writer := memory.NewWriter(store)
	writer.SetLLM(provider, "mock-model")

	sess := &SessionContext{ConversationID: "private:test_user_race"}
	ctx, epoch, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	validatorCheckPassedCh := make(chan struct{})
	var checkCount atomic.Int32

	// Atomic validator that signals when the pre-save check passes in parseAndSave
	validator := func() bool {
		valid := ctx.Err() == nil && sess.Epoch() == epoch
		if valid {
			// Call 1: before LLM
			// Call 2: after LLM
			// Call 3: in parseAndSave entry loop before SaveConditionally
			if checkCount.Add(1) == 3 {
				select {
				case <-validatorCheckPassedCh:
				default:
					close(validatorCheckPassedCh)
				}
			}
		}
		return valid
	}

	// 1. Lock the store to block SaveConditionally before it can commit
	unlockStore := store.LockWriteForTest()

	var extractWG sync.WaitGroup
	extractWG.Add(1)
	go func() {
		defer extractWG.Done()
		_ = writer.ExtractByOwnerWithRouteContext(
			ctx,
			"test_user_race",
			memory.OwnerUser,
			core.RouteContext{},
			[]core.ChatMessage{{Role: core.RoleUser, Content: "test"}},
			validator,
		)
	}()

	// 2. Wait deterministically for validator to pass the pre-save check in parseAndSave
	select {
	case <-validatorCheckPassedCh:
	case <-time.After(2 * time.Second):
		unlockStore()
		t.Fatalf("timed out waiting for validator check to pass")
	}

	// At this point:
	// - validator() has passed in parseAndSave
	// - SaveConditionally is currently blocked waiting on store.mu write lock
	// 3. Trigger ResetSession while SaveConditionally is waiting for store.mu
	if err := sess.ResetSession(nil); err != nil {
		unlockStore()
		t.Fatalf("ResetSession failed: %v", err)
	}

	// 4. Release store lock. SaveConditionally enters critical section, re-evaluates validator inside lock
	unlockStore()
	extractWG.Wait()

	// 5. Verify that stale race memory was NOT written to store
	entries, err := store.ListByOwner("test_user_race")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 memory entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Content != "pre-existing valid memory" {
		t.Fatalf("expected pre-existing valid memory, got: %q", entries[0].Content)
	}
}

func TestExtractionLifecycle_ResetDuringCommitValidatorRejectsStaleExtraction(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "brain.json")
	store := memory.NewStore(storePath)

	initialEntry := memory.MemoryEntry{
		ID:        "mem_init",
		Owner:     "test_user_commit_race",
		OwnerType: memory.OwnerUser,
		Content:   "pre-existing valid memory",
	}
	if err := store.Save(initialEntry); err != nil {
		t.Fatalf("failed to save initial memory: %v", err)
	}

	provider := &mockBlockingExtractProvider{
		onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: `[{"content": "stale race memory from old generation", "tags": ["race"], "visibility": "private"}]`,
				},
			}, nil
		},
	}

	writer := memory.NewWriter(store)
	writer.SetLLM(provider, "mock-model")

	sess := &SessionContext{ConversationID: "private:test_user_commit_race"}
	ctx, epoch, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	var checkCount atomic.Int32
	resetTriggered := make(chan struct{})
	resetDone := make(chan struct{})

	// Goroutine that executes ResetSession when signaled by the 4th validator check
	go func() {
		<-resetTriggered
		_ = sess.ResetSession(nil)
		close(resetDone)
	}()

	// Validator hook: on the 4th call (inside Store persistence critical section),
	// trigger ResetSession and wait until ResetSession has fully returned.
	// Then return true (simulating that the validator had observed the epoch as valid
	// before ResetSession returned, or that validator returned true).
	validator := func() bool {
		cnt := checkCount.Add(1)
		if cnt == 4 {
			// Signal the concurrent thread to reset the session
			close(resetTriggered)
			// Wait until ResetSession has completely returned to the caller
			<-resetDone
			// Simulates validator having evaluated old epoch before reset completed
			return true
		}
		return ctx.Err() == nil && sess.Epoch() == epoch
	}

	err := writer.ExtractByOwnerWithRouteContext(
		ctx,
		"test_user_commit_race",
		memory.OwnerUser,
		core.RouteContext{},
		[]core.ChatMessage{{Role: core.RoleUser, Content: "test"}},
		validator,
	)

	// Verify that extraction was rejected
	if err == nil {
		t.Fatalf("expected error from aborted extraction, got nil")
	}

	// Verify that stale memory was NEVER persisted to store
	entries, err := store.ListByOwner("test_user_commit_race")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 memory entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Content != "pre-existing valid memory" {
		t.Fatalf("expected initial memory, got: %q", entries[0].Content)
	}
}

func TestExtractionLifecycle_AtomicTryBeginCommitStateTransitions(t *testing.T) {
	sess := &SessionContext{ConversationID: "private:test_user_atomic_barrier"}
	ctx, _, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier == nil {
		t.Fatalf("expected non-nil extraction barrier in context")
	}

	if !barrier.IsValid() {
		t.Fatalf("expected barrier to be valid initially")
	}

	// First TryBeginCommit transitions barrierPending -> barrierWriting
	if !barrier.TryBeginCommit() {
		t.Fatalf("expected first TryBeginCommit to succeed")
	}

	// Second TryBeginCommit must fail because state is already barrierWriting
	if barrier.TryBeginCommit() {
		t.Fatalf("expected second TryBeginCommit to fail while writing")
	}

	endCalled := make(chan struct{})
	resetDone := make(chan struct{})

	go func() {
		// ResetSession must block deterministically until EndCommit completes
		_ = sess.ResetSession(nil)
		close(resetDone)
	}()

	// Ensure ResetSession has reached AbortAndWait and is waiting
	time.Sleep(50 * time.Millisecond)
	select {
	case <-resetDone:
		t.Fatalf("ResetSession must not return while extraction barrier is in writing state")
	default:
	}

	// Signal EndCommit
	barrier.EndCommit()
	close(endCalled)

	select {
	case <-resetDone:
		// ResetSession returned after EndCommit
	case <-time.After(2 * time.Second):
		t.Fatalf("ResetSession timed out waiting for EndCommit completion")
	}

	// Subsequent TryBeginCommit must fail
	if barrier.TryBeginCommit() {
		t.Fatalf("expected TryBeginCommit to fail after barrier is completed/aborted")
	}
}

func TestExtractionLifecycle_ResetBlocksDeterministicallyWithoutTimeoutDuringWriting(t *testing.T) {
	sess := &SessionContext{ConversationID: "private:test_user_slow_disk_write"}
	ctx, _, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier == nil {
		t.Fatalf("expected non-nil extraction barrier in context")
	}

	if !barrier.TryBeginCommit() {
		t.Fatalf("expected TryBeginCommit to succeed")
	}

	resetReturned := make(chan struct{})
	go func() {
		_ = sess.ResetSession(nil)
		close(resetReturned)
	}()

	// Wait 600ms, which exceeds the former 500ms hardcoded timeout.
	// ResetSession MUST STILL be blocking because EndCommit has not been called!
	select {
	case <-resetReturned:
		t.Fatalf("ResetSession prematurely returned before EndCommit (timeout regression)")
	case <-time.After(600 * time.Millisecond):
		// Expected: ResetSession is still waiting deterministically
	}

	// Now complete the persistence
	barrier.EndCommit()

	select {
	case <-resetReturned:
		// Succeeded: unblocked as soon as EndCommit finished
	case <-time.After(2 * time.Second):
		t.Fatalf("ResetSession failed to unblock after EndCommit was called")
	}
}

func TestExtractionLifecycle_ResetBeforeTryBeginCommitAbortsAtomically(t *testing.T) {
	sess := &SessionContext{ConversationID: "private:test_user_reset_before_commit"}
	ctx, _, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier == nil {
		t.Fatalf("expected non-nil extraction barrier in context")
	}

	// Trigger ResetSession while barrier is still in barrierPending
	if err := sess.ResetSession(nil); err != nil {
		t.Fatalf("ResetSession failed: %v", err)
	}

	// Barrier must now be aborted, TryBeginCommit must return false atomically
	if barrier.TryBeginCommit() {
		t.Fatalf("expected TryBeginCommit to fail after ResetSession")
	}
	if barrier.IsValid() {
		t.Fatalf("expected IsValid to return false after ResetSession")
	}
}

func TestExtractionLifecycle_ConcurrentResetsWaitUntilWritingBarrierCompletes(t *testing.T) {
	tempDir := t.TempDir()
	brainPath := filepath.Join(tempDir, "brain.json")
	store := memory.NewStore(brainPath)

	sess := &SessionContext{ConversationID: "private:test_concurrent_resets"}
	ctx, _, cleanup := sess.BeginExtraction(context.Background())
	defer cleanup()

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier == nil {
		t.Fatalf("expected non-nil extraction barrier in context")
	}

	// Transition barrier to barrierWriting state
	if !barrier.TryBeginCommit() {
		t.Fatalf("expected TryBeginCommit to succeed")
	}

	reset1Done := make(chan struct{})
	reset2Done := make(chan struct{})

	// Launch Reset #1 concurrently
	go func() {
		_ = sess.ResetSession(nil)
		close(reset1Done)
	}()

	// Launch Reset #2 concurrently
	go func() {
		_ = sess.ResetSession(nil)
		close(reset2Done)
	}()

	// Neither Reset #1 nor Reset #2 should return while the barrier is in writing state
	time.Sleep(80 * time.Millisecond)
	select {
	case <-reset1Done:
		t.Fatalf("Reset #1 returned prematurely while extraction barrier was still writing")
	case <-reset2Done:
		t.Fatalf("Reset #2 returned prematurely while extraction barrier was still writing")
	default:
	}

	// Now complete the writing commit
	barrier.EndCommit()

	// Both concurrent resets must unblock and complete successfully
	select {
	case <-reset1Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Reset #1 timed out waiting for barrier to complete")
	}

	select {
	case <-reset2Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Reset #2 timed out waiting for barrier to complete")
	}

	// Post-condition: barrier must be terminated and cannot begin new commit
	if barrier.TryBeginCommit() {
		t.Fatalf("expected TryBeginCommit to fail on aborted barrier")
	}

	// Verify that attempting to save via this extraction context is rejected
	staleEntry := memory.MemoryEntry{
		Owner:   "test_concurrent_resets",
		Content: "stale memory that should never persist",
	}
	err := store.SaveEntriesConditionallyContext(ctx, []memory.MemoryEntry{staleEntry}, nil)
	if err == nil {
		t.Fatalf("expected store write to be rejected by aborted barrier, got nil")
	}

	entries, err := store.ListByOwner("test_concurrent_resets")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries in store, got %d: %+v", len(entries), entries)
	}
}

func TestExtractionLifecycle_MultiRouteBatchPersistsAllGroupsWithoutReset(t *testing.T) {
	tempDir := t.TempDir()
	brainPath := filepath.Join(tempDir, "brain.json")
	store := memory.NewStore(brainPath)

	var chatCount atomic.Int32
	provider := &mockBlockingExtractProvider{
		onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			idx := chatCount.Add(1)
			var content string
			if idx == 1 {
				content = `[{"content": "onebot extracted memory", "tags": ["onebot"], "visibility": "private"}]`
			} else {
				content = `[{"content": "aiocqhttp extracted memory", "tags": ["aiocqhttp"], "visibility": "private"}]`
			}
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: content,
				},
			}, nil
		},
	}

	writer := memory.NewWriter(store)
	writer.SetLLM(provider, "mock-model")

	sm := NewSessionManager()
	engine := &Engine{
		Scope:          runtimescope.New(nil, nil, nil),
		SessionManager: sm,
		MemoryWriter:   writer,
		Provider:       provider,
	}

	sess := sm.GetOrCreate("private:test_multi_user_01")
	batch, ready := sess.EnqueuePendingTurn([]memory.PendingExtractionItem{
		{
			Owner:     "test_multi_user_01",
			OwnerType: memory.OwnerUser,
			Route:     core.RouteContext{Platform: "onebot", GroupID: "group_a"},
			Message:   core.ChatMessage{Role: core.RoleUser, Content: "onebot message"},
		},
		{
			Owner:     "test_multi_user_01",
			OwnerType: memory.OwnerUser,
			Route:     core.RouteContext{Platform: "aiocqhttp", GroupID: "group_b"},
			Message:   core.ChatMessage{Role: core.RoleUser, Content: "aiocqhttp message"},
		},
	}, 1, 1)
	if !ready {
		t.Fatalf("expected batch to be ready")
	}

	// Execute extraction of multi-route batch
	engine.extractPendingBatch(batch)

	// Both route groups must have been evaluated by LLM
	if got := chatCount.Load(); got != 2 {
		t.Fatalf("expected LLM Chat to be called 2 times for 2 route groups, got %d", got)
	}

	// Both memories must have been persisted to store
	entries, err := store.ListByOwner("test_multi_user_01")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in store, got %d: %+v", len(entries), entries)
	}

	foundOnebot := false
	foundAiocqhttp := false
	for _, e := range entries {
		if e.Content == "onebot extracted memory" {
			foundOnebot = true
		}
		if e.Content == "aiocqhttp extracted memory" {
			foundAiocqhttp = true
		}
	}
	if !foundOnebot || !foundAiocqhttp {
		t.Fatalf("expected both onebot and aiocqhttp memories, got: %+v", entries)
	}
}

func TestExtractionLifecycle_MultiRouteBatchAbortsRemainingGroupsOnReset(t *testing.T) {
	tempDir := t.TempDir()
	brainPath := filepath.Join(tempDir, "brain.json")
	store := memory.NewStore(brainPath)

	var chatCount atomic.Int32
	group1Done := make(chan struct{})
	resetDone := make(chan struct{})

	sm := NewSessionManager()
	sess := sm.GetOrCreate("private:test_multi_abort_user")

	provider := &mockBlockingExtractProvider{
		onChat: func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
			idx := chatCount.Add(1)
			if idx == 1 {
				// Group 1 succeeds normally
				return &core.ChatResponse{
					Message: core.ChatMessage{
						Role:    core.RoleAssistant,
						Content: `[{"content": "group 1 memory", "tags": ["g1"], "visibility": "private"}]`,
					},
				}, nil
			}
			// Group 2 waits until reset has been triggered and completed
			select {
			case <-resetDone:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &core.ChatResponse{
				Message: core.ChatMessage{
					Role:    core.RoleAssistant,
					Content: `[{"content": "group 2 memory", "tags": ["g2"], "visibility": "private"}]`,
				},
			}, nil
		},
	}

	writer := memory.NewWriter(store)
	writer.SetLLM(provider, "mock-model")

	engine := &Engine{
		Scope:          runtimescope.New(nil, nil, nil),
		SessionManager: sm,
		MemoryWriter:   writer,
		Provider:       provider,
	}

	batch, ready := sess.EnqueuePendingTurn([]memory.PendingExtractionItem{
		{
			Owner:     "test_multi_abort_user",
			OwnerType: memory.OwnerUser,
			Route:     core.RouteContext{Platform: "onebot", GroupID: "group_1"},
			Message:   core.ChatMessage{Role: core.RoleUser, Content: "g1 msg"},
		},
		{
			Owner:     "test_multi_abort_user",
			OwnerType: memory.OwnerUser,
			Route:     core.RouteContext{Platform: "aiocqhttp", GroupID: "group_2"},
			Message:   core.ChatMessage{Role: core.RoleUser, Content: "g2 msg"},
		},
	}, 1, 1)
	if !ready {
		t.Fatalf("expected batch to be ready")
	}

	// Trigger reset after Group 1 has committed to disk
	go func() {
		for {
			entries, _ := store.ListByOwner("test_multi_abort_user")
			if len(entries) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		close(group1Done)
		_ = sess.ResetSession(nil)
		close(resetDone)
	}()

	engine.extractPendingBatch(batch)

	<-resetDone

	// Verify that Group 2 was aborted and never persisted to store
	entries, err := store.ListByOwner("test_multi_abort_user")
	if err != nil {
		t.Fatalf("failed to list memories: %v", err)
	}

	for _, e := range entries {
		if e.Content == "group 2 memory" {
			t.Fatalf("Group 2 memory must not be persisted after session reset")
		}
	}
}

