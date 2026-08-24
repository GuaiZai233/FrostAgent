package botstatus

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/llm"

	"connectrpc.com/connect"
)

func TestDerivePlatform(t *testing.T) {
	tests := []struct {
		sessionID string
		want      string
	}{
		{"astrbot:group:123", "astrbot"},
		{"onebot:group:123", "onebot"},
		{"qq:group:123", "onebot"},
		{"QQ:group:123", "onebot"},
		{"aiocqhttp:group:100001", "aiocqhttp"},
		{"AIOCQHTTP:group:100001", "aiocqhttp"},
		{"telegram:group:999", "telegram"},
		{"group:12345", "onebot"},
		{"GROUP:12345", "onebot"},
		{"private:12345", "onebot"},
		{"discord_group_123", "discord"},
		{"telegram_123", "telegram"},
		{"some_random_id", "unknown"},
		{"invalid", "unknown"},
	}

	for _, tt := range tests {
		got := derivePlatform(tt.sessionID)
		if got != tt.want {
			t.Errorf("derivePlatform(%q) = %q, want %q", tt.sessionID, got, tt.want)
		}
	}
}

func TestIsGroupSession(t *testing.T) {
	tests := []struct {
		sessionID string
		want      bool
	}{
		{"group:12345", true},
		{"GROUP:12345", true},
		{"aiocqhttp:group:100001", true},
		{"AIOCQHTTP:GROUP:100001", true},
		{"astrbot:group:12345", true},
		{"AstrBot:Group:12345", true},
		{"onebot:group:12345", true},
		{"private:12345", false},
		{"PRIVATE:12345", false},
		{"astrbot:private:12345", false},
		{"onebot:private:12345", false},
		{"aiocqhttp:private:12345", false},
		{"some_random_id", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isGroupSession(tt.sessionID)
		if got != tt.want {
			t.Errorf("isGroupSession(%q) = %v, want %v", tt.sessionID, got, tt.want)
		}
	}
}

func TestGetSessionsAndGroupSummary(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "group_summaries.json")
	store, err := groupsummary.NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create group summary store: %v", err)
	}

	sessionManager := llm.NewSessionManager()
	sessionManager.SetGroupSummaryStore(store)

	engine := &llm.Engine{
		SessionManager:    sessionManager,
		GroupSummaryStore: store,
		StartedAt:         time.Now(),
		ModelName:         "test-model",
	}

	svc := New(engine, "test-v1")

	// 1. Add active session in sessionManager
	activeSession := sessionManager.GetOrCreate("aiocqhttp:group:100001")
	activeSession.AppendGroupCompactMessage("User (123): hello", 20)

	// 2. Add durable group summary
	if _, err := store.Upsert("astrbot:group:999999", "persisted summary for astrbot", 0); err != nil {
		t.Fatalf("failed to upsert summary: %v", err)
	}

	// 3. Call GetSessions
	resp, err := svc.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil {
		t.Fatalf("GetSessions failed: %v", err)
	}

	if len(resp.Msg.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(resp.Msg.Sessions))
	}

	sessionMap := make(map[string]*v1.SessionInfo)
	for _, s := range resp.Msg.Sessions {
		sessionMap[s.SessionId] = s
	}

	// Verify aiocqhttp group session
	aiocqhttpSession, ok := sessionMap["aiocqhttp:group:100001"]
	if !ok {
		t.Fatalf("missing aiocqhttp session")
	}
	if aiocqhttpSession.Platform != "aiocqhttp" {
		t.Errorf("expected platform 'aiocqhttp', got %q", aiocqhttpSession.Platform)
	}

	// Verify astrbot persisted group session
	astrbotSession, ok := sessionMap["astrbot:group:999999"]
	if !ok {
		t.Fatalf("missing astrbot session")
	}
	if astrbotSession.Platform != "astrbot" {
		t.Errorf("expected platform 'astrbot', got %q", astrbotSession.Platform)
	}
	if astrbotSession.GroupSummary != "persisted summary for astrbot" {
		t.Errorf("expected group summary 'persisted summary for astrbot', got %q", astrbotSession.GroupSummary)
	}

	// 4. Delete group summary
	delResp, err := svc.DeleteGroupSummary(context.Background(), connect.NewRequest(&v1.DeleteGroupSummaryRequest{
		SessionId: "astrbot:group:999999",
	}))
	if err != nil {
		t.Fatalf("DeleteGroupSummary failed: %v", err)
	}
	if !delResp.Msg.Success {
		t.Errorf("DeleteGroupSummary returned success=false, error: %s", delResp.Msg.Error)
	}

	// Verify it was deleted from store
	_, exists, _ := store.Get("astrbot:group:999999")
	if exists {
		t.Errorf("expected summary for astrbot:group:999999 to be deleted")
	}
}

func TestGetSessionContext(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "group_summaries.json")
	store, err := groupsummary.NewStore(storePath)
	if err != nil {
		t.Fatalf("failed to create group summary store: %v", err)
	}

	sessionManager := llm.NewSessionManager()
	sessionManager.SetGroupSummaryStore(store)

	engine := &llm.Engine{
		SessionManager:    sessionManager,
		GroupSummaryStore: store,
		StartedAt:         time.Now(),
		ModelName:         "test-model",
	}

	svc := New(engine, "test-v1")

	sessionID := "aiohttp:group:100001"
	sess := sessionManager.GetOrCreate(sessionID)
	sess.AppendGroupCompactMessage("[09:30] User1 (101) [msg_1]: 早上好", 20)
	sess.AppendGroupCompactMessage("[09:31] User2 (102) [msg_2]: 今天天气真好", 20)

	snap, ok := sess.SnapshotGroupCompact(2)
	if !ok {
		t.Fatalf("expected SnapshotGroupCompact to succeed")
	}
	sess.CommitGroupCompact(snap, "群友互道早安并讨论天气很好")
	sess.AppendGroupCompactMessage("[09:35] User3 (103) [msg_3]: 中午去吃什么？", 20)

	resp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{
		SessionId:   sessionID,
		RecentLimit: 20,
	}))
	if err != nil {
		t.Fatalf("GetSessionContext failed: %v", err)
	}

	if resp.Msg.SessionId != sessionID {
		t.Errorf("expected session_id %s, got %s", sessionID, resp.Msg.SessionId)
	}
	if resp.Msg.RunningSummary != "群友互道早安并讨论天气很好" {
		t.Errorf("expected running_summary %q, got %q", "群友互道早安并讨论天气很好", resp.Msg.RunningSummary)
	}
	if len(resp.Msg.SummaryGroups) != 1 {
		t.Fatalf("expected 1 summary group, got %d", len(resp.Msg.SummaryGroups))
	}
	if resp.Msg.SummaryGroups[0].StartMessageId != "msg_1" || resp.Msg.SummaryGroups[0].EndMessageId != "msg_2" {
		t.Errorf("unexpected summary group range: %v -> %v", resp.Msg.SummaryGroups[0].StartMessageId, resp.Msg.SummaryGroups[0].EndMessageId)
	}
	if len(resp.Msg.RecentMessages) != 1 {
		t.Fatalf("expected 1 recent message, got %d", len(resp.Msg.RecentMessages))
	}

	// Before LLM invocation, system_prompt and model should be empty (never fabricated)
	if resp.Msg.SystemPrompt != "" {
		t.Errorf("expected empty system_prompt before request, got %q", resp.Msg.SystemPrompt)
	}
	if resp.Msg.Model != "" {
		t.Errorf("expected empty model before request, got %q", resp.Msg.Model)
	}

	// Simulate dynamic prompt assembly and LLM request trace
	expectedSysPrompt := "You are FrostAgent\nCurrent Time: 2026-08-23 10:00:00\nDialogue few-shots..."
	sess.SetLastPromptTrace(expectedSysPrompt, "gpt-4o-mini")

	respAfter, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{
		SessionId: sessionID,
	}))
	if err != nil {
		t.Fatalf("GetSessionContext after trace failed: %v", err)
	}
	if respAfter.Msg.SystemPrompt != expectedSysPrompt {
		t.Errorf("expected system_prompt %q, got %q", expectedSysPrompt, respAfter.Msg.SystemPrompt)
	}
	if respAfter.Msg.Model != "gpt-4o-mini" {
		t.Errorf("expected model 'gpt-4o-mini', got %q", respAfter.Msg.Model)
	}
}
