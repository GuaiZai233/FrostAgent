package botstatus

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/groupsummary"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
)

// newCompactWhitelistService builds a service whose group summary store holds a summary for
// "group:100001" (live session) and "group:200002" (persisted only). Whitelist env is set by the caller.
func newCompactWhitelistService(t *testing.T) (*Service, *llm.SessionManager, *groupsummary.Store) {
	t.Helper()
	store, err := groupsummary.NewStore(filepath.Join(t.TempDir(), "group_summaries.json"))
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
	for _, id := range []string{"group:100001", "group:200002"} {
		if _, err := store.Upsert(id, "stored summary for "+id, 0); err != nil {
			t.Fatalf("failed to upsert summary: %v", err)
		}
	}
	sessionManager.GetOrCreate("group:100001").AppendGroupCompactMessage("User (1): hi", 20)
	return New(engine, "test-v1", ""), sessionManager, store
}

func summaryOf(t *testing.T, svc *Service, id string) string {
	t.Helper()
	resp, err := svc.GetSessions(context.Background(), connect.NewRequest(&v1.GetSessionsRequest{}))
	if err != nil {
		t.Fatalf("GetSessions failed: %v", err)
	}
	for _, s := range resp.Msg.Sessions {
		if s.SessionId == id {
			return s.GroupSummary
		}
	}
	t.Fatalf("session %s missing from GetSessions", id)
	return ""
}

func TestCompactWhitelistHidesSummariesForNonWhitelistedGroups(t *testing.T) {
	t.Setenv("ENABLE_GROUP_COMPACT_WHITELIST", "true")
	t.Setenv("GROUP_COMPACT_WHITELIST", "999")
	svc, _, _ := newCompactWhitelistService(t)

	// Live session with a persisted summary.
	if got := summaryOf(t, svc, "group:100001"); got != "" {
		t.Errorf("live non-whitelisted session leaked summary %q", got)
	}
	// Persisted-only session.
	if got := summaryOf(t, svc, "group:200002"); got != "" {
		t.Errorf("persisted-only non-whitelisted session leaked summary %q", got)
	}

	for _, id := range []string{"group:100001", "group:200002"} {
		resp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{
			SessionId: id, RecentLimit: 20,
		}))
		if err != nil {
			t.Fatalf("GetSessionContext(%s) failed: %v", id, err)
		}
		if resp.Msg.RunningSummary != "" {
			t.Errorf("GetSessionContext(%s) leaked running summary %q", id, resp.Msg.RunningSummary)
		}
		if !resp.Msg.GroupCompactDisabled {
			t.Errorf("GetSessionContext(%s) must report group_compact_disabled", id)
		}
	}
}

func TestCompactWhitelistKeepsSummaryForWhitelistedGroup(t *testing.T) {
	t.Setenv("ENABLE_GROUP_COMPACT_WHITELIST", "true")
	t.Setenv("GROUP_COMPACT_WHITELIST", "100001")
	svc, _, _ := newCompactWhitelistService(t)

	if got := summaryOf(t, svc, "group:100001"); got != "stored summary for group:100001" {
		t.Errorf("whitelisted session summary = %q", got)
	}
	resp, err := svc.GetSessionContext(context.Background(), connect.NewRequest(&v1.GetSessionContextRequest{
		SessionId: "group:100001", RecentLimit: 20,
	}))
	if err != nil {
		t.Fatalf("GetSessionContext failed: %v", err)
	}
	if resp.Msg.RunningSummary != "stored summary for group:100001" {
		t.Errorf("whitelisted running summary = %q", resp.Msg.RunningSummary)
	}
	if resp.Msg.GroupCompactDisabled {
		t.Errorf("whitelisted group must not be reported as disabled")
	}
}
