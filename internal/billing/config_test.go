package billing

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigFromEnv(t *testing.T) {
	// Set test environment
	os.Setenv("BILLING_ENABLED", "true")
	os.Setenv("ALCYONE_BASE_URL", "http://127.0.0.1:9090")
	os.Setenv("ALCYONE_SERVICE_TOKEN", "secret-token-123")
	os.Setenv("ALCYONE_TIMEOUT", "10s")
	os.Setenv("BILLING_MAX_OUTPUT_TOKENS", "4096")
	os.Setenv("BILLING_SAFETY_MULTIPLIER", "1.5")
	os.Setenv("BILLING_PROMPT_PRICE_PER_MILLION", "1500")
	os.Setenv("BILLING_COMPLETION_PRICE_PER_MILLION", "3000")
	defer func() {
		os.Unsetenv("BILLING_ENABLED")
		os.Unsetenv("ALCYONE_BASE_URL")
		os.Unsetenv("ALCYONE_SERVICE_TOKEN")
		os.Unsetenv("ALCYONE_TIMEOUT")
		os.Unsetenv("BILLING_MAX_OUTPUT_TOKENS")
		os.Unsetenv("BILLING_SAFETY_MULTIPLIER")
		os.Unsetenv("BILLING_PROMPT_PRICE_PER_MILLION")
		os.Unsetenv("BILLING_COMPLETION_PRICE_PER_MILLION")
	}()

	cfg := LoadConfigFromEnv()

	if !cfg.Enabled {
		t.Errorf("expected Enabled to be true")
	}
	if cfg.BaseURL != "http://127.0.0.1:9090" {
		t.Errorf("unexpected BaseURL: %s", cfg.BaseURL)
	}
	if cfg.ServiceToken != "secret-token-123" {
		t.Errorf("unexpected ServiceToken: %s", cfg.ServiceToken)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("unexpected Timeout: %v", cfg.Timeout)
	}
	if cfg.ModelName != DefaultModelName {
		t.Errorf("unexpected ModelName: %s", cfg.ModelName)
	}
	if cfg.MaxOutputTokens != 4096 {
		t.Errorf("unexpected MaxOutputTokens: %d", cfg.MaxOutputTokens)
	}
	if cfg.SafetyMultiplier != 1.5 {
		t.Errorf("unexpected SafetyMultiplier: %f", cfg.SafetyMultiplier)
	}
	if cfg.CustomPromptPricePerMillion == nil || *cfg.CustomPromptPricePerMillion != 1500 {
		t.Errorf("unexpected CustomPromptPricePerMillion: %v", cfg.CustomPromptPricePerMillion)
	}
	if cfg.CustomCompletionPricePerMillion == nil || *cfg.CustomCompletionPricePerMillion != 3000 {
		t.Errorf("unexpected CustomCompletionPricePerMillion: %v", cfg.CustomCompletionPricePerMillion)
	}
}

func TestInitBillingClient(t *testing.T) {
	// Disabled billing returns nil, nil
	cfgDisabled := Config{
		Enabled: false,
	}
	client, err := InitBillingClient(cfgDisabled)
	if err != nil || client != nil {
		t.Fatalf("expected nil client when disabled, got %v, %v", client, err)
	}

	// Enabled with empty URL returns error
	cfgEmptyURL := Config{
		Enabled: true,
		BaseURL: "",
	}
	_, err = InitBillingClient(cfgEmptyURL)
	if err == nil {
		t.Fatalf("expected error on empty BaseURL when enabled")
	}

	// Enabled with valid URL
	cfgValid := Config{
		Enabled:   true,
		BaseURL:   "http://127.0.0.1:8081",
		ModelName: "deepseek-chat",
	}
	client, err = InitBillingClient(cfgValid)
	if err != nil || client == nil {
		t.Fatalf("expected valid client, got %v, %v", client, err)
	}

	// Enabled with custom prices overrides price table
	customPrompt := int64(333)
	customCompletion := int64(777)
	cfgCustom := Config{
		Enabled:                         true,
		BaseURL:                         "http://127.0.0.1:8081",
		ModelName:                       "custom-override-model",
		CustomPromptPricePerMillion:     &customPrompt,
		CustomCompletionPricePerMillion: &customCompletion,
	}
	client, err = InitBillingClient(cfgCustom)
	if err != nil || client == nil {
		t.Fatalf("expected valid client with custom prices, got %v, %v", client, err)
	}
	p, ok := GetPrice("custom-override-model")
	if !ok || p.PromptPricePerMillion != 333 || p.CompletionPricePerMillion != 777 {
		t.Errorf("custom price not applied: %+v, ok=%v", p, ok)
	}
}

func TestFormatSnowflakes(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{minor: 0, want: "0.00"},
		{minor: 1, want: "0.01"},
		{minor: 10, want: "0.10"},
		{minor: 99, want: "0.99"},
		{minor: 100, want: "1.00"},
		{minor: 1050, want: "10.50"},
		{minor: 10000, want: "100.00"},
		{minor: -150, want: "-1.50"},
		{minor: -5, want: "-0.05"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatSnowflakes(tt.minor)
			if got != tt.want {
				t.Errorf("FormatSnowflakes(%d) = %q, want %q", tt.minor, got, tt.want)
			}
		})
	}
}
