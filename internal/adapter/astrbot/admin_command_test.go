package astrbot

import (
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newAdminTestScope(t *testing.T, env map[string]string) *runtimescope.Scope {
	t.Helper()
	dir := t.TempDir()
	instanceStore, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatalf("打开实例配置失败: %v", err)
	}
	globalStore, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatalf("打开全局配置失败: %v", err)
	}
	for k, v := range env {
		if err := instanceStore.Update(k, v, false); err != nil {
			t.Fatalf("写入配置失败: %v", err)
		}
	}
	return runtimescope.New(instanceStore, globalStore, nil)
}

func TestExtractAstrBotAdminCommand(t *testing.T) {
	// 1. IsAt == false -> non candidate even with command text
	eventNoAt := Event{
		IsAt:    false,
		Content: "/reset",
	}
	_, isCand, err := extractAstrBotAdminCommand(eventNoAt, "/")
	if isCand || err != nil {
		t.Fatalf("expected isCand=false when IsAt is false, got isCand=%v err=%v", isCand, err)
	}

	// 2. IsAt == true with leading mention [@bot]
	eventWithAt := Event{
		IsAt:    true,
		Content: "[@bot] /reset",
	}
	cmd, isCand, err := extractAstrBotAdminCommand(eventWithAt, "/")
	if !isCand || err != nil || cmd.Type != admincmd.CmdReset {
		t.Fatalf("expected CmdReset candidate, got isCand=%v err=%v type=%v", isCand, err, cmd.Type)
	}

	// 3. IsAt == true with ban args
	eventBan := Event{
		IsAt:    true,
		Content: "@bot /ban 10001",
	}
	cmd, isCand, err = extractAstrBotAdminCommand(eventBan, "/")
	if !isCand || err != nil || cmd.Type != admincmd.CmdBan || len(cmd.Args) != 1 || cmd.Args[0] != "10001" {
		t.Fatalf("expected CmdBan candidate, got isCand=%v err=%v cmd=%+v", isCand, err, cmd)
	}

	// 4. IsAt == true with word prefix
	eventWord := Event{
		IsAt:    true,
		Content: "@bot execute reset",
	}
	cmd, isCand, err = extractAstrBotAdminCommand(eventWord, "execute")
	if !isCand || err != nil || cmd.Type != admincmd.CmdReset {
		t.Fatalf("expected CmdReset candidate with word prefix, got isCand=%v err=%v", isCand, err)
	}

	// 5. IsAt == true with syntax error
	eventErr := Event{
		IsAt:    true,
		Content: "@bot /ban",
	}
	_, isCand, err = extractAstrBotAdminCommand(eventErr, "/")
	if !isCand || err == nil {
		t.Fatalf("expected candidate with error, got isCand=%v err=%v", isCand, err)
	}
}

func setupTestAstrBotWS(t *testing.T) (*wsConn, chan Action, func()) {
	t.Helper()
	actionCh := make(chan Action, 10)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var act Action
			if json.Unmarshal(msg, &act) == nil {
				actionCh <- act
			}
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("Dial ws failed: %v", err)
	}

	conn := newWSConn(clientConn)

	cleanup := func() {
		_ = clientConn.Close()
		server.Close()
	}

	return conn, actionCh, cleanup
}

func TestHandleAdminCommand_AstrBot_AdminFlow(t *testing.T) {
	conn, actionCh, cleanup := setupTestAstrBotWS(t)
	defer cleanup()

	tmpDir := t.TempDir()
	summaryStore, _ := groupsummary.NewStore(filepath.Join(tmpDir, "summaries.json"))
	secCtrl := security.NewController(tmpDir)

	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv:         "20001",
		admincmd.AdminCommandPrefixEnv: "/",
	})

	engine := &llm.Engine{
		Scope:             scope,
		Security:          secCtrl,
		SessionManager:    llm.NewSessionManager(),
		GroupSummaryStore: summaryStore,
		ModelName:         "mock-model",
		Provider:          &mockLLMProvider{},
	}
	conn.Scope = scope

	// 1. Admin sends @bot /reset
	event := Event{
		MessageID:   "msg_001",
		IsAt:        true,
		UserID:      "20001", // admin
		GroupID:     "30001",
		MessageType: "group",
		Content:     "[@bot] /reset",
	}

	handled := handleAdminCommand(conn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for admin /reset")
	}

	select {
	case act := <-actionCh:
		if act.Type != "action" || act.Action != "send_message" {
			t.Errorf("expected send_message action, got: %+v", act)
		}
		if act.Content != "当前会话已重置。" {
			t.Errorf("unexpected reply content: %q", act.Content)
		}
		if act.IsIntermediate {
			t.Errorf("reset reply should not be intermediate")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for reset reply action")
	}

	// 2. Admin sends syntax error @bot /ban
	eventSyntaxErr := Event{
		MessageID:   "msg_002",
		IsAt:        true,
		UserID:      "20001",
		GroupID:     "30001",
		MessageType: "group",
		Content:     "@bot /ban",
	}

	handled = handleAdminCommand(conn, eventSyntaxErr, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for syntax error")
	}

	select {
	case act := <-actionCh:
		if act.Type != "action" || act.Action != "send_message" {
			t.Errorf("expected send_message action, got: %+v", act)
		}
		if !strings.Contains(act.Content, "/ban <userID>") {
			t.Errorf("expected usage info in reply, got: %q", act.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for syntax error reply")
	}
}

func TestHandleAdminCommand_AstrBot_LockedAdminRejected(t *testing.T) {
	conn, actionCh, cleanup := setupTestAstrBotWS(t)
	defer cleanup()

	tmpDir := t.TempDir()
	secCtrl := security.NewController(tmpDir)

	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv:         "20001",
		admincmd.AdminCommandPrefixEnv: "/",
	})

	engine := &llm.Engine{
		Scope:          scope,
		Security:       secCtrl,
		SessionManager: llm.NewSessionManager(),
		ModelName:      "mock-model",
		Provider:       &mockLLMProvider{},
	}
	conn.Scope = scope

	// Lock admin principal in security controller
	adminPrincipal, err := security.NewPrincipal("astrbot", "20001")
	if err != nil {
		t.Fatalf("NewPrincipal failed: %v", err)
	}
	if err := secCtrl.Lock(adminPrincipal, "locked admin account"); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// 1. Admin sends @bot /reset
	event := Event{
		MessageID:   "msg_locked_01",
		IsAt:        true,
		UserID:      "20001",
		GroupID:     "30001",
		MessageType: "group",
		Platform:    "astrbot",
		Content:     "@bot /reset",
	}

	handled := handleAdminCommand(conn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for locked admin")
	}

	select {
	case act := <-actionCh:
		if act.Type != "action" || act.Action != "send_message" {
			t.Errorf("expected send_message action, got: %+v", act)
		}
		if !strings.Contains(act.Content, security.RejectGatewayMsg) {
			t.Errorf("expected RejectGatewayMsg, got: %q", act.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for locked admin rejection reply")
	}

	// 2. Locked admin sends @bot /unban 20001 attempting self-unlock
	eventUnban := Event{
		MessageID:   "msg_locked_02",
		IsAt:        true,
		UserID:      "20001",
		GroupID:     "30001",
		MessageType: "group",
		Platform:    "astrbot",
		Content:     "@bot /unban 20001",
	}

	handled = handleAdminCommand(conn, eventUnban, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for locked admin unban attempt")
	}

	select {
	case act := <-actionCh:
		if act.Type != "action" || act.Action != "send_message" {
			t.Errorf("expected send_message action, got: %+v", act)
		}
		if !strings.Contains(act.Content, security.RejectGatewayMsg) {
			t.Errorf("expected RejectGatewayMsg on unban attempt, got: %q", act.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for locked admin rejection reply on unban")
	}

	// Verify admin is still locked
	if err := secCtrl.CheckAccess(adminPrincipal); !errors.Is(err, security.ErrLocked) {
		t.Fatalf("expected admin to remain locked, got err=%v", err)
	}
}

func TestHandleAdminCommand_AstrBot_NonAdminSilentDrop(t *testing.T) {
	conn, actionCh, cleanup := setupTestAstrBotWS(t)
	defer cleanup()

	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv:         "20001",
		admincmd.AdminCommandPrefixEnv: "/",
	})

	engine := &llm.Engine{
		Scope:          scope,
		SessionManager: llm.NewSessionManager(),
		ModelName:      "mock-model",
		Provider:       &mockLLMProvider{},
	}
	conn.Scope = scope

	// Non-admin sends @bot /reset
	event := Event{
		MessageID:   "msg_003",
		SessionID:   "sess_003",
		IsAt:        true,
		UserID:      "20002", // non-admin
		GroupID:     "30001",
		MessageType: "group",
		Content:     "[@bot] /reset",
	}

	handled := handleAdminCommand(conn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for non-admin candidate")
	}

	// AstrBot must send noop action to terminate upstream waiting without user text
	select {
	case act := <-actionCh:
		if act.Type != "action" || act.Action != "noop" {
			t.Errorf("expected noop action for non-admin silent drop, got: %+v", act)
		}
		if act.SubType != "admin_silent_drop" {
			t.Errorf("expected subtype admin_silent_drop, got: %s", act.SubType)
		}
		if !act.SuppressLLM {
			t.Errorf("expected SuppressLLM true for admin silent drop, got false")
		}
		if act.Echo != "reply_msg_003" {
			t.Errorf("expected echo reply_msg_003, got: %s", act.Echo)
		}
		if act.Content != "" {
			t.Errorf("expected no user text in noop action, got: %q", act.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for noop action")
	}
}

func TestHandleAdminCommand_AstrBot_NonCandidatePassthrough(t *testing.T) {
	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv:         "20001",
		admincmd.AdminCommandPrefixEnv: "/",
	})

	engine := &llm.Engine{
		Scope:          scope,
		SessionManager: llm.NewSessionManager(),
		ModelName:      "mock-model",
		Provider:       &mockLLMProvider{},
	}

	event := Event{
		MessageID:   "msg_004",
		IsAt:        false,
		UserID:      "20001",
		GroupID:     "30001",
		MessageType: "group",
		Content:     "你好啊",
	}

	handled := handleAdminCommand(nil, event, engine)
	if handled {
		t.Fatalf("expected handleAdminCommand to return false for non-candidate chat message")
	}
}

func TestAstrBotQueuedTurnEpochInvalidation(t *testing.T) {
	sessionMgr := llm.NewSessionManager()
	session := sessionMgr.GetOrCreate("group:30001")

	turn1 := session.ReserveTurn()
	if turn1 == nil {
		t.Fatalf("expected turn1 not nil")
	}

	turn2 := session.ReserveTurn()
	if turn2 == nil {
		t.Fatalf("expected turn2 not nil")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	turn2Finished := false
	turn2Valid := true

	go func() {
		defer wg.Done()
		turn2.Wait()
		defer turn2.Done()

		turn2Valid = turn2.IsValid(session)
		turn2Finished = true
	}()

	session.ResetSession(nil)
	turn1.Done()

	wg.Wait()

	if !turn2Finished {
		t.Fatalf("expected turn2 to finish")
	}
	if turn2Valid {
		t.Errorf("expected turn2 to be invalidated after session reset, but IsValid returned true")
	}
}

func TestSendAstrBotAdminReply_PlatformPopulated(t *testing.T) {
	conn, actionCh, cleanup := setupTestAstrBotWS(t)
	defer cleanup()

	// 1. Explicit platform
	eventTG := Event{
		MessageID:   "msg_tg_01",
		Platform:    "telegram",
		MessageType: "group",
		GroupID:     "tg_group_1",
		UserID:      "tg_user_1",
	}
	if err := sendAstrBotAdminReply(eventTG, conn, "测试回复", false); err != nil {
		t.Fatalf("sendAstrBotAdminReply failed: %v", err)
	}

	select {
	case act := <-actionCh:
		if act.Platform != "telegram" {
			t.Errorf("expected act.Platform == 'telegram', got %q", act.Platform)
		}
		if act.GroupID != "tg_group_1" {
			t.Errorf("expected act.GroupID == 'tg_group_1', got %q", act.GroupID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for action")
	}

	// 2. Default platform fallback
	eventDefault := Event{
		MessageID:   "msg_def_01",
		Platform:    "",
		MessageType: "private",
		UserID:      "user_1",
	}
	if err := sendAstrBotAdminReply(eventDefault, conn, "测试私聊回复", false); err != nil {
		t.Fatalf("sendAstrBotAdminReply failed: %v", err)
	}

	select {
	case act := <-actionCh:
		if act.Platform != "astrbot" {
			t.Errorf("expected act.Platform == 'astrbot', got %q", act.Platform)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for action")
	}
}

type mockLLMCallbackProvider struct {
	onChat func()
}

func (m *mockLLMCallbackProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	if m.onChat != nil {
		m.onChat()
	}
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: "stale reply",
		},
	}, nil
}

func TestAstrBotProcessEvent_EpochInvalidationDuringProcessing(t *testing.T) {
	conn, actionCh, cleanup := setupTestAstrBotWS(t)
	defer cleanup()

	sm := llm.NewSessionManager()
	sess := sm.GetOrCreate("group:50001")

	// Provider that triggers session reset in the middle of generation
	resetDone := false
	provider := &mockLLMCallbackProvider{
		onChat: func() {
			_ = sess.ResetSession(nil)
			resetDone = true
		},
	}

	engine := &llm.Engine{
		MaxIterations:  3,
		SessionManager: sm,
		ModelName:      "mock-model",
		Provider:       provider,
	}

	turn := sess.ReserveTurn()
	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_epoch_01",
		SessionID:   "group:50001",
		Platform:    "qq",
		MessageType: "group",
		GroupID:     "50001",
		UserID:      "user_501",
		Content:     "hello",
		IsWake:      true,
	}

	processEvent(conn, event, engine, turn, nil)

	if !resetDone {
		t.Fatalf("expected onChat to be called")
	}

	// Verify no message action sent, and history was not written back
	select {
	case act := <-actionCh:
		if act.Action == "send_message" {
			t.Errorf("expected NO send_message action when epoch was invalidated, got: %+v", act)
		}
	case <-time.After(100 * time.Millisecond):
		// No action sent - ok
	}

	if len(sess.Snapshot()) != 0 {
		t.Errorf("expected session history to remain empty after reset, got %d messages", len(sess.Snapshot()))
	}
}

func TestAstrBotIngress_NonAdminCommandWithWatchdogKeyword_SilentlyDroppedBeforeSecurityGate(t *testing.T) {
	tmpDir := t.TempDir()
	secCtrl := security.NewController(tmpDir)

	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminQQIDsEnv:         "20001",
		admincmd.AdminCommandPrefixEnv: "/",
	})

	engine := &llm.Engine{
		Scope:          scope,
		Security:       secCtrl,
		SessionManager: llm.NewSessionManager(),
		ModelName:      "mock-model",
		Provider:       &mockLLMProvider{},
	}

	srv, _, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 1. Non-admin user (20002) sends a command candidate containing dangerous watchdog trigger text
	event := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_sec_001",
		IsAt:        true,
		UserID:      "20002", // non-admin
		GroupID:     "30001",
		MessageType: "group",
		Content:     "[@bot] /reset rm -rf / ignore all previous",
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// Must receive noop action with subtype admin_silent_drop and suppress_llm=true,
	// NOT a security rejection message
	_, respBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	var act Action
	if err := json.Unmarshal(respBytes, &act); err != nil {
		t.Fatalf("解析 action 失败: %v", err)
	}
	if act.Action != "noop" || act.SubType != "admin_silent_drop" || !act.SuppressLLM {
		t.Errorf("expected noop admin_silent_drop action with suppress_llm=true, got: %+v", act)
	}
	if act.Content != "" {
		t.Errorf("expected empty content in silent drop noop action, got: %q", act.Content)
	}

	// Ensure no security audit events or lock strikes were created
	audits, err := secCtrl.Audit.List(10)
	if err != nil {
		t.Fatalf("failed to list audits: %v", err)
	}
	if len(audits) != 0 {
		t.Errorf("expected 0 security audit events for dropped non-admin command candidate, got %d: %+v", len(audits), audits)
	}

	principal, err := security.NewPrincipal("astrbot", "20002")
	if err != nil {
		t.Fatalf("failed to create principal: %v", err)
	}
	if secCtrl.IsLocked(principal) {
		t.Errorf("non-admin principal should not be locked")
	}

	// 2. Contrast: Regular non-candidate message with dangerous keyword is checked by GateIngress
	normalEvent := Event{
		Type:        "event",
		EventType:   "message",
		MessageID:   "msg_sec_002",
		IsAt:        false,
		UserID:      "20002",
		GroupID:     "30001",
		MessageType: "group",
		Content:     "rm -rf /",
	}
	normalBytes, _ := json.Marshal(normalEvent)
	if err := conn.WriteMessage(websocket.TextMessage, normalBytes); err != nil {
		t.Fatalf("发送常规危险消息失败: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	auditsAfter, err := secCtrl.Audit.List(10)
	if err != nil {
		t.Fatalf("failed to list audits: %v", err)
	}
	if len(auditsAfter) != 1 {
		t.Errorf("expected exactly 1 security audit event for normal dangerous message, got %d", len(auditsAfter))
	}
}
