package actionscat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type waitHTTPClient func(*http.Request) (*http.Response, error)

func (c waitHTTPClient) Do(req *http.Request) (*http.Response, error) { return c(req) }

func TestRunWaitBoundsPolling(t *testing.T) {
	for _, budget := range []time.Duration{100 * time.Millisecond, time.Second} {
		t.Run(budget.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				posts, polls := 0, 0
				client := New(func(key string) string {
					if key == "ACTIONSCAT_ENDPOINT" {
						return "http://fixture.invalid"
					}
					return ""
				}, WithHTTPClient(waitHTTPClient(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPost {
						posts++
						return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"id":"fixture-run","status":"running"}`))}, nil
					}
					polls++
					<-req.Context().Done()
					return nil, req.Context().Err()
				})))
				start := time.Now()
				run, err := client.TriggerRunAndWait(context.Background(), "fixture-action", ManualRunReq{}, budget)
				if err != nil || run == nil || run.ID != "fixture-run" || run.Status != "running" {
					t.Fatalf("wait expiry must preserve the accepted run: run=%+v err=%v", run, err)
				}
				if elapsed := time.Since(start); elapsed != budget {
					t.Fatalf("wait exceeded its budget: elapsed=%v budget=%v", elapsed, budget)
				}
				wantPolls := 1
				if budget < defaultPollInterval {
					wantPolls = 0
				}
				if posts != 1 || polls != wantPolls {
					t.Fatalf("requests: posts=%d polls=%d, want 1/%d", posts, polls, wantPolls)
				}
			})
		})
	}
}

func TestRunWaitPreservesParentCancellationAndTerminalResults(t *testing.T) {
	for _, mode := range []string{"cancel", "parent deadline", "terminal post", "terminal poll"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				var cancel context.CancelFunc
				if mode == "cancel" {
					ctx, cancel = context.WithCancel(ctx)
					time.AfterFunc(50*time.Millisecond, cancel)
				} else if mode == "parent deadline" {
					ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(ctx)
				}
				defer cancel()
				posts, polls := 0, 0
				client := New(func(key string) string {
					if key == "ACTIONSCAT_ENDPOINT" {
						return "http://fixture.invalid"
					}
					return ""
				}, WithHTTPClient(waitHTTPClient(func(req *http.Request) (*http.Response, error) {
					status := "running"
					if req.Method == http.MethodPost {
						posts++
						if mode == "terminal post" {
							status = "succeeded"
						}
					} else {
						polls++
						status = "succeeded"
					}
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"fixture-run","status":"` + status + `"}`))}, nil
				})))
				run, err := client.TriggerRunAndWait(ctx, "fixture-action", ManualRunReq{}, time.Second)
				if run == nil || run.ID != "fixture-run" || posts != 1 {
					t.Fatalf("lost accepted run: %+v %v", run, err)
				}
				switch mode {
				case "cancel":
					if !errors.Is(err, context.Canceled) || polls != 0 {
						t.Fatalf("cancellation: %v polls=%d", err, polls)
					}
				case "parent deadline":
					if !errors.Is(err, context.DeadlineExceeded) || polls != 0 {
						t.Fatalf("parent deadline: %v polls=%d", err, polls)
					}
				default:
					if err != nil || !run.IsTerminal() {
						t.Fatalf("terminal result: %+v %v", run, err)
					}
				}
			})
		})
	}
}
