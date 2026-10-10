package proactive

import (
	"testing"
)

func TestParseProbability(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"", 0},
		{"0", 0},
		{"-0.5", 0},
		{"invalid", 0},
		{"0.001", 0.01}, // clamped to min
		{"0.01", 0.01},
		{"0.05", 0.05},
		{"0.5", 0.5},
		{"1.00", 1.00},
		{"1.5", 1.00}, // clamped to max
		{" 0.25 ", 0.25},
		{"0.1234", 0.12}, // rounded to 2 decimals
	}

	for _, tc := range tests {
		actual := ParseProbability(tc.input)
		if actual != tc.expected {
			t.Errorf("ParseProbability(%q) = %v, expected %v", tc.input, actual, tc.expected)
		}
	}
}

func TestGetProbability(t *testing.T) {
	mockEnv := func(env map[string]string) func(string) string {
		return func(k string) string {
			return env[k]
		}
	}

	// 1. Unset
	if p := GetProbability(mockEnv(map[string]string{})); p != 0 {
		t.Errorf("expected 0 for unset, got %v", p)
	}

	// 2. Disabled explicitly
	if p := GetProbability(mockEnv(map[string]string{
		EnvEnabled:     "false",
		EnvProbability: "0.5",
	})); p != 0 {
		t.Errorf("expected 0 when explicitly disabled, got %v", p)
	}

	// 3. Enabled without explicit probability -> at least 0.01
	if p := GetProbability(mockEnv(map[string]string{
		EnvEnabled: "true",
	})); p != 0.01 {
		t.Errorf("expected 0.01 when enabled with no prob, got %v", p)
	}

	// 4. Configured probability
	if p := GetProbability(mockEnv(map[string]string{
		EnvProbability: "0.20",
	})); p != 0.20 {
		t.Errorf("expected 0.20, got %v", p)
	}

	// 5. Zero configured
	if p := GetProbability(mockEnv(map[string]string{
		EnvProbability: "0",
	})); p != 0 {
		t.Errorf("expected 0 for 0, got %v", p)
	}
}

func TestRoll(t *testing.T) {
	mockEnv := func(prob string) func(string) string {
		return func(k string) string {
			if k == EnvProbability {
				return prob
			}
			return ""
		}
	}

	// Disabled -> always false
	if RollWithRand(mockEnv("0"), func() float64 { return 0.001 }) {
		t.Errorf("expected false when disabled")
	}

	// 0.20 probability: roll 0.15 hits, roll 0.25 misses
	if !RollWithRand(mockEnv("0.20"), func() float64 { return 0.15 }) {
		t.Errorf("expected hit for 0.15 < 0.20")
	}
	if RollWithRand(mockEnv("0.20"), func() float64 { return 0.25 }) {
		t.Errorf("expected miss for 0.25 >= 0.20")
	}
}

func TestPromptPrefix(t *testing.T) {
	expected := "此为触发主动回复逻辑的消息，如果你认为值得插嘴，请回复；反之，对于你不感兴趣的话题/领域、说了一半的话等，请调用stay_silent工具静默。"
	if PromptPrefix != expected {
		t.Errorf("PromptPrefix mismatch:\ngot:  %q\nwant: %q", PromptPrefix, expected)
	}
}

func TestGroupWhitelist(t *testing.T) {
	mockEnv := func(env map[string]string) func(string) string {
		return func(k string) string {
			return env[k]
		}
	}

	t.Run("ParseGroupWhitelist", func(t *testing.T) {
		res := ParseGroupWhitelist("123456, 34567,9999\n8888;7777")
		expected := []string{"123456", "34567", "9999", "8888", "7777"}
		for _, exp := range expected {
			if _, ok := res[exp]; !ok {
				t.Errorf("expected group %s in parsed whitelist", exp)
			}
		}
		if len(res) != len(expected) {
			t.Errorf("expected %d groups, got %d", len(expected), len(res))
		}

		if empty := ParseGroupWhitelist("   "); empty != nil {
			t.Errorf("expected nil for whitespace, got %v", empty)
		}
	})

	t.Run("IsWhitelistEnabled", func(t *testing.T) {
		// 1. Unset and no groups -> disabled
		if IsWhitelistEnabled(mockEnv(map[string]string{})) {
			t.Errorf("expected disabled when unset")
		}

		// 2. Explicitly true
		if !IsWhitelistEnabled(mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
		})) {
			t.Errorf("expected enabled when set to true")
		}

		// 3. Explicitly false even if groups exist
		if IsWhitelistEnabled(mockEnv(map[string]string{
			EnvWhitelistEnabled: "false",
			EnvGroupWhitelist:    "123456,34567",
		})) {
			t.Errorf("expected disabled when explicitly false")
		}

		// 4. Unset but groups exist -> defaults to enabled
		if !IsWhitelistEnabled(mockEnv(map[string]string{
			EnvGroupWhitelist: "123456",
		})) {
			t.Errorf("expected enabled when groups exist and switch unset")
		}
	})

	t.Run("IsGroupAllowed", func(t *testing.T) {
		// Whitelist disabled -> all groups allowed
		envDisabled := mockEnv(map[string]string{
			EnvWhitelistEnabled: "false",
			EnvGroupWhitelist:    "123456",
		})
		if !IsGroupAllowed(envDisabled, "99999") {
			t.Errorf("expected all groups allowed when whitelist disabled")
		}

		// Whitelist enabled with groups
		envEnabled := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "123456, 34567",
		})
		if !IsGroupAllowed(envEnabled, "123456") {
			t.Errorf("expected 123456 allowed")
		}
		if !IsGroupAllowed(envEnabled, "34567") {
			t.Errorf("expected 34567 allowed")
		}
		if IsGroupAllowed(envEnabled, "99999") {
			t.Errorf("expected 99999 NOT allowed (not in whitelist)")
		}
		if IsGroupAllowed(envEnabled, "") {
			t.Errorf("expected empty group NOT allowed")
		}

		// Whitelist enabled but list is empty -> no groups allowed
		envEmpty := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "",
		})
		if IsGroupAllowed(envEmpty, "123456") {
			t.Errorf("expected group NOT allowed when whitelist is empty")
		}
	})

	t.Run("OptionBPlatformIsolation", func(t *testing.T) {
		// Option B: Bare IDs belong strictly to the QQ family.
		// Non-QQ platforms (Telegram, Discord, etc.) must NOT match bare ID whitelist entries.
		envQQBare := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "123456",
		})

		// 1. QQ family matches bare ID
		if !IsGroupAllowed(envQQBare, "123456") {
			t.Errorf("expected bare ID allowed with unspecified platform")
		}
		if !IsGroupAllowed(envQQBare, "123456", "qq") {
			t.Errorf("expected QQ group 123456 allowed")
		}
		if !IsGroupAllowed(envQQBare, "123456", "onebot") {
			t.Errorf("expected OneBot group 123456 allowed")
		}
		if !IsGroupAllowed(envQQBare, "123456", "aiocqhttp") {
			t.Errorf("expected aiocqhttp group 123456 allowed")
		}
		if !IsGroupAllowed(envQQBare, "qq:123456") {
			t.Errorf("expected qq:123456 allowed")
		}

		// 2. Non-QQ platforms with same numeric ID MUST NOT collide with bare QQ entry
		if IsGroupAllowed(envQQBare, "123456", "telegram") {
			t.Errorf("N2 regression: Telegram group 123456 must NOT match bare QQ entry 123456")
		}
		if IsGroupAllowed(envQQBare, "123456", "discord") {
			t.Errorf("N2 regression: Discord group 123456 must NOT match bare QQ entry 123456")
		}
		if IsGroupAllowed(envQQBare, "telegram:123456") {
			t.Errorf("N2 regression: telegram:123456 must NOT match bare QQ entry 123456")
		}
		if IsGroupAllowed(envQQBare, "discord:123456") {
			t.Errorf("N2 regression: discord:123456 must NOT match bare QQ entry 123456")
		}

		// 3. Platform-qualified Telegram entry
		envTG := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "telegram:123456",
		})
		if !IsGroupAllowed(envTG, "123456", "telegram") {
			t.Errorf("expected Telegram group allowed with platform-qualified entry")
		}
		if !IsGroupAllowed(envTG, "telegram:123456") {
			t.Errorf("expected telegram:123456 allowed")
		}
		// QQ, Discord, and others must not match
		if IsGroupAllowed(envTG, "123456", "qq") {
			t.Errorf("QQ group must NOT match telegram:123456")
		}
		if IsGroupAllowed(envTG, "123456") {
			t.Errorf("bare group must NOT match telegram:123456")
		}
		if IsGroupAllowed(envTG, "123456", "discord") {
			t.Errorf("Discord group must NOT match telegram:123456")
		}

		// 4. Multi-platform coexist with same numeric ID
		envMulti := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "123456, telegram:123456, discord:123456",
		})
		if !IsGroupAllowed(envMulti, "123456", "qq") {
			t.Errorf("QQ group 123456 should be allowed")
		}
		if !IsGroupAllowed(envMulti, "123456", "telegram") {
			t.Errorf("Telegram group 123456 should be allowed")
		}
		if !IsGroupAllowed(envMulti, "123456", "discord") {
			t.Errorf("Discord group 123456 should be allowed")
		}
		if IsGroupAllowed(envMulti, "123456", "slack") {
			t.Errorf("Slack group 123456 must NOT be allowed")
		}

		// 5. R2: Colon-containing raw group IDs and mismatched prefix/explicit-platform pair
		envColon := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "telegram:room:42, telegram:12345",
		})
		// Colon-containing raw group ID matches when explicit platform is supplied
		if !IsGroupAllowed(envColon, "room:42", "telegram") {
			t.Errorf("R2 regression: expected Telegram group 'room:42' allowed for 'telegram:room:42'")
		}
		if IsGroupAllowed(envColon, "room:42", "discord") {
			t.Errorf("Discord group 'room:42' must NOT match telegram:room:42")
		}
		// Mismatched prefix and explicit platform (spoof attempt):
		// Discord caller sending "telegram:12345" must NOT match "telegram:12345" entry!
		if IsGroupAllowed(envColon, "telegram:12345", "discord") {
			t.Errorf("R2 regression: Discord event with groupID 'telegram:12345' must NOT hijack telegram:12345 entry")
		}
		// Omitted platform argument fallback allows prefixed identity for backward compatibility
		if !IsGroupAllowed(envColon, "telegram:12345") {
			t.Errorf("expected prefixed identity 'telegram:12345' allowed when no platform argument is supplied")
		}
	})

	t.Run("ImplicitWhitelistTransitions", func(t *testing.T) {
		// N1: Test implicit enable boundary invariants
		// Unset switch + empty list -> false
		if IsWhitelistEnabled(mockEnv(map[string]string{})) {
			t.Errorf("expected disabled when unset and empty")
		}
		// Unset switch + nonempty list -> true
		if !IsWhitelistEnabled(mockEnv(map[string]string{
			EnvGroupWhitelist: "101",
		})) {
			t.Errorf("expected enabled when unset and nonempty")
		}
		// Repro A target: Explicit "true" + empty list -> true (strict gate, 0 groups allowed)
		envReproATarget := mockEnv(map[string]string{
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "",
		})
		if !IsWhitelistEnabled(envReproATarget) {
			t.Errorf("expected enabled when explicit true even with empty list")
		}
		if IsGroupAllowed(envReproATarget, "101") {
			t.Errorf("expected all groups rejected when whitelist is strictly enabled with empty list")
		}

		// Repro B target: Explicit "false" + nonempty list -> false (whitelist disabled)
		envReproBTarget := mockEnv(map[string]string{
			EnvWhitelistEnabled: "false",
			EnvGroupWhitelist:    "101",
		})
		if IsWhitelistEnabled(envReproBTarget) {
			t.Errorf("expected disabled when explicit false even with nonempty list")
		}
		if !IsGroupAllowed(envReproBTarget, "999") {
			t.Errorf("expected all groups allowed when whitelist is disabled")
		}
	})

	t.Run("RollGroupWithRand", func(t *testing.T) {
		env := mockEnv(map[string]string{
			EnvProbability:       "0.50",
			EnvWhitelistEnabled: "true",
			EnvGroupWhitelist:    "123456, 34567",
		})

		hitRng := func() float64 { return 0.1 }

		// Whitelisted group hits RNG -> triggers
		if !RollGroupWithRand(env, "123456", hitRng) {
			t.Errorf("expected whitelisted group to trigger when hitting RNG")
		}

		// Non-whitelisted group never triggers even if RNG hits!
		if RollGroupWithRand(env, "99999", hitRng) {
			t.Errorf("expected non-whitelisted group to NEVER trigger")
		}
	})
}

