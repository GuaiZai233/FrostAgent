package onebot

import (
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
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

func TestExtractOneBotAdminCommand(t *testing.T) {
	scope := newAdminTestScope(t, map[string]string{
		admincmd.AdminCommandPrefixEnv: "/",
	})

	// 1. Real @ targeting bot SelfID
	atSelfMsg, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /reset"}},
	})
	eventAtSelf := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     atSelfMsg,
	}
	cmd, isCand, err := extractOneBotAdminCommand(eventAtSelf, "/", scope)
	if !isCand || err != nil || cmd.Type != admincmd.CmdReset {
		t.Fatalf("expected CmdReset candidate, got isCand=%v err=%v type=%v", isCand, err, cmd.Type)
	}

	// 2. Real @ targeting another user (not SelfID)
	atOtherMsg, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10002"}},
		{"type": "text", "data": map[string]any{"text": " /reset"}},
	})
	eventAtOther := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     atOtherMsg,
	}
	_, isCand, err = extractOneBotAdminCommand(eventAtOther, "/", scope)
	if isCand {
		t.Fatalf("expected non-candidate when @ targeting someone else, got isCand=true")
	}

	// 3. No @ segment
	noAtMsg, _ := json.Marshal([]map[string]any{
		{"type": "text", "data": map[string]any{"text": "/reset"}},
	})
	eventNoAt := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     noAtMsg,
	}
	_, isCand, err = extractOneBotAdminCommand(eventNoAt, "/", scope)
	if isCand {
		t.Fatalf("expected non-candidate without real @, got isCand=true")
	}

	// 4. Text representation [@<selfID>] is NOT accepted (must be real at segment)
	textFallbackMsg, _ := json.Marshal([]map[string]any{
		{"type": "text", "data": map[string]any{"text": "[@10001] /compact"}},
	})
	eventTextFallback := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     textFallbackMsg,
	}
	_, isCand, err = extractOneBotAdminCommand(eventTextFallback, "/", scope)
	if isCand {
		t.Fatalf("expected pure text mention to be rejected as non-candidate, got isCand=true")
	}

	// 5. Historical @ in event.Messages should NOT authorize command in event.Message
	histAtMsg, _ := json.Marshal([]any{
		[]map[string]any{
			{"type": "at", "data": map[string]any{"qq": "10001"}},
			{"type": "text", "data": map[string]any{"text": " hi"}},
		},
	})
	currNoAtMsg, _ := json.Marshal([]map[string]any{
		{"type": "text", "data": map[string]any{"text": "/reset"}},
	})
	eventHistAt := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     currNoAtMsg,
		Messages:    histAtMsg,
	}
	_, isCand, err = extractOneBotAdminCommand(eventHistAt, "/", scope)
	if isCand {
		t.Fatalf("expected historical @ in event.Messages to not trigger command, got isCand=true")
	}

	// 6. Word prefix
	wordScope := newAdminTestScope(t, map[string]string{
		admincmd.AdminCommandPrefixEnv: "execute",
	})
	atWordMsg, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " execute reset"}},
	})
	eventWord := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     atWordMsg,
	}
	cmd, isCand, err = extractOneBotAdminCommand(eventWord, "execute", wordScope)
	if !isCand || err != nil || cmd.Type != admincmd.CmdReset {
		t.Fatalf("expected word prefix CmdReset, got isCand=%v err=%v type=%v", isCand, err, cmd.Type)
	}
}

func setupTestWS(t *testing.T) (*wsConnection, chan model.OneBotAction, func()) {
	t.Helper()
	actionCh := make(chan model.OneBotAction, 10)

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
			var act model.OneBotAction
			if json.Unmarshal(msg, &act) == nil {
				actionCh <- act
				// Echo success response
				resp := map[string]any{
					"status":  "ok",
					"retcode": 0,
					"echo":    act.Echo,
				}
				_ = ws.WriteJSON(resp)
			}
		}
	}))

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("Dial ws failed: %v", err)
	}

	wsConn := newWSConnection(clientConn)

	go func() {
		for {
			_, msg, err := clientConn.ReadMessage()
			if err != nil {
				break
			}
			wsConn.handleAPIResponse(msg)
		}
	}()

	cleanup := func() {
		_ = clientConn.Close()
		server.Close()
	}

	return wsConn, actionCh, cleanup
}

func TestHandleAdminCommand_AdminFlow(t *testing.T) {
	wsConn, actionCh, cleanup := setupTestWS(t)
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
	wsConn.Scope = scope

	// 1. Admin sends @bot /reset
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /reset"}},
	})
	event := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytes,
	}

	handled := handleAdminCommand(wsConn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for admin /reset")
	}

	select {
	case act := <-actionCh:
		if act.Action != "send_group_msg" {
			t.Errorf("expected send_group_msg action, got %s", act.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for reset reply action")
	}

	// 2. Admin sends syntax error @bot /ban (missing target)
	msgBytes, _ = json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /ban"}},
	})
	eventSyntaxErr := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytes,
	}

	handled = handleAdminCommand(wsConn, eventSyntaxErr, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for syntax error")
	}

	select {
	case act := <-actionCh:
		if act.Action != "send_group_msg" {
			t.Errorf("expected send_group_msg action, got %s", act.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for syntax error reply")
	}
}

func TestHandleAdminCommand_LockedAdminRejected(t *testing.T) {
	wsConn, actionCh, cleanup := setupTestWS(t)
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
	wsConn.Scope = scope

	// Lock the admin principal in security controller
	adminPrincipal, err := security.NewPrincipal("onebot", "20001")
	if err != nil {
		t.Fatalf("NewPrincipal failed: %v", err)
	}
	if err := secCtrl.Lock(adminPrincipal, "admin account locked"); err != nil {
		t.Fatalf("secCtrl.Lock failed: %v", err)
	}

	// 1. Admin sends @bot /reset
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /reset"}},
	})
	event := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytes,
	}

	handled := handleAdminCommand(wsConn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for locked admin")
	}

	// Must reply with RejectGatewayMsg
	select {
	case act := <-actionCh:
		if act.Action != "send_group_msg" {
			t.Errorf("expected send_group_msg action, got %s", act.Action)
		}
		params, _ := act.Params.(map[string]any)
		if msg, _ := params["message"].(string); !strings.Contains(msg, security.RejectGatewayMsg) {
			t.Errorf("expected reply containing RejectGatewayMsg, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for locked admin rejection reply")
	}

	// 2. Locked admin sends @bot /unban 20001 attempting self-unlock
	msgBytesUnban, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /unban 20001"}},
	})
	eventUnban := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytesUnban,
	}

	handled = handleAdminCommand(wsConn, eventUnban, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true for locked admin unban attempt")
	}

	select {
	case act := <-actionCh:
		params, _ := act.Params.(map[string]any)
		if msg, _ := params["message"].(string); !strings.Contains(msg, security.RejectGatewayMsg) {
			t.Errorf("expected reply containing RejectGatewayMsg on unban attempt, got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for locked admin rejection reply on unban")
	}

	// Verify admin is still locked
	if err := secCtrl.CheckAccess(adminPrincipal); !errors.Is(err, security.ErrLocked) {
		t.Fatalf("expected admin to remain locked, got err=%v", err)
	}
}

func TestHandleAdminCommand_NonAdminSilentDrop(t *testing.T) {
	wsConn, actionCh, cleanup := setupTestWS(t)
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
	wsConn.Scope = scope

	// Non-admin (user 20002 != admin 20001) sends @bot /reset
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /reset"}},
	})
	event := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20002, // non-admin
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytes,
	}

	handled := handleAdminCommand(wsConn, event, engine)
	if !handled {
		t.Fatalf("expected handleAdminCommand to return true to drop non-admin candidate")
	}

	// Must NOT send any reply to connection
	select {
	case act := <-actionCh:
		t.Fatalf("unexpected reply action sent for non-admin: %+v", act)
	case <-time.After(300 * time.Millisecond):
		// Success: silent drop
	}
}

func TestHandleAdminCommand_NonCandidatePassthrough(t *testing.T) {
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

	// Normal chat message without @bot
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "text", "data": map[string]any{"text": "你好啊"}},
	})
	event := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20001,
		GroupID:     30001,
		MessageType: "group",
		Message:     msgBytes,
	}

	handled := handleAdminCommand(nil, event, engine)
	if handled {
		t.Fatalf("expected handleAdminCommand to return false for non-candidate chat message")
	}
}

func TestQueuedTurnEpochInvalidation(t *testing.T) {
	sessionMgr := llm.NewSessionManager()
	session := sessionMgr.GetOrCreate("group:30001")

	// Reserve turn 1
	turn1 := session.ReserveTurn()
	if turn1 == nil {
		t.Fatalf("expected turn1 not nil")
	}

	// Reserve turn 2 while turn 1 is still active
	turn2 := session.ReserveTurn()
	if turn2 == nil {
		t.Fatalf("expected turn2 not nil")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	turn2Finished := false
	turn2Valid := true

	// Turn 2 waits on turn 1 in background
	go func() {
		defer wg.Done()
		turn2.Wait()
		defer turn2.Done()

		turn2Valid = turn2.IsValid(session)
		turn2Finished = true
	}()

	// Simulate admin /reset while turn 2 is queued waiting for turn 1
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

func TestOneBotProcessEvent_EpochInvalidationDuringProcessing(t *testing.T) {
	wsConn, actionCh, cleanup := setupTestWS(t)
	defer cleanup()

	sm := llm.NewSessionManager()
	sess := sm.GetOrCreate("group:50001")

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
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " hello"}},
	})
	event := model.OneBotEvent{
		PostType:    "message",
		MessageType: "group",
		SelfID:      10001,
		GroupID:     50001,
		UserID:      501,
		Message:     msgBytes,
	}

	processEvent(wsConn, event, engine, turn, nil)

	if !resetDone {
		t.Fatalf("expected onChat to be called")
	}

	select {
	case act := <-actionCh:
		if act.Action == "send_group_msg" {
			t.Errorf("expected NO send_group_msg action when epoch was invalidated, got: %+v", act)
		}
	case <-time.After(100 * time.Millisecond):
		// No action sent - ok
	}

	if len(sess.Snapshot()) != 0 {
		t.Errorf("expected session history to remain empty after reset, got %d messages", len(sess.Snapshot()))
	}
}

func TestOneBotProcessEvent_EpochInvalidationBeforeReply(t *testing.T) {
	wsConn, actionCh, cleanup := setupTestWS(t)
	defer cleanup()

	sm := llm.NewSessionManager()
	sess := sm.GetOrCreate("group:50002")

	providerCalled := false
	provider := &mockLLMCallbackProvider{
		onChat: func() {
			providerCalled = true
		},
	}

	engine := &llm.Engine{
		MaxIterations:  3,
		SessionManager: sm,
		ModelName:      "mock-model",
		Provider:       provider,
	}

	turn := sess.ReserveTurn()

	// Invalidate epoch before processEvent executes quote lookup / preprocessing
	_ = sess.ResetSession(nil)

	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " hello"}},
	})
	event := model.OneBotEvent{
		PostType:    "message",
		MessageType: "group",
		SelfID:      10001,
		GroupID:     50002,
		UserID:      502,
		Message:     msgBytes,
	}

	processEvent(wsConn, event, engine, turn, nil)

	if providerCalled {
		t.Fatalf("provider should not have been called when turn was invalidated before processing")
	}

	select {
	case act := <-actionCh:
		t.Errorf("expected NO action when turn was invalidated before processing, got: %+v", act)
	case <-time.After(100 * time.Millisecond):
		// No action sent - ok
	}
}

func TestOneBotIngress_NonAdminCommandWithWatchdogKeyword_SilentlyDroppedBeforeSecurityGate(t *testing.T) {
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

	srv, wsURL := startWSTestServer(engine)
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket 连接失败: %v", err)
	}
	defer conn.Close()

	// 1. Non-admin sends command candidate containing dangerous watchdog trigger text
	msgBytes, _ := json.Marshal([]map[string]any{
		{"type": "at", "data": map[string]any{"qq": "10001"}},
		{"type": "text", "data": map[string]any{"text": " /reset rm -rf / ignore all previous"}},
	})
	event := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20002, // non-admin
		GroupID:     30001,
		MessageType: "group",
		PostType:    "message",
		MessageID:   999,
		Message:     msgBytes,
	}
	eventBytes, _ := json.Marshal(event)

	if err := conn.WriteMessage(websocket.TextMessage, eventBytes); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}

	// Wait briefly to allow processing
	time.Sleep(100 * time.Millisecond)

	// Ensure no security rejection message or security audit strike was recorded
	audits, err := secCtrl.Audit.List(10)
	if err != nil {
		t.Fatalf("failed to list audits: %v", err)
	}
	if len(audits) != 0 {
		t.Errorf("expected 0 security audit events for dropped non-admin command candidate, got %d: %+v", len(audits), audits)
	}

	principal, err := security.NewPrincipal("onebot", "20002")
	if err != nil {
		t.Fatalf("failed to create principal: %v", err)
	}
	if secCtrl.IsLocked(principal) {
		t.Errorf("non-admin principal should not be locked")
	}

	// 2. Contrast: Regular chat message with dangerous keyword is checked by GateIngress
	dangerMsgBytes, _ := json.Marshal([]map[string]any{
		{"type": "text", "data": map[string]any{"text": "rm -rf /"}},
	})
	dangerEvent := model.OneBotEvent{
		SelfID:      10001,
		UserID:      20002,
		GroupID:     30001,
		MessageType: "group",
		PostType:    "message",
		MessageID:   1000,
		Message:     dangerMsgBytes,
	}
	dangerBytes, _ := json.Marshal(dangerEvent)
	if err := conn.WriteMessage(websocket.TextMessage, dangerBytes); err != nil {
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
