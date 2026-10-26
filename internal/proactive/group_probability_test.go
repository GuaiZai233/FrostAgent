package proactive

import "testing"

func TestParseGroupProbability(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"", 0.01}, {"bad", 0.01}, {"0.3tail", 0.01}, {"NaN", 0.01},
		{"Infinity", 0.01}, {"-Infinity", 0.01}, {"1e999", 0.01}, {"0x1", 0.01},
		{"-0.5", 0.01}, {"0", 0}, {"-0", 0}, {"0.001", 0.001},
		{"0.1234", 0.1234}, {" 0.25 ", 0.25}, {"+.25", 0.25}, {"2e-1", 0.2},
		{"1", 1}, {"1.5", 1}, {"100", 1},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := ParseGroupProbability(tc.raw); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseGroupProbabilities(t *testing.T) {
	values, err := ParseGroupProbabilities(`{"synthetic-group":0.25,"Telegram:synthetic-group":0.38,"qq:zero":0,"discord:bad":"bad","onebot:negative":-1,"qq:large":2,"qq:null":null,"qq:bool":true,"qq:object":{},"qq:array":[0.8]}`)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]float64{
		"qq:synthetic-group": 0.25, "telegram:synthetic-group": 0.38, "qq:zero": 0,
		"discord:bad": 0.01, "qq:negative": 0.01, "qq:large": 1,
		"qq:null": 0.01, "qq:bool": 0.01, "qq:object": 0.01, "qq:array": 0.01,
	} {
		if got, ok := values[key]; !ok || got != want {
			t.Fatalf("%s = %v (exists=%v), want %v", key, got, ok, want)
		}
	}
	for _, raw := range []string{"broken", "[]", "null", "0", `{"":0.2}`, `{"qq:":0.2}`, `{"a b":0.2}`, `{"a\u00a0b":0.2}`, `{"synthetic":0.2,"OneBot:synthetic":0.3}`} {
		if _, err := ParseGroupProbabilities(raw); err == nil {
			t.Fatalf("accepted malformed configuration %q", raw)
		}
	}
}

func TestGroupProbabilityOverridesAndPlatformIsolation(t *testing.T) {
	env := map[string]string{
		EnvProbability:        "0.5",
		EnvGroupProbabilities: `{"synthetic-group":0.25,"telegram:synthetic-group":0.38,"discord:room:synthetic":0.75,"qq:zero":0}`,
	}
	getenv := func(key string) string { return env[key] }
	for _, tc := range []struct {
		id, platform string
		want         float64
	}{
		{"synthetic-group", "onebot", 0.25}, {"synthetic-group", "QQ", 0.25},
		{"synthetic-group", "aiocqhttp", 0.25}, {"qq:synthetic-group", "", 0.25},
		{"synthetic-group", "telegram", 0.38}, {"synthetic-group", "discord", 0.5},
		{"room:synthetic", "discord", 0.75}, {"telegram:synthetic-group", "discord", 0.5},
		{"telegram:synthetic-group", "qq", 0.5}, {"missing", "qq", 0.5}, {"zero", "qq", 0},
	} {
		if got := GetGroupProbability(getenv, tc.id, tc.platform); got != tc.want {
			t.Fatalf("%s/%s = %v, want %v", tc.platform, tc.id, got, tc.want)
		}
	}
	if !RollGroupWithRand(getenv, "synthetic-group", func() float64 { return 0.24 }, "onebot") ||
		RollGroupWithRand(getenv, "synthetic-group", func() float64 { return 0.25 }, "onebot") {
		t.Fatal("roll must use the group override, including the exclusive boundary")
	}
	if RollGroupWithRand(getenv, "zero", func() float64 { t.Fatal("zero must not roll"); return 0 }, "qq") {
		t.Fatal("zero-probability group triggered")
	}
	env[EnvWhitelistEnabled], env[EnvGroupWhitelist] = "true", "telegram:synthetic-group"
	if RollGroupWithRand(getenv, "synthetic-group", func() float64 { t.Fatal("denied group must not roll"); return 0 }, "qq") {
		t.Fatal("override bypassed whitelist")
	}
	if !RollGroupWithRand(getenv, "synthetic-group", func() float64 { return 0.3 }, "telegram") {
		t.Fatal("allowed platform must use its override")
	}
	env[EnvEnabled] = "false"
	if GetGroupProbability(getenv, "synthetic-group", "telegram") != 0 {
		t.Fatal("override bypassed master switch")
	}
	env[EnvEnabled], env[EnvProbability] = "true", "0"
	if GetGroupProbability(getenv, "synthetic-group", "telegram") != 0 {
		t.Fatal("override bypassed legacy zero probability")
	}
	env[EnvProbability] = ""
	if got := GetGroupProbability(getenv, "synthetic-group", "telegram"); got != 0.38 {
		t.Fatalf("explicit enable with default probability lost override: %v", got)
	}
	env[EnvGroupProbabilities] = "broken"
	if got := GetGroupProbability(getenv, "synthetic-group", "telegram"); got != 0.01 {
		t.Fatalf("invalid container must preserve global fallback: %v", got)
	}
	if GetGroupProbability(nil, "synthetic-group") != 0 {
		t.Fatal("nil scope must be disabled")
	}
}
