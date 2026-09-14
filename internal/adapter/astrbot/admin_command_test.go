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
