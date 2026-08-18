package billing

import (
	"testing"
)

func TestPriceTable_GetPrice(t *testing.T) {
	pt := NewPriceTable()

	// Registered model
	p, ok := pt.GetPrice("deepseek-chat")
	if !ok {
		t.Fatalf("expected deepseek-chat to be registered")
	}
	if p.PromptPricePerMillion != 1000 || p.CompletionPricePerMillion != 2000 {
		t.Errorf("unexpected price: %+v", p)
	}

	// Case insensitive
	p2, ok2 := pt.GetPrice("DeepSeek-V4-Flash")
	if !ok2 {
		t.Fatalf("expected DeepSeek-V4-Flash to be registered")
	}
	if p2.PromptPricePerMillion != 1000 {
		t.Errorf("unexpected price: %+v", p2)
	}

	// Unknown model fallback
	p3, ok3 := pt.GetPrice("unknown-custom-model")
	if ok3 {
		t.Fatalf("expected unknown model not to be registered")
	}
	if p3 != DefaultFallbackPrice {
		t.Errorf("expected default fallback price, got %+v", p3)
	}
}

func TestPriceTable_CalculateCost(t *testing.T) {
	pt := NewPriceTable()

	tests := []struct {
		name             string
		model            string
		promptTokens     int
		completionTokens int
		wantMinor        int64
	}{
		{
			name:             "zero tokens",
			model:            "deepseek-chat",
			promptTokens:     0,
			completionTokens: 0,
			wantMinor:        0,
		},
		{
			name:             "negative tokens clamped to zero",
			model:            "deepseek-chat",
			promptTokens:     -10,
			completionTokens: -5,
			wantMinor:        0,
		},
		{
			name:             "small token count rounds up to 1 minor unit",
			model:            "deepseek-chat", // 1000 / 2000 per 1M
			promptTokens:     10,              // 10 * 1000 = 10,000
			completionTokens: 10,              // 10 * 2000 = 20,000 => sum = 30,000 => (30000 + 999999) / 1000000 = 1
			wantMinor:        1,
		},
		{
			name:             "exact 1M prompt tokens",
			model:            "deepseek-chat", // 1000 / 2000
			promptTokens:     1_000_000,
			completionTokens: 0,
			wantMinor:        1000, // 10.00 snowflakes
		},
		{
			name:             "exact 1M completion tokens",
			model:            "deepseek-chat", // 1000 / 2000
			promptTokens:     0,
			completionTokens: 1_000_000,
			wantMinor:        2000, // 20.00 snowflakes
		},
		{
			name:             "typical request rounding up",
			model:            "deepseek-chat",
			promptTokens:     1500, // 1500 * 1000 = 1,500,000
			completionTokens: 300,  // 300 * 2000 = 600,000 => sum = 2,100,000 => ceil = 3
			wantMinor:        3,    // 0.03 snowflakes
		},
		{
			name:             "deepseek-reasoner request",
			model:            "deepseek-reasoner", // 4000 / 16000
			promptTokens:     5000,                // 5000 * 4000 = 20,000,000
			completionTokens: 1000,                // 1000 * 16000 = 16,000,000 => sum = 36,000,000 => 36 minor
			wantMinor:        36,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pt.CalculateCost(tt.model, tt.promptTokens, tt.completionTokens)
			if got != tt.wantMinor {
				t.Errorf("CalculateCost(%s, %d, %d) = %d, want %d",
					tt.model, tt.promptTokens, tt.completionTokens, got, tt.wantMinor)
			}
		})
	}
}

func TestPriceTable_RegisterPrice(t *testing.T) {
	pt := NewPriceTable()
	customPrice := ModelPrice{
		PromptPricePerMillion:     500,
		CompletionPricePerMillion: 1500,
	}
	pt.RegisterPrice("my-custom-model", customPrice)

	p, ok := pt.GetPrice("my-custom-model")
	if !ok || p != customPrice {
		t.Errorf("failed to retrieve custom price: %+v", p)
	}

	cost := pt.CalculateCost("my-custom-model", 2000, 1000)
	// 2000 * 500 = 1,000,000; 1000 * 1500 = 1,500,000 => 2,500,000 => ceil = 3 minor
	if cost != 3 {
		t.Errorf("expected 3 minor, got %d", cost)
	}
}
