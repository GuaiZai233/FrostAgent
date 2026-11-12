package botloop

import "testing"

func TestParseAndMatch(t *testing.T) {
	values := map[string]string{
		"BOT_LOOP_USER_IDS":  "bot-any, AiOcQhTtP:bot-qq; Telegram:CaseID",
		"BOT_LOOP_MAX_TURNS": "2",
		"BOT_LOOP_MODE":      "hard",
	}
	cfg := Parse(func(key string) string { return values[key] })
	if !cfg.Hard() || cfg.Limit() != 2 {
		t.Fatalf("unexpected config: hard=%v limit=%d", cfg.Hard(), cfg.Limit())
	}
	for _, tc := range []struct {
		platform, userID string
		want             bool
	}{
		{"onebot", "bot-any", true},
		{"telegram", "bot-any", true},
		{"aiocqhttp", "bot-qq", true},
		{"telegram", "bot-qq", false},
		{"telegram", "CaseID", true},
		{"telegram", "caseid", false},
	} {
		if got := cfg.IsMarked(tc.platform, tc.userID); got != tc.want {
			t.Errorf("IsMarked(%q, %q) = %v, want %v", tc.platform, tc.userID, got, tc.want)
		}
	}
	values["BOT_LOOP_MODE"] = "soft"
	if cfg.Revision() == Parse(func(key string) string { return values[key] }).Revision() {
		t.Fatal("configuration revision did not change")
	}
}

func TestParseDefaults(t *testing.T) {
	cfg := Parse(nil)
	if cfg.Hard() || cfg.Limit() != 3 || cfg.IsMarked("onebot", "bot") {
		t.Fatalf("unexpected default config: %+v", cfg)
	}
}

func TestExplicitDisablePreservesLegacyDefault(t *testing.T) {
	values := map[string]string{"BOT_LOOP_USER_IDS": "fixture-bot", "BOT_LOOP_MAX_TURNS": "2", "BOT_LOOP_MODE": "hard"}
	getenv := func(key string) string { return values[key] }
	legacy := Parse(getenv)
	if !legacy.Enabled() || !legacy.IsMarked("onebot", "fixture-bot") {
		t.Fatal("unset switch must preserve legacy marks")
	}
	values["BOT_LOOP_ENABLED"] = "false"
	disabled := Parse(getenv)
	if disabled.Enabled() || disabled.IsMarked("onebot", "fixture-bot") || legacy.Revision() == disabled.Revision() {
		t.Fatal("explicit disable did not stop/reset limiter")
	}
	values["BOT_LOOP_ENABLED"] = "true"
	enabled := Parse(getenv)
	if !enabled.Enabled() || !enabled.IsMarked("onebot", "fixture-bot") || enabled.Limit() != 2 || !enabled.Hard() {
		t.Fatal("re-enable did not recover settings")
	}
}
