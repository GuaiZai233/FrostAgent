package silentignore

import "testing"

func env(value string) func(string) string {
	return func(key string) string {
		if key == EnvKey {
			return value
		}
		return ""
	}
}

func TestParseAndIgnored(t *testing.T) {
	cfg := Parse(env(" 1001, onebot:1002;\nTelegram:1003 "))
	cases := []struct {
		platform, id string
		want         bool
	}{
		{"onebot", "1001", true},
		{"telegram", "1001", true},
		{"onebot", "1002", true},
		{"telegram", "1002", false},
		{"telegram", "1003", true},
		{"onebot", "1004", false},
		{"onebot", "", false},
	}
	for _, c := range cases {
		if got := cfg.Ignored(c.platform, c.id); got != c.want {
			t.Errorf("Ignored(%q,%q)=%v want %v", c.platform, c.id, got, c.want)
		}
	}
}

func TestEmptyAndNilGetenv(t *testing.T) {
	if Parse(env("")).Ignored("onebot", "1001") || Parse(nil).Ignored("onebot", "1001") {
		t.Fatal("empty config must ignore nobody")
	}
}
