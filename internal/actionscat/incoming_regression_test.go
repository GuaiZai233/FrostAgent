package actionscat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/actionscat"
	"FrostAgent/internal/adapter/astrbot"
	"FrostAgent/internal/security"
)

type ingressCountingClassifier struct {
	groupCalls atomic.Int32
	wakeCalls  atomic.Int32
}

func (c *ingressCountingClassifier) Classify(_ context.Context, input security.ClassificationInput) (security.ClassificationResult, error) {
	if input.Stage == security.StageIngress {
		if strings.Contains(input.Content, "ordinary fixture group text") {
			c.groupCalls.Add(1)
		} else {
			c.wakeCalls.Add(1)
		}
	}
	return security.ClassificationResult{Category: security.RiskCategoryNone, RiskLevel: security.RiskLevelNone}, nil
}

func TestDisabledDispatchSkipsOnlyUnaddressedIngressClassification(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		t.Run(name, func(t *testing.T) {
			var disabled atomic.Bool
			var groupDispatches atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var event actionscat.MessageEvent
				if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
					t.Error(err)
				}
				if event.Text == "ordinary fixture group text" {
					groupDispatches.Add(1)
				}
				_, _ = w.Write([]byte("{}"))
			}))
			defer backend.Close()
			client := actionscat.New(func(key string) string {
				if key == "ACTIONSCAT_ENDPOINT" {
					return backend.URL
				}
				if key == "ACTIONSCAT_ENABLED" && disabled.Load() {
					return "false"
				}
				return ""
			})
			adapter, engine, group := ingressCase(name, "group", "ordinary fixture group text")
			classifier := &ingressCountingClassifier{}
			engine.Security = security.NewController(t.TempDir())
			engine.Security.SetMode(security.ControlModeAggressive)
			engine.Security.SetClassifier(classifier)
			barrier := make(chan struct{}, 3)
			engine.Provider = emptyProvider{calls: barrier}
			adapter.SetIncomingDispatcher(client)
			connection := startIngress(t, adapter, false)
			wantGroups := int32(0)
			for step, off := range []bool{true, false, true} {
				disabled.Store(off)
				if err := connection.WriteJSON(group); err != nil {
					t.Fatal(err)
				}
				_, _, private := ingressCase(name, "private", fmt.Sprintf("fixture wake barrier %d", step))
				if err := connection.WriteJSON(private); err != nil {
					t.Fatal(err)
				}
				select {
				case <-barrier:
				case <-time.After(2 * time.Second):
					t.Fatal("wake message failed to cross the reader barrier")
				}
				if !off {
					wantGroups++
				}
				if got := classifier.groupCalls.Load(); got != wantGroups {
					t.Errorf("step %d disabled=%v: unaddressed classifier calls=%d want=%d", step, off, got, wantGroups)
				}
				if got := groupDispatches.Load(); got != wantGroups {
					t.Errorf("step %d: group dispatches=%d want=%d", step, got, wantGroups)
				}
				if got := classifier.wakeCalls.Load(); got < int32(step+1) {
					t.Errorf("disabling dispatch bypassed normal wake security: calls=%d", got)
				}
			}
		})
	}
}

func TestIngressCarriesTrustedTransportAdapter(t *testing.T) {
	for _, name := range []string{"onebot", "astrbot"} {
		for _, messageType := range []string{"group", "private"} {
			t.Run(name+"/"+messageType, func(t *testing.T) {
				captured := make(chan map[string]any, 1)
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var event map[string]any
					if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
						t.Error(err)
					}
					captured <- event
					_, _ = w.Write([]byte("{}"))
				}))
				defer backend.Close()
				adapter, _, event := ingressCase(name, messageType, "fixture routing text")
				if incoming, ok := event.(astrbot.Event); ok {
					incoming.Metadata = map[string]any{"adapter_id": "attacker-adapter"}
					event = incoming
				}
				adapter.SetIncomingDispatcher(dispatchClient(backend.URL))
				connection := startIngress(t, adapter, false)
				if err := connection.WriteJSON(event); err != nil {
					t.Fatal(err)
				}
				select {
				case payload := <-captured:
					if payload["adapter_id"] != name {
						t.Fatalf("untrusted or missing adapter: %+v", payload)
					}
					wantPlatform := name
					if name == "astrbot" {
						wantPlatform = "test-platform"
					}
					if payload["platform"] != wantPlatform {
						t.Fatalf("upstream platform lost: %+v", payload)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("event was not dispatched")
				}
			})
		}
	}
}
