package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
)

func TestSleepRefreshRunsBeforeNextThoughtAndPreservesTranscript(t *testing.T) {
	for _, toolErr := range []error{nil, timesleep.ErrTimeout, errors.New("invalid seconds")} {
		t.Run(fmt.Sprint(toolErr), func(t *testing.T) {
			e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 3}
			s := e.SessionManager.GetOrCreate("group:synthetic-refresh")
			turn := s.ReserveSequenceTurn("sender:synthetic", true, false)
			defer turn.Done()
			turn.Wait()
			messages, _, _ := s.PrepareTurnMessages(turn, "durable-input", "original-background", nil)
			refreshes, calls := 0, 0
			e.ToolRegistry = map[string]ToolExecutor{timesleep.ToolName: injectionTool{timesleep.ToolName, func() (string, error) { return "sleep-result", toolErr }}}
			e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				calls++
				if calls < 3 {
					return injectionCalls(timesleep.ToolName), nil
				}
				want := "refreshed-background"
				if toolErr != nil && !errors.Is(toolErr, timesleep.ErrTimeout) {
					want = "original-background"
				}
				all := fmt.Sprint(req.Messages)
				if strings.Count(all, want) != 1 || strings.Count(all, "durable-input") > 0 {
					t.Errorf("prompt was not replaced: %s", all)
				}
				toolResults := 0
				for _, msg := range req.Messages {
					if msg.Role == core.RoleTool {
						toolResults++
					}
				}
				if toolResults != 2 {
					t.Errorf("lost tool trajectory: %s", all)
				}
				return finalInjection("finished"), nil
			})
			result := e.RunMessagesWithContext(messages, RunContext{SessionID: s.ID(), Turn: turn, Epoch: turn.Epoch(), Mock: true, RefreshPrompt: func(context.Context) (PromptRefresh, error) {
				refreshes++
				return PromptRefresh{Content: "refreshed-background"}, nil
			}})
			wantRefreshes := 2
			if toolErr != nil && !errors.Is(toolErr, timesleep.ErrTimeout) {
				wantRefreshes = 0
			}
			if result.Content != "finished" || refreshes != wantRefreshes || e.TotalMessagesProcessed.Load() != 3 || turn.Interrupted() {
				t.Fatalf("changed execution or budget: %+v, refreshes=%d", result, refreshes)
			}
			if all := fmt.Sprint(turn.lane.transcript); strings.Contains(all, "background") || !strings.Contains(all, "durable-input") {
				t.Fatalf("transient prompt entered sequence checkpoint: %s", all)
			}
		})
	}
}

func TestSleepRefreshSecurityAndWhitelist(t *testing.T) {
	for _, mode := range []string{"pass", "blocked", "failure", "whitelist"} {
		t.Run(mode, func(t *testing.T) {
			e := &Engine{}
			run := RunContext{SessionID: "group:synthetic-room", ActorPlatform: "synthetic", ActorUserID: "synthetic-actor"}
			if mode == "whitelist" {
				t.Setenv(EnvGroupCompactWhitelistEnabled, "true")
				t.Setenv(EnvGroupCompactWhitelist, "")
			} else {
				e.Security = security.NewController(t.TempDir())
				e.Security.SetMode(security.ControlModeAggressive)
				e.Security.SetClassifier(&mockGateClassifier{fn: func(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
					if input.Origin != security.SourceGroupContext || input.Stage != security.StageIngress {
						t.Errorf("wrong context provenance: %+v", input)
					}
					if mode == "failure" {
						return security.ClassificationResult{}, errors.New("synthetic classifier unavailable")
					}
					if mode == "blocked" {
						return security.ClassificationResult{Category: security.RiskCategoryPromptInjection, RiskLevel: security.RiskLevelHigh}, nil
					}
					return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
				}})
			}
			snapshot := GroupContextSnapshot{RunningSummary: "synthetic-summary", RecentStructuredMessages: []GroupCompactMessage{{Role: "user", SenderID: "synthetic-sender", Content: "untrusted-new-message"}}}
			prompt, _, err := e.GroupContextPrompt(context.Background(), run, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "blocked" || mode == "failure" {
				if strings.Contains(prompt, "synthetic-summary") || strings.Contains(prompt, "untrusted-new-message") {
					t.Fatalf("unsafe context escaped: %s", prompt)
				}
			} else if !strings.Contains(prompt, "untrusted-new-message") || strings.Contains(prompt, "synthetic-summary") != (mode == "pass") {
				t.Fatalf("wrong refreshed context: %s", prompt)
			}
			if mode == "whitelist" && !strings.Contains(prompt, GroupCompactStatusPrompt) {
				t.Fatal("whitelist warning lost")
			}
		})
	}
}

func TestSleepRefreshFailureStopsModel(t *testing.T) {
	e := &Engine{MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{timesleep.ToolName: injectionTool{timesleep.ToolName, func() (string, error) { return "done", nil }}}}
	calls := 0
	e.Provider = injectionProvider(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
		calls++
		return injectionCalls(timesleep.ToolName), nil
	})
	want := errors.New("synthetic refresh failure")
	result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "input"}}, RunContext{Mock: true, RefreshPrompt: func(context.Context) (PromptRefresh, error) { return PromptRefresh{}, want }})
	if !errors.Is(result.Error, want) || calls != 1 {
		t.Fatalf("stale prompt used after refresh failure: %+v, calls=%d", result, calls)
	}
}
