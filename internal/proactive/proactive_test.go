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
