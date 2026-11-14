package llm

import (
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
)

func TestGroupCompactAllowed(t *testing.T) {
	const enable = EnvGroupCompactWhitelistEnabled
	const list = EnvGroupCompactWhitelist
	cases := []struct {
		name string
		env  map[string]string
		conv string
		want bool
	}{
		{"unset switch and empty list allows every group", map[string]string{}, "group:123", true},
		{"explicit false allows every group", map[string]string{enable: "false", list: "999"}, "group:123", true},
		{"enabled allows a listed bare QQ id", map[string]string{enable: "true", list: "123, 456"}, "group:123", true},
		{"enabled rejects an unlisted group", map[string]string{enable: "true", list: "123"}, "group:789", false},
		{"onebot-prefixed session matches the bare QQ id", map[string]string{enable: "TRUE", list: "123"}, "onebot:group:123", true},
		{"retired session does not match the bare QQ id", map[string]string{enable: "true", list: "123"}, "aiocqhttp:group:123", false},
		{"retired whitelist entry does not match OneBot", map[string]string{enable: "true", list: "aiocqhttp:123"}, "onebot:group:123", false},
		{"telegram group needs a platform-qualified entry", map[string]string{enable: "true", list: "123"}, "telegram:group:123", false},
		{"telegram group matches its platform entry", map[string]string{enable: "true", list: "telegram:123"}, "telegram:group:123", true},
		{"enabled with an empty list rejects every group", map[string]string{enable: "true"}, "group:123", false},
		{"unset switch with a non-empty list implies enabled", map[string]string{list: "123"}, "group:456", false},
		{"private conversations are not governed by the group whitelist", map[string]string{enable: "true"}, "private:1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			if got := GroupCompactAllowed(getenv, tc.conv); got != tc.want {
				t.Fatalf("GroupCompactAllowed(%q) = %v, want %v", tc.conv, got, tc.want)
			}
		})
	}
}

func TestGroupIdentityFromConversationID(t *testing.T) {
	cases := []struct {
		conv     string
		identity string
		isGroup  bool
	}{
		{"group:123", "123", true},
		{"onebot:group:123", "123", true},
		{"QQ:group:9", "9", true},
		{"discord:group:chan", "discord:chan", true},
		{"private:1", "", false},
	}
	for _, tc := range cases {
		identity, isGroup := groupIdentityFromConversationID(tc.conv)
		if identity != tc.identity || isGroup != tc.isGroup {
			t.Errorf("groupIdentityFromConversationID(%q) = (%q, %v), want (%q, %v)",
				tc.conv, identity, isGroup, tc.identity, tc.isGroup)
		}
	}
}

func TestGroupCompactorAutoTriggerSkipsNonWhitelistedGroup(t *testing.T) {
	t.Setenv(EnvGroupCompactWhitelistEnabled, "true")
	t.Setenv(EnvGroupCompactWhitelist, "111")

	llm := &mockCompactorLLM{}
	compactor := NewGroupCompactor(llm, nil, "mock-model", 1, time.Second)
	// A nil scope resolves env from the process environment, which the test controls.
	compactor.Scope = nil

	session := &SessionContext{ConversationID: "group:222"}
	session.AppendGroupCompactMessage("msg", 10)

	compactor.TriggerWithScope(session, "owner", modelrouter.Scope{})

	time.Sleep(30 * time.Millisecond)
	if n := llm.CallCount(); n != 0 {
		t.Fatalf("automatic compaction called the model %d times for a non-whitelisted group", n)
	}
	if summary := session.GroupRunningSummary(); summary != "" {
		t.Fatalf("expected no running summary for a non-whitelisted group, got %q", summary)
	}
}

func TestWithoutCompactSummaryKeepsRecentMessages(t *testing.T) {
	in := GroupContextSnapshot{
		RunningSummary:           "old summary",
		RecentMessages:           []string{"User1: hi"},
		RecentStructuredMessages: []GroupCompactMessage{{Role: "user", Content: "hi"}},
		SummaryGroups:            []SummaryGroup{{Summary: "batch"}},
	}
	snap := WithoutCompactSummary(in)
	if !snap.CompactDisabled || snap.RunningSummary != "" || len(snap.SummaryGroups) != 0 {
		t.Fatalf("summary must be stripped and flagged: %+v", snap)
	}
	if len(snap.RecentMessages) != 1 || len(snap.RecentStructuredMessages) != 1 {
		t.Fatalf("uncompacted recent messages must be kept: %+v", snap)
	}
}
