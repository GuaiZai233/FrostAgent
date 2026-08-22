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

	// 不追加新消息，静候失败自动重试定时器 (40ms) 到期
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
