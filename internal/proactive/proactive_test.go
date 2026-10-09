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

