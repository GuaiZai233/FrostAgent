package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestInjectionBillingSettlesLateSuccessAndAggregatesReceipt(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_%v", cancelled), func(t *testing.T) {
			var mu sync.Mutex
			var ids []string
			commits, releases := 0, 0
			var releaseReason string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				result := billing.LLMReservationResult{BalanceMinor: 9000}
				switch {
				case r.URL.Path == "/v1/balance":
					json.NewEncoder(w).Encode(map[string]any{"data": billing.BalanceResult{Exists: true, BalanceMinor: 9000}})
					return
				case r.URL.Path == "/v1/billing/llm/reservations":
					var req billing.LLMReserveRequest
					json.NewDecoder(r.Body).Decode(&req)
					ids = append(ids, req.CallID+"|"+r.Header.Get("Idempotency-Key"))
					result.ReservationID = fmt.Sprintf("reservation-%d", len(ids))
					result.Decision = billing.DecisionReserved
				case strings.HasSuffix(r.URL.Path, "/commit"):
					commits++
				case strings.HasSuffix(r.URL.Path, "/release"):
					releases++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					releaseReason = fmt.Sprint(body["reason"])
				default:
					t.Errorf("unexpected billing request: %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"data": result})
			}))
			defer server.Close()
			e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 1, ModelName: "test-model", BillingClient: billing.NewClient(server.URL, "synthetic-token", time.Second), BillingConfig: billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 100, SafetyMultiplier: 1.2}}
			started, secondStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			e.Provider = injectionProvider(func(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
				if calls.Add(1) == 1 {
					close(started)
					if cancelled {
						<-ctx.Done()
						return nil, ctx.Err()
					}
					<-release
				} else {
					close(secondStarted)
				}
				resp := finalInjection("current answer")
				resp.Usage = &core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
				return resp, nil
			})
			s := e.SessionManager.GetOrCreate("private:billing-synthetic")
			state := &BillingRunState{Platform: "synthetic", ExternalID: "sender-test", TaskID: "task-test", BillingActive: true}
			first := s.ReserveSequenceTurn("sender", true, false)
			old := make(chan AgentRunResult, 1)
			go func() { old <- injectionRun(e, s, first, "first", state) }()
			receiveInjection(t, started)
			second := s.ReserveSequenceTurn("sender", true, false)
			if result := receiveInjection(t, old); !result.Silent || result.Content != "" {
				t.Fatalf("old attempt produced reply: %+v", result)
			}
			current := make(chan AgentRunResult, 1)
			go func() { current <- injectionRun(e, s, second, "second", nil) }()
			receiveInjection(t, secondStarted)
			if !cancelled {
				select {
				case result := <-current:
					t.Fatalf("receipt finalized before late settlement: %+v", result)
				default:
				}
				close(release)
			}
			result := receiveInjection(t, current)
			wantCalls := 2
			if cancelled {
				wantCalls = 1
			}
			if result.Usage.TotalTokens != 15*wantCalls || state.Receipt().IterationsBilled != wantCalls {
				t.Fatalf("bad totals: result=%+v receipt=%+v", result, state.Receipt())
			}
			mu.Lock()
			defer mu.Unlock()
			if len(ids) != 2 || ids[0] == ids[1] || commits != wantCalls || releases != 2-wantCalls {
				t.Fatalf("bad accounting: ids=%v commits=%d releases=%d", ids, commits, releases)
			}
			if cancelled && releaseReason != billing.ReasonRequestCancelled {
				t.Fatalf("release reason=%q", releaseReason)
			}
		})
	}
}

func TestInjectionBillingRefreshesBalanceAfterLateRelease(t *testing.T) {
	var balance atomic.Int64
	balance.Store(10000)
	releaseStarted, allowRelease, committed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var reserves atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result := billing.LLMReservationResult{}
		switch {
		case r.URL.Path == "/v1/balance":
			json.NewEncoder(w).Encode(map[string]any{"data": billing.BalanceResult{Exists: true, BalanceMinor: balance.Load()}})
			return
		case r.URL.Path == "/v1/billing/llm/reservations":
			result.ReservationID = fmt.Sprintf("reserve-%d", reserves.Add(1))
			result.Decision = billing.DecisionReserved
			result.BalanceMinor = balance.Add(-100)
		case strings.HasSuffix(r.URL.Path, "/release"):
			close(releaseStarted)
			<-allowRelease
			// Simulate an older response snapshot: the release refund happens
			// after the successor commit and must be read back at finalization.
			result.BalanceMinor = balance.Load()
			balance.Add(100)
		case strings.HasSuffix(r.URL.Path, "/commit"):
			var body map[string]int64
			json.NewDecoder(r.Body).Decode(&body)
			result.BalanceMinor = balance.Add(100 - body["actual_minor"])
			close(committed)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": result})
	}))
	defer server.Close()
	e := &Engine{SessionManager: NewSessionManager(), MaxIterations: 1, ModelName: "synthetic-model", BillingClient: billing.NewClient(server.URL, "synthetic-token", time.Second), BillingConfig: billing.Config{Enabled: true, Timeout: time.Second, MaxOutputTokens: 100, SafetyMultiplier: 1.2}}
	started := make(chan struct{})
	var calls atomic.Int32
	e.Provider = injectionProvider(func(ctx context.Context, _ core.ChatRequest) (*core.ChatResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		resp := finalInjection("done")
		resp.Usage = &core.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
		return resp, nil
	})
	s := e.SessionManager.GetOrCreate("private:billing-release")
	state := &BillingRunState{Platform: "synthetic", ExternalID: "synthetic-sender", TaskID: "synthetic-task", BillingActive: true}
	first := s.ReserveSequenceTurn("A", true, false)
	old := make(chan AgentRunResult, 1)
	go func() { old <- injectionRun(e, s, first, "first", state) }()
	receiveInjection(t, started)
	next := s.ReserveSequenceTurn("A", true, false)
	receiveInjection(t, old)
	receiveInjection(t, releaseStarted)
	current := make(chan AgentRunResult, 1)
	go func() { current <- injectionRun(e, s, next, "next", nil) }()
	receiveInjection(t, committed)
	close(allowRelease)
	receiveInjection(t, current)
	if state.Receipt().LastBalanceMinor != balance.Load() {
		t.Fatalf("stale receipt: %+v actual=%d", state.Receipt(), balance.Load())
	}
}
