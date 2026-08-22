package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockCompactorLLM struct {
	mu           sync.Mutex
	calls        int
	delay        time.Duration
	failCount    int // number of initial calls to fail
	customReply  func(req core.ChatRequest) (string, error)
	receivedReqs []core.ChatRequest
}

func (m *mockCompactorLLM) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	m.calls++
	callIdx := m.calls
	delay := m.delay
	failCount := m.failCount
	custom := m.customReply
	m.receivedReqs = append(m.receivedReqs, req)
	m.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if custom != nil {
		content, err := custom(req)
		if err != nil {
			return nil, err
		}
		return &core.ChatResponse{
			Message: core.ChatMessage{
				Role:    core.RoleAssistant,
				Content: content,
			},
		}, nil
	}

	if callIdx <= failCount {
		return nil, errors.New("mock llm transient failure")
	}

	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: fmt.Sprintf("mock summary for call %d", callIdx),
		},
	}, nil
}

func (m *mockCompactorLLM) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func TestAppendGroupCompactMessage_SafetyBuffer(t *testing.T) {
	s := &SessionContext{
		ConversationID: "test_group_1",
	}

	// 验证默认 buffer limit 不会在 batchSize (如 5) 时提前丢弃消息
	for i := 1; i <= 15; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 20)
	}
	if got := s.GroupCompactBufferCount(); got != 15 {
		t.Fatalf("expected 15 messages in buffer, got %d", got)
	}

	// 验证达到 maxBufferSize 时从头部淘汰最旧消息
	for i := 16; i <= 25; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 20)
	}
	if got := s.GroupCompactBufferCount(); got != 20 {
		t.Fatalf("expected buffer capped at maxBufferSize=20, got %d", got)
	}

	snap, ok := s.SnapshotGroupCompact(5)
	if !ok {
		t.Fatalf("expected snapshot ready")
	}
	if len(snap.Messages) != 20 {
		t.Fatalf("expected snapshot to contain all 20 buffered messages, got %d", len(snap.Messages))
	}
	if snap.Messages[0] != "msg 6" {
		t.Errorf("expected oldest msg 1-5 dropped, first message = %q", snap.Messages[0])
	}
	if snap.Messages[19] != "msg 25" {
		t.Errorf("expected newest message = 'msg 25', got %q", snap.Messages[19])
	}
}

func TestGroupCompactor_LLMFailureRetention(t *testing.T) {
	mockLLM := &mockCompactorLLM{
		failCount: 1, // 第 1 次调用失败，第 2 次成功
	}

	tmpDir := t.TempDir()
	store, err := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))
	if err != nil {
		t.Fatalf("failed to create summary store: %v", err)
	}

	bufferSize := 5
	maxBufferSize := 50
	minInterval := 10 * time.Millisecond
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", bufferSize, minInterval)
	compactor.SetMaxBufferSize(maxBufferSize)
	compactor.SetRetryDelay(200 * time.Millisecond)

	s := &SessionContext{
		ConversationID: "test_group_fault_1",
	}

	// 1. 注入 5 条消息并触发压缩
	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), maxBufferSize)
	}
	compactor.Trigger(s, "test_group_fault_1")

	// 2. 模拟 LLM 异步执行中，群里又进来了 3 条新消息 (6, 7, 8)
	for i := 6; i <= 8; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), maxBufferSize)
	}

	// 等待第 1 次失败执行完成（此时重试计时器尚未触发，因为 retryDelay = 200ms）
	time.Sleep(30 * time.Millisecond)

	// 验证第 1 次失败后：总结未更新，1~8 号消息依然安全保存在 buffer 中，没有任何消息丢失
	if summary := s.GroupRunningSummary(); summary != "" {
		t.Errorf("expected empty summary after failure, got %q", summary)
	}
	if count := s.GroupCompactBufferCount(); count != 8 {
		t.Fatalf("expected all 8 messages retained after failure, got %d", count)
	}

	// 3. 手动触发（或等待重试），此时 LLM 成功返回
	compactor.Trigger(s, "test_group_fault_1")
	time.Sleep(30 * time.Millisecond)

	if mockLLM.CallCount() < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", mockLLM.CallCount())
	}

	// 验证成功后：总结已更新，原先失败的消息和新消息全部被成功 commit 消费
	if summary := s.GroupRunningSummary(); summary == "" {
		t.Errorf("expected non-empty summary after successful retry")
	}
	if count := s.GroupCompactBufferCount(); count != 0 {
		t.Errorf("expected buffer emptied after successful commit, got %d", count)
	}
}

func TestGroupCompactor_ConcurrentNewMessagesDuringInflight(t *testing.T) {
	mockLLM := &mockCompactorLLM{
		delay: 40 * time.Millisecond,
	}

	tmpDir := t.TempDir()
	store, err := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))
	if err != nil {
		t.Fatalf("failed to create summary store: %v", err)
	}

	bufferSize := 5
	maxBufferSize := 50
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", bufferSize, 10*time.Millisecond)
	compactor.SetMaxBufferSize(maxBufferSize)

	s := &SessionContext{
		ConversationID: "test_group_concurrent_1",
	}

	// 注入 5 条消息 (1~5) 并触发
	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), maxBufferSize)
	}
	compactor.Trigger(s, "test_group_concurrent_1")

	// 在 LLM in-flight 处理 1~5 期间，并发注入新消息 (6~8)
	time.Sleep(10 * time.Millisecond)
	for i := 6; i <= 8; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), maxBufferSize)
	}

	// 等待 in-flight compact 完成
	time.Sleep(80 * time.Millisecond)

	// 验证：1~5 被成功提交移除，6~8 依然保留在 buffer 中
	if s.GroupRunningSummary() == "" {
		t.Errorf("expected summary updated")
	}
	if count := s.GroupCompactBufferCount(); count != 3 {
		t.Fatalf("expected 3 newer messages retained, got %d", count)
	}
}

func TestGroupCompactor_CooldownDelayedTrigger(t *testing.T) {
	mockLLM := &mockCompactorLLM{}

	tmpDir := t.TempDir()
	store, err := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))
	if err != nil {
		t.Fatalf("failed to create summary store: %v", err)
	}

	bufferSize := 5
	minInterval := 60 * time.Millisecond
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", bufferSize, minInterval)

	s := &SessionContext{
		ConversationID: "test_group_cooldown_1",
	}

	// Batch 1: 5 条消息
	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 100)
	}
	compactor.Trigger(s, "test_group_cooldown_1")
	time.Sleep(15 * time.Millisecond)

	if mockLLM.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", mockLLM.CallCount())
	}

	// 处于 minInterval 冷却期内时，注入 Batch 2 (6~10) 并调用 Trigger
	for i := 6; i <= 10; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 100)
	}
	compactor.Trigger(s, "test_group_cooldown_1")

	// 此时因为处于冷却期，不应立即发起第 2 次 LLM 调用
	if mockLLM.CallCount() != 1 {
		t.Fatalf("expected still 1 call during cooldown, got %d", mockLLM.CallCount())
	}

	// 等待冷却结束，定时器自动触发第 2 次压缩
	time.Sleep(80 * time.Millisecond)

	if mockLLM.CallCount() != 2 {
		t.Fatalf("expected delayed trigger to execute call 2, got %d", mockLLM.CallCount())
	}
	if count := s.GroupCompactBufferCount(); count != 0 {
		t.Errorf("expected buffer fully compacted, got %d remaining", count)
	}
}

func TestGroupCompactor_ResetInvalidatesInflight(t *testing.T) {
	var inFlight atomic.Bool
	mockLLM := &mockCompactorLLM{
		delay: 50 * time.Millisecond,
		customReply: func(req core.ChatRequest) (string, error) {
			inFlight.Store(true)
			return "stale summary", nil
		},
	}

	tmpDir := t.TempDir()
	store, _ := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 10*time.Millisecond)

	s := &SessionContext{
		ConversationID: "test_group_reset_1",
	}

	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 50)
	}
	compactor.Trigger(s, "test_group_reset_1")

	// 等待 LLM 开始处理
	time.Sleep(15 * time.Millisecond)

	// 重置会话总结（例如管理员清空或删除群聊总结）
	s.ResetGroupCompact()

	// 等待 LLM 请求结束
	time.Sleep(70 * time.Millisecond)

	// 验证已失效的 in-flight 结果不会被写入
	if got := s.GroupRunningSummary(); got != "" {
		t.Errorf("expected summary to remain empty after reset, got %q", got)
	}
}

func TestGroupCompactor_AutomaticRetryOnFailure(t *testing.T) {
	mockLLM := &mockCompactorLLM{
		failCount: 1, // 第一次失败，之后成功
	}

	tmpDir := t.TempDir()
	store, _ := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 20*time.Millisecond)
	compactor.SetRetryDelay(40 * time.Millisecond)

	s := &SessionContext{
		ConversationID: "test_group_autoretry_1",
	}

	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 50)
	}
	compactor.Trigger(s, "test_group_autoretry_1")

	// 第一次调用失败
	time.Sleep(20 * time.Millisecond)
	if count := mockLLM.CallCount(); count != 1 {
		t.Fatalf("expected 1 call after initial failure, got %d", count)
	}
	if s.GroupRunningSummary() != "" {
		t.Errorf("expected empty summary after initial failure")
	}

	// 不追加新消息，静候失败自动重试定时器 (max(retryDelay, remaining cooldown) = 40ms) 到期
	time.Sleep(80 * time.Millisecond)

	if count := mockLLM.CallCount(); count != 2 {
		t.Fatalf("expected 2 calls after automatic retry, got %d", count)
	}
	if s.GroupRunningSummary() == "" {
		t.Errorf("expected summary populated after automatic retry")
	}
	if count := s.GroupCompactBufferCount(); count != 0 {
		t.Errorf("expected buffer cleared after successful automatic retry, got %d", count)
	}
}

func TestGroupCompactor_BufferSizeInvariant(t *testing.T) {
	mockLLM := &mockCompactorLLM{}
	tmpDir := t.TempDir()
	store, _ := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))

	// 1. 初始化时验证 bufferSize 与 maxBufferSize
	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 20, 30*time.Second)
	if compactor.MaxBufferSize() < compactor.BufferSize() {
		t.Fatalf("expected initial maxBufferSize >= bufferSize, got max=%d, buf=%d", compactor.MaxBufferSize(), compactor.BufferSize())
	}

	// 2. SetMaxBufferSize 小于 bufferSize 时被自动修正为 bufferSize
	compactor.SetBufferSize(20)
	compactor.SetMaxBufferSize(10)
	if compactor.MaxBufferSize() != 20 {
		t.Fatalf("expected maxBufferSize clamped to bufferSize=20, got %d", compactor.MaxBufferSize())
	}

	// 3. SetBufferSize 增大超过 maxBufferSize 时，maxBufferSize 自动随之扩展
	compactor.SetBufferSize(50)
	if compactor.MaxBufferSize() < 50 {
		t.Fatalf("expected maxBufferSize expanded to at least bufferSize=50, got %d", compactor.MaxBufferSize())
	}

	// 4. 验证在非法配置尝试下，系统依然能正常积累满 bufferSize 条消息并成功触发压缩
	compactor.SetBufferSize(5)
	compactor.SetMaxBufferSize(2) // 被 clamp 为 5
	s := &SessionContext{
		ConversationID: "test_group_invariant_1",
	}
	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), compactor.MaxBufferSize())
	}
	if s.GroupCompactBufferCount() != 5 {
		t.Fatalf("expected 5 messages kept in buffer, got %d", s.GroupCompactBufferCount())
	}
	compactor.Trigger(s, "test_group_invariant_1")
	time.Sleep(20 * time.Millisecond)
	if mockLLM.CallCount() != 1 {
		t.Fatalf("expected 1 LLM call on trigger, got %d", mockLLM.CallCount())
	}
}

func TestGroupCompactor_PendingPersistenceRetryAndRecovery(t *testing.T) {
	mockLLM := &mockCompactorLLM{}

	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "group_summaries.json")
	store, err := groupsummary.NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	var saveAttempts atomic.Int32
	firstAttemptDone := make(chan struct{})
	recoveredChan := make(chan struct{})

	// 第一次持久化失败，第二次及后续成功
	store.SetSaveHook(func(records map[string]groupsummary.Record) error {
		attempt := saveAttempts.Add(1)
		if attempt == 1 {
			close(firstAttemptDone)
			return errors.New("simulated transient disk I/O error")
		}
		select {
		case <-recoveredChan:
		default:
			close(recoveredChan)
		}
		return nil
	})

	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 10*time.Millisecond)
	s := &SessionContext{
		ConversationID: "test_group_persist_fail_1",
	}

	for i := 1; i <= 5; i++ {
		s.AppendGroupCompactMessage(fmt.Sprintf("msg %d", i), 50)
	}

	// 触发 compact，LLM 会成功返回
	compactor.Trigger(s, "test_group_persist_fail_1")

	// 等待第 1 次持久化尝试失败
	select {
	case <-firstAttemptDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for first persist attempt")
	}

	// 验证：内存中的 raw messages 已成功 commit 被清空，内存 summary 已更新
	if s.GroupRunningSummary() == "" {
		t.Fatalf("expected memory summary to be updated")
	}
	if count := s.GroupCompactBufferCount(); count != 0 {
		t.Fatalf("expected raw messages <= ThroughSequence removed from buffer, got %d", count)
	}

	// 验证：处于 dirty / pending persistence 状态
	if !compactor.HasPendingPersistence("test_group_persist_fail_1") {
		t.Fatalf("expected HasPendingPersistence to be true while disk writes fail")
	}

	// 等待后台独立重试定时器到期执行（第 1 次重试延迟为 50ms）
	select {
	case <-recoveredChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for retry recovery")
	}

	// 等待 worker 更新完 pending 状态
	time.Sleep(20 * time.Millisecond)

	// 验证：重试已成功落盘，dirty 状态清除
	if compactor.HasPendingPersistence("test_group_persist_fail_1") {
		t.Fatalf("expected HasPendingPersistence to be false after recovery")
	}
	rec, ok, err := store.Get("test_group_persist_fail_1")
	if err != nil || !ok || rec.Summary == "" {
		t.Fatalf("expected summary successfully persisted to disk after retry, got ok=%v, err=%v, rec=%+v", ok, err, rec)
	}
}

func TestGroupCompactor_NewerPersistenceOverridesPendingRetryTimer(t *testing.T) {
	mockLLM := &mockCompactorLLM{}
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "group_summaries.json")
	store, _ := groupsummary.NewStore(storePath)

	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 10*time.Millisecond)
	owner := "test_group_override_timer_1"

	var v1FailCount atomic.Int32
	var v2FailCount atomic.Int32
	v1FailedChan := make(chan struct{})
	v2RecoveredChan := make(chan struct{})

	store.SetSaveHook(func(records map[string]groupsummary.Record) error {
		rec := records[owner]
		if rec.Summary == "Summary V1" {
			v1FailCount.Add(1)
			select {
			case <-v1FailedChan:
			default:
				close(v1FailedChan)
			}
			return errors.New("v1 disk error")
		}
		if rec.Summary == "Summary V2" {
			if v2FailCount.Add(1) == 1 {
				// V2 第一次尝试也失败
				return errors.New("v2 initial disk error")
			}
			// V2 第二次（重试）成功
			select {
			case <-v2RecoveredChan:
			default:
				close(v2RecoveredChan)
			}
			return nil
		}
		return nil
	})

	// 1. 提交 V1，触发失败并启动 V1 的 retry timer
	compactor.queuePersistence(owner, "Summary V1", 0)
	select {
	case <-v1FailedChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for v1 failure")
	}

	// 确保此时处于 dirty 状态且正在等待重试
	time.Sleep(10 * time.Millisecond)
	if !compactor.HasPendingPersistence(owner) {
		t.Fatalf("expected pending persistence for v1")
	}

	// 2. 在 V1 的 retry timer 尚未触发时，V2 到来
	compactor.queuePersistence(owner, "Summary V2", 0)

	// 3. 验证 V2 即使初次失败，也绝不会被 V1 的旧 timer 吞掉或卡死，而是会由自己的 timer 持续重试并最终成功
	select {
	case <-v2RecoveredChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for v2 retry recovery")
	}

	time.Sleep(20 * time.Millisecond)
	if compactor.HasPendingPersistence(owner) {
		t.Fatalf("expected pending persistence cleared after v2 recovery")
	}
	rec, ok, _ := store.Get(owner)
	if !ok || rec.Summary != "Summary V2" {
		t.Fatalf("expected Summary V2 persisted on disk, got %q", rec.Summary)
	}
}

func TestGroupCompactor_ConcurrentPersistenceTOCTOUOrdering(t *testing.T) {
	mockLLM := &mockCompactorLLM{}
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "group_summaries.json")
	store, _ := groupsummary.NewStore(storePath)

	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 10*time.Millisecond)
	owner := "test_group_toctou_1"

	v1SavingChan := make(chan struct{})
	releaseV1Chan := make(chan struct{})
	v2DoneChan := make(chan struct{})

	store.SetSaveHook(func(records map[string]groupsummary.Record) error {
		rec := records[owner]
		if rec.Summary == "Summary V1" {
			// 通知测试：V1 正在执行 Upsert 写入
			select {
			case <-v1SavingChan:
			default:
				close(v1SavingChan)
			}
			// 阻塞等待，模拟慢速 I/O
			<-releaseV1Chan
			return nil
		}
		if rec.Summary == "Summary V2" {
			select {
			case <-v2DoneChan:
			default:
				close(v2DoneChan)
			}
			return nil
		}
		return nil
	})

	// 1. 发起 V1 持久化，worker 会进入 Upsert(V1) 并阻塞在 hook 中
	compactor.queuePersistence(owner, "Summary V1", 0)

	select {
	case <-v1SavingChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for v1 to begin saving")
	}

	// 2. 当 V1 正在 Upsert 中时，并发写入 V2
	compactor.queuePersistence(owner, "Summary V2", 0)

	// 3. 释放 V1 的写入阻塞
	close(releaseV1Chan)

	// 4. 等待 V2 持久化完成
	select {
	case <-v2DoneChan:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for v2 to finish saving")
	}

	time.Sleep(20 * time.Millisecond)

	// 5. 验证无论调度顺序如何，最终磁盘上的总结绝对是 Summary V2，绝不能回退到 V1
	rec, ok, _ := store.Get(owner)
	if !ok || rec.Summary != "Summary V2" {
		t.Fatalf("expected final summary on disk to be Summary V2, got %q", rec.Summary)
	}
	if compactor.HasPendingPersistence(owner) {
		t.Fatalf("expected no pending persistence after V2 completed")
	}
}

func TestGroupCompactor_ScheduledTimerStaleCallbackRace(t *testing.T) {
	mockLLM := &mockCompactorLLM{}
	tmpDir := t.TempDir()
	store, _ := groupsummary.NewStore(filepath.Join(tmpDir, "group_summaries.json"))

	compactor := NewGroupCompactor(mockLLM, store, "mock-model", 5, 100*time.Millisecond)
	s := &SessionContext{
		ConversationID: "test_group_stale_timer_1",
	}
	owner := "test_group_stale_timer_1"
	key := s.ConversationID

	compactor.mu.Lock()
	// 模拟调度了 Generation 1 的 timer
	compactor.scheduleTriggerLocked(s, owner, 1*time.Hour)
	token1 := compactor.scheduledToken[key]
	timer1 := compactor.scheduled[key]
	compactor.mu.Unlock()

	if token1 == 0 || timer1 == nil {
		t.Fatalf("expected timer 1 scheduled")
	}

	// 模拟外部事件取消并创建了 Generation 2 的 timer
	compactor.mu.Lock()
	compactor.cancelScheduledLocked(key)
	compactor.scheduleTriggerLocked(s, owner, 2*time.Hour)
	token2 := compactor.scheduledToken[key]
	timer2 := compactor.scheduled[key]
	compactor.mu.Unlock()

	if token2 <= token1 {
		t.Fatalf("expected token2 (%d) > token1 (%d)", token2, token1)
	}
	if timer2 == nil {
		t.Fatalf("expected timer 2 scheduled")
	}

	// 模拟 timer1 的 callback 延迟唤醒并获取锁尝试清理
	compactor.mu.Lock()
	if compactor.scheduledToken[key] == token1 {
		delete(compactor.scheduled, key)
		delete(compactor.scheduledToken, key)
	}
	compactor.mu.Unlock()

	// 验证：timer2 依然安全保留在 scheduled map 中，没有被旧 callback 误删
	compactor.mu.Lock()
	activeTimer := compactor.scheduled[key]
	activeToken := compactor.scheduledToken[key]
	compactor.mu.Unlock()

	if activeTimer != timer2 || activeToken != token2 {
		t.Fatalf("expected timer2 (token %d) to remain active, got timer=%v, token=%d", token2, activeTimer, activeToken)
	}
}
