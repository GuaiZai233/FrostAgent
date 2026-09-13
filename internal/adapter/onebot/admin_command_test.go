package onebot

import (
	"FrostAgent/internal/admincmd"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/model"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
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

	// 4. Text representation fallback [@<selfID>]
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
	cmd, isCand, err = extractOneBotAdminCommand(eventTextFallback, "/", scope)
	if !isCand || err != nil || cmd.Type != admincmd.CmdCompact {
		t.Fatalf("expected CmdCompact candidate for text fallback, got isCand=%v err=%v type=%v", isCand, err, cmd.Type)
	}

	// 5. Word prefix
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
