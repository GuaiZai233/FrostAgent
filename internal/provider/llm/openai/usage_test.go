package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestUsageFieldPresence(t *testing.T) {
	for _, tc := range []struct {
		name, raw                 string
		embedding                 bool
		prompt, completion, total bool
		totalValue                int
		cache                     *int
	}{
		{name: "empty", raw: `{}`},
		{name: "null", raw: `{"prompt_tokens":null,"total_tokens":null}`},
		{name: "explicit zeros", raw: `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}`, prompt: true, completion: true, total: true, cache: intPtr(0)},
		{name: "derive total", raw: `{"prompt_tokens":12,"completion_tokens":3}`, prompt: true, completion: true, total: true, totalValue: 15},
		{name: "partial input", raw: `{"prompt_tokens":12}`, prompt: true},
		{name: "total only", raw: `{"total_tokens":15}`, total: true, totalValue: 15},
		{name: "deepseek cache", raw: `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_cache_hit_tokens":9}`, prompt: true, completion: true, total: true, totalValue: 15, cache: intPtr(9)},
		{name: "no cache detail", raw: `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{}}`, prompt: true, completion: true, total: true, totalValue: 15},
		{name: "negative unknown", raw: `{"prompt_tokens":-1,"completion_tokens":-2,"total_tokens":-3,"prompt_cache_hit_tokens":-1}`},
		{name: "embedding", raw: `{"prompt_tokens":12,"total_tokens":12}`, embedding: true, prompt: true, completion: true, total: true, totalValue: 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wire apiUsage
			if err := json.Unmarshal([]byte(tc.raw), &wire); err != nil {
				t.Fatal(err)
			}
			usage := wire.coreUsage(tc.embedding)
			if usage.Reported.Prompt != tc.prompt || usage.Reported.Completion != tc.completion || usage.Reported.Total != tc.total || usage.TotalTokens != tc.totalValue {
				t.Fatalf("presence/value lost: %+v %+v", usage, usage.Reported)
			}
			if (usage.CachedPromptTokens == nil) != (tc.cache == nil) || (tc.cache != nil && *usage.CachedPromptTokens != *tc.cache) {
				t.Fatalf("cache presence/value lost: %+v", usage)
			}
		})
	}
	var absent *apiUsage
	if absent.coreUsage(false) != nil {
		t.Fatal("omitted usage must remain nil")
	}
}

func intPtr(value int) *int { return &value }

func TestUsageObserverCountsUnusablePaidResponsesOnce(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		embedding  bool
	}{
		{"chat no choices", `{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":0,"total_tokens":20}}`, false},
		{"embedding bad vector", `{"data":[],"usage":{"prompt_tokens":20,"total_tokens":20}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			client := NewClient(server.URL, "")
			calls := 0
			client.ObserveUsage = func(usage *core.Usage) {
				calls++
				if usage == nil || usage.TotalTokens != 20 {
					t.Errorf("usage missing: %+v", usage)
				}
			}
			var err error
			if tc.embedding {
				_, err = client.Embed(context.Background(), core.EmbeddingRequest{Input: []string{"synthetic"}})
			} else {
				_, err = client.Chat(context.Background(), core.ChatRequest{})
			}
			if err == nil || calls != 1 {
				t.Fatalf("error=%v observations=%d", err, calls)
			}
		})
	}
}
