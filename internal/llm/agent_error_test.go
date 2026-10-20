package llm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/storage"
)

type failedDialogueProvider struct{ err error }

func (p *failedDialogueProvider) Chat(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
	return nil, p.err
}

func TestAgentMarksDialogueProviderFailure(t *testing.T) {
	failure := errors.New("synthetic upstream unavailable after fallback")
	engine := &Engine{MaxIterations: 1, Provider: &failedDialogueProvider{err: failure}}
	result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
	if !result.LLMFailed || !errors.Is(result.Error, failure) || result.Silent {
		t.Fatalf("unexpected failure classification: %+v", result)
	}
}

func TestAgentClassifiesDialogueRouteFailures(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "router.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("unavailable=%v", unavailable), func(t *testing.T) {
			router := modelrouter.New(filepath.Join(t.TempDir(), "router.json"))
			if unavailable {
				router = modelrouter.NewSQL(db, "test-instance")
			}
			engine := &Engine{MaxIterations: 1, ModelRouter: router}
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{})
			if unavailable {
				if !result.LLMFailed || result.Error == nil || result.Silent {
					t.Fatalf("route failure not classified: %+v", result)
				}
			} else if result.LLMFailed || !result.Silent || result.SilenceReason != "route_disabled" {
				t.Fatalf("disabled route misclassified: %+v", result)
			}
		})
	}
}
