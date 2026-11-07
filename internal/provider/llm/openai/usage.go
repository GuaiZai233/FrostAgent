package openai

import "github.com/GuaiZai233/FrostAgent/internal/core"

// Pointer fields distinguish omitted/null usage details from an explicit zero.
// OpenAI exposes prompt_tokens_details.cached_tokens; DeepSeek also exposes
// prompt_cache_hit_tokens. Cached tokens are a subset of input, never additive.
type apiUsage struct {
	Prompt        *int `json:"prompt_tokens"`
	Completion    *int `json:"completion_tokens"`
	Total         *int `json:"total_tokens"`
	CacheHit      *int `json:"prompt_cache_hit_tokens"`
	PromptDetails *struct {
		Cached *int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func validToken(value *int) bool { return value != nil && *value >= 0 }

func (u *apiUsage) coreUsage(embedding bool) *core.Usage {
	if u == nil {
		return nil
	}
	result := &core.Usage{Reported: &core.UsageReported{}}
	if validToken(u.Prompt) {
		result.PromptTokens = *u.Prompt
		result.Reported.Prompt = true
	}
	if validToken(u.Completion) {
		result.CompletionTokens = *u.Completion
		result.Reported.Completion = true
	} else if embedding {
		// Embeddings have no generated completion.
		result.Reported.Completion = true
	}
	if validToken(u.Total) {
		result.TotalTokens = *u.Total
		result.Reported.Total = true
	} else if result.Reported.Prompt && result.Reported.Completion {
		result.TotalTokens = result.PromptTokens + result.CompletionTokens
		result.Reported.Total = true
	}
	if u.PromptDetails != nil && validToken(u.PromptDetails.Cached) {
		result.CachedPromptTokens = u.PromptDetails.Cached
	} else if validToken(u.CacheHit) {
		result.CachedPromptTokens = u.CacheHit
	}
	return result
}
