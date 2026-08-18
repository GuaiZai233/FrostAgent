package billing

import (
	"strings"
	"sync"
)

// ModelPrice defines input and output token pricing in minor units per 1,000,000 tokens.
// 100 minor units = 1 snowflake = 0.01 CNY.
type ModelPrice struct {
	PromptPricePerMillion     int64 `json:"prompt_price_per_million"`
	CompletionPricePerMillion int64 `json:"completion_price_per_million"`
}

// Default fallback pricing used when a model is not recognized in the registry.
var DefaultFallbackPrice = ModelPrice{
	PromptPricePerMillion:     2000, // 0.20 CNY / 1M tokens = 20 snowflakes / 1M
	CompletionPricePerMillion: 8000, // 0.80 CNY / 1M tokens = 80 snowflakes / 1M
}

// PriceTable manages model pricing lookups.
type PriceTable struct {
	mu     sync.RWMutex
	prices map[string]ModelPrice
}

var defaultRegistry = NewPriceTable()

// NewPriceTable creates an empty PriceTable. Model-specific prices must be
// registered explicitly at runtime.
func NewPriceTable() *PriceTable {
	return &PriceTable{
		prices: make(map[string]ModelPrice),
	}
}

// RegisterPrice adds or overrides the price for a specific model name.
func (pt *PriceTable) RegisterPrice(model string, price ModelPrice) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.prices[strings.ToLower(strings.TrimSpace(model))] = price
}

// GetPrice looks up the price for a model, returning the price and whether it was officially registered.
func (pt *PriceTable) GetPrice(model string) (ModelPrice, bool) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	key := strings.ToLower(strings.TrimSpace(model))
	price, ok := pt.prices[key]
	if ok {
		return price, true
	}
	return DefaultFallbackPrice, false
}

// CalculateCost computes total token cost in minor units using ceiling integer arithmetic.
func (pt *PriceTable) CalculateCost(model string, promptTokens, completionTokens int) int64 {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	if promptTokens == 0 && completionTokens == 0 {
		return 0
	}

	price, _ := pt.GetPrice(model)
	numerator := int64(promptTokens)*price.PromptPricePerMillion + int64(completionTokens)*price.CompletionPricePerMillion
	if numerator <= 0 {
		return 0
	}
	// Ceiling division: (numerator + 999,999) / 1,000,000
	return (numerator + 999_999) / 1_000_000
}

// Default registry convenience functions

// RegisterPrice registers a model price in the default global registry.
func RegisterPrice(model string, price ModelPrice) {
	defaultRegistry.RegisterPrice(model, price)
}

// GetPrice looks up a model price from the default global registry.
func GetPrice(model string) (ModelPrice, bool) {
	return defaultRegistry.GetPrice(model)
}

// CalculateCost calculates token cost using the default global registry.
func CalculateCost(model string, promptTokens, completionTokens int) int64 {
	return defaultRegistry.CalculateCost(model, promptTokens, completionTokens)
}
