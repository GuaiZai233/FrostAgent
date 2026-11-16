package proactive

import (
	"testing"
	"time"
)

func TestAwarenessIntervalAndIndependentDeadlines(t *testing.T) {
	for _, invalid := range []string{"30s - 2h", "0m - 1h", "2h - 30m", "1h", "NaNm - 1h", "1m - 999999999h", "-1m - 2h", "1m - +2h"} {
		if _, _, err := ParseAwarenessInterval(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	lo, hi, err := ParseAwarenessInterval(" 30m - 2h ")
	if err != nil || lo != 30*time.Minute || hi != 2*time.Hour {
		t.Fatalf("bad bounds: %v %v %v", lo, hi, err)
	}
	now := time.Now()
	clock := AwarenessClock{Random: func() float64 { return 0 }}
	if got := clock.Due(now, "30m - 2h", []string{"private:fixture", "group:fixture"}); len(got) != 0 {
		t.Fatal(got)
	}
	// Polling and ordinary conversation observations cannot postpone a deadline.
	clock.Due(now.Add(20*time.Minute), "30m - 2h", []string{"private:fixture", "group:fixture"})
	if got := clock.Due(now.Add(30*time.Minute-time.Nanosecond), "30m - 2h", []string{"private:fixture", "group:fixture"}); len(got) != 0 {
		t.Fatal("fired early")
	}
	if got := clock.Due(now.Add(30*time.Minute), "30m - 2h", []string{"private:fixture", "group:fixture"}); len(got) != 2 {
		t.Fatalf("lost independent deadlines: %v", got)
	}
	// Neither interval edits nor temporarily removing a target can overlap work.
	clock.Due(now.Add(time.Hour), "1m - 1m", nil)
	clock.Due(now.Add(time.Hour), "1m - 1m", []string{"private:fixture"})
	if got := clock.Due(now.Add(2*time.Hour), "1m - 1m", []string{"private:fixture"}); len(got) != 0 {
		t.Fatalf("overlapped in-flight turn: %v", got)
	}
	clock.Done("private:fixture")
	if got := clock.Due(now.Add(2*time.Hour), "1m - 1m", []string{"private:fixture"}); len(got) != 1 {
		t.Fatalf("did not resume: %v", got)
	}
}

func TestAwarenessWhitelistScopesAndMessages(t *testing.T) {
	values := map[string]string{AwarenessEnabled: "true", AwarenessPrivateWhitelistEnabled: "true", AwarenessGroupWhitelistEnabled: "true",
		AwarenessPrivateWhitelist: "fixture-user,telegram:fixture-user", AwarenessGroupWhitelist: "fixture-group"}
	get := func(k string) string { return values[k] }
	for _, tc := range []struct {
		platform, kind, id string
		allowed            bool
	}{
		{"qq", "private", "fixture-user", true}, {"onebot", "private", "fixture-user", true},
		{"telegram", "private", "fixture-user", true}, {"discord", "private", "fixture-user", false},
		{"qq", "group", "fixture-user", false}, {"qq", "private", "fixture-group", false},
		{"qq", "group", "fixture-group", true}, {"telegram", "group", "fixture-group", false},
	} {
		if got := AwarenessAllowed(get, tc.platform, tc.kind, tc.id); got != tc.allowed {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
	values[AwarenessGroupWhitelist] = ""
	if AwarenessAllowed(get, "qq", "group", "fixture-group") {
		t.Fatal("enabled empty whitelist must deny")
	}
	for _, args := range []string{
		`{"session":"qq:private:other","messages":[{"type":"plain","text":"hi"}]}`,
		`{"messages":[{"type":"image","url":"https://example.invalid/image"}]}`,
		`{"messages":[{"type":"quote","message_id":"fixture-message"},{"type":"plain","text":"hi"}]}`,
		`{"messages":[{"type":"mention_user","mention_user_id":"fixture-member"}]}`,
	} {
		if AwarenessSendAllowed(args) {
			t.Fatalf("unsafe send accepted: %s", args)
		}
	}
	if !AwarenessSendAllowed(`{"messages":[{"type":"mention_user","mention_user_id":"fixture-member"},{"type":"plain","text":" hi"}]}`) {
		t.Fatal("valid mention rejected")
	}
}
