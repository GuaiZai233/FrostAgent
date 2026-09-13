package admincmd

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/security"
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type mockLLM struct {
	mu       sync.Mutex
	response string
	err      error
}

func (m *mockLLM) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	resp := m.response
	if resp == "" {
		resp = "这是压缩总结测试结果"
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: resp,
		},
	}, nil
}

func TestExecutor_Reset(t *testing.T) {
	tmpDir := t.TempDir()
	summaryStore, err := groupsummary.NewStore(filepath.Join(tmpDir, "summaries.json"))
	if err != nil {
		t.Fatalf("failed to open summary store: %v", err)
	}

	sessionMgr := llm.NewSessionManager()
	sessionID := "test:group:1001"
	session := sessionMgr.GetOrCreate(sessionID)
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "hello"})
	session.SetGroupRunningSummary("old summary")
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{Content: "msg1"}, 10)

	// Persist summary in store
	_, _ = summaryStore.Upsert(sessionID, "persisted summary", 0)

	// Start active in-flight run
	runCtx, _, cancelRun := session.BeginRun(context.Background())
	defer cancelRun()

	engine := &llm.Engine{
		SessionManager:     sessionMgr,
		GroupSummaryStore:  summaryStore,
		ModelName:          "mock-model",
		Provider:           &mockLLM{},
	}

	exec := NewExecutor(engine)
	var replies []string
	replyFunc := func(ctx context.Context, text string, isIntermediate bool) error {
		replies = append(replies, text)
		return nil
	}

	cmdCtx := CommandContext{
		SessionID:    sessionID,
		Owner:        "group:1001",
		IsGroup:      true,
		CallerUserID: "admin-1",
		RouteScope:   modelrouter.Scope{Platform: "test", GroupID: "1001"},
		Reply:        replyFunc,
	}

	initialEpoch := session.Epoch()
	cmd := ParsedCommand{Type: CmdReset}

	if err := exec.Execute(context.Background(), cmdCtx, cmd); err != nil {
		t.Fatalf("Execute reset failed: %v", err)
	}

	// 1. In-flight run context should be cancelled
	select {
	case <-runCtx.Done():
		// ok
	default:
		t.Errorf("expected active run context to be cancelled by reset")
	}

	// 2. Epoch should be incremented
	if session.Epoch() <= initialEpoch {
		t.Errorf("expected session epoch to increment, got %d <= %d", session.Epoch(), initialEpoch)
	}

	// 3. History and buffers should be cleared
	if len(session.Snapshot()) != 0 {
		t.Errorf("expected empty history, got %d messages", len(session.Snapshot()))
	}
	if session.GroupRunningSummary() != "" {
		t.Errorf("expected empty group running summary, got %q", session.GroupRunningSummary())
	}
	if session.GroupCompactBufferCount() != 0 {
		t.Errorf("expected empty uncompacted buffer, got %d items", session.GroupCompactBufferCount())
	}

	// 4. Persisted summary in store should be deleted
	if _, ok, _ := summaryStore.Get(sessionID); ok {
		t.Errorf("expected summary in store to be deleted")
	}

	// 5. Reply received
	if len(replies) == 0 || replies[0] != "当前会话已重置。" {
		t.Errorf("unexpected replies: %v", replies)
	}
}

func TestExecutor_BanAndUnban(t *testing.T) {
	tmpDir := t.TempDir()
	secCtrl := security.NewController(tmpDir)

	scope := newTestScope(t, map[string]string{
		AdminQQIDsEnv: "admin-1,admin-2",
	})

	engine := &llm.Engine{
		Scope:          scope,
		Security:       secCtrl,
		SessionManager: llm.NewSessionManager(),
		ModelName:      "mock-model",
	}

	exec := NewExecutor(engine)
	var replies []string
	replyFunc := func(ctx context.Context, text string, isIntermediate bool) error {
		replies = append(replies, text)
		return nil
	}

	cmdCtx := CommandContext{
		SessionID:    "test:group:1001",
		Owner:        "group:1001",
		IsGroup:      true,
		CallerUserID: "admin-1",
		Reply:        replyFunc,
	}

	// 1. Cannot ban caller themselves
	replies = nil
	_ = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdBan, Args: []string{"admin-1"}})
	if len(replies) == 0 || replies[0] != "无法封禁当前调用者账号。" {
		t.Errorf("expected cannot ban caller reply, got: %v", replies)
	}

	// 2. Cannot ban other admin
	replies = nil
	_ = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdBan, Args: []string{"admin-2"}})
	if len(replies) == 0 || replies[0] != "无法封禁管理员账号。" {
		t.Errorf("expected cannot ban admin reply, got: %v", replies)
	}

	// 3. Ban normal user
	replies = nil
	targetUser := "user-99"
	err := exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdBan, Args: []string{targetUser}})
	if err != nil {
		t.Fatalf("ban failed: %v", err)
	}
	if len(replies) == 0 || replies[0] != "已成功封禁用户 user-99。" {
		t.Errorf("expected success ban reply, got: %v", replies)
	}

	// Check user is locked in security controller
	p, err := security.NewPrincipal("qq", targetUser)
	if err != nil {
		t.Fatalf("NewPrincipal failed: %v", err)
	}
	if err := secCtrl.CheckAccess(p); err != security.ErrLocked {
		t.Errorf("expected user to be locked with ErrLocked, got: %v", err)
	}

	// 4. Unban normal user
	replies = nil
	err = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdUnban, Args: []string{targetUser}})
	if err != nil {
		t.Fatalf("unban failed: %v", err)
	}
	if len(replies) == 0 || replies[0] != "已成功解封用户 user-99。" {
		t.Errorf("expected success unban reply, got: %v", replies)
	}

	// Check user is unlocked in security controller
	if err := secCtrl.CheckAccess(p); err != nil {
		t.Errorf("expected user to be unlocked, got: %v", err)
	}
}

func TestExecutor_Compact_Group(t *testing.T) {
	tmpDir := t.TempDir()
	summaryStore, err := groupsummary.NewStore(filepath.Join(tmpDir, "summaries.json"))
	if err != nil {
		t.Fatalf("failed to open summary store: %v", err)
	}

	mockProvider := &mockLLM{response: "新的群聊总结"}
	compactor := llm.NewGroupCompactor(mockProvider, summaryStore, "mock-model", 10, 30*time.Second)

	sessionMgr := llm.NewSessionManager()
	engine := &llm.Engine{
		SessionManager:    sessionMgr,
		GroupCompactor:    compactor,
		GroupSummaryStore: summaryStore,
		ModelName:         "mock-model",
		Provider:          mockProvider,
	}

	exec := NewExecutor(engine)

	// 1. Group with nothing to compact
	sessionID := "test:group:1001"
	var replies []string
	var mu sync.Mutex
	replyFunc := func(ctx context.Context, text string, isIntermediate bool) error {
		mu.Lock()
		defer mu.Unlock()
		replies = append(replies, text)
		return nil
	}

	cmdCtx := CommandContext{
		SessionID:    sessionID,
		Owner:        "group:1001",
		IsGroup:      true,
		CallerUserID: "admin-1",
		RouteScope:   modelrouter.Scope{Platform: "test", GroupID: "1001"},
		Reply:        replyFunc,
	}

	_ = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdCompact})
	if len(replies) == 0 || replies[0] != "当前会话没有需要压缩的内容。" {
		t.Errorf("expected nothing to compact, got: %v", replies)
	}

	// 2. Group with messages to compact
	session := sessionMgr.GetOrCreate(sessionID)
	session.AppendGroupCompactMessage(llm.GroupCompactMessage{
		Role:    "user",
		Sender:  "user-1",
		Content: "群聊消息内容",
	}, 10)

	replies = nil
	err = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdCompact})
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(replies) == 0 || replies[0] != "已开始群聊上下文压缩总结..." {
		t.Errorf("expected start receipt, got: %v", replies)
	}

	// Wait for background completion
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(replies)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(replies) < 2 || replies[1] != "群聊上下文压缩总结完成。" {
		t.Errorf("expected completion receipt, got: %v", replies)
	}
}

func TestExecutor_Compact_Private(t *testing.T) {
	mockProvider := &mockLLM{response: "私聊总结"}
	sessionMgr := llm.NewSessionManager()

	engine := &llm.Engine{
		SessionManager: sessionMgr,
		ModelName:      "mock-model",
		Provider:       mockProvider,
	}

	exec := NewExecutor(engine)
	sessionID := "test:private:2001"

	var replies []string
	var mu sync.Mutex
	replyFunc := func(ctx context.Context, text string, isIntermediate bool) error {
		mu.Lock()
		defer mu.Unlock()
		replies = append(replies, text)
		return nil
	}

	cmdCtx := CommandContext{
		SessionID:    sessionID,
		Owner:        "private:2001",
		IsGroup:      false,
		CallerUserID: "admin-1",
		RouteScope:   modelrouter.Scope{Platform: "test"},
		Reply:        replyFunc,
	}

	// 1. Nothing to compact
	_ = exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdCompact})
	if len(replies) == 0 || replies[0] != "当前会话没有需要压缩的内容。" {
		t.Errorf("expected nothing to compact for private, got: %v", replies)
	}

	// 2. Add private messages and compact
	session := sessionMgr.GetOrCreate(sessionID)
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "私聊问题1"})
	session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "私聊回答1"})
	session.AddMessage(core.ChatMessage{Role: core.RoleUser, Content: "私聊问题2"})
	session.AddMessage(core.ChatMessage{Role: core.RoleAssistant, Content: "私聊回答2"})

	replies = nil
	err := exec.Execute(context.Background(), cmdCtx, ParsedCommand{Type: CmdCompact})
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(replies) == 0 || replies[0] != "已开始私聊上下文压缩总结..." {
		t.Errorf("expected start receipt, got: %v", replies)
	}

	// Wait for background private compact to complete
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(replies)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(replies) < 2 || replies[1] != "私聊上下文压缩总结完成。" {
		t.Errorf("expected completion receipt, got: %v", replies)
	}
}
