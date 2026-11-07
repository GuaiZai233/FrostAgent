package botstatus

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/tokenusage"
	"testing"
)

func TestOverviewReportsPartialUsage(t *testing.T) {
	store, _ := tokenusage.New(nil, "", "")
	cache := 8
	for _, usage := range []*core.Usage{{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedPromptTokens: &cache}, nil} {
		if err := store.Record("dialogue", usage); err != nil {
			t.Fatal(err)
		}
	}
	service := New(&llm.Engine{TokenUsage: store}, "test", "")
	response, err := service.GetOverview(context.Background(), connect.NewRequest(&v1.GetOverviewRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	result := response.Msg.TokenUsage
	if result == nil || !result.Available || len(result.Modules) != len(tokenusage.Modules) {
		t.Fatalf("missing usage: %+v", result)
	}
	dialogue := result.Modules[0]
	if dialogue.Requests != 2 || dialogue.Total.Value != 15 || dialogue.Total.UnestimatedRequests != 1 || dialogue.Cached.Value != 8 || dialogue.CacheHits.Value != 1 {
		t.Fatalf("usage changed at API boundary: %+v", dialogue)
	}
}
