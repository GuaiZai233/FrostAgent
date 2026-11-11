package modelrouter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
)

func TestNativeContinuationStateStaysWithEndpoint(t *testing.T) {
	for _, tc := range []struct {
		format               api.Format
		first, final, marker string
	}{
		{api.Responses, `{"output":[{"type":"reasoning","summary":[],"encrypted_content":"synthetic-opaque-state"},{"type":"message","content":[{"type":"output_text","text":"visible-reply"}]},{"type":"function_call","call_id":"fixture-call","name":"lookup","arguments":"{\"query\":\"synthetic-query\"}"}]}`, `{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`, "synthetic-opaque-state"},
		{api.Anthropic, `{"content":[{"type":"thinking","thinking":"synthetic-hidden","signature":"synthetic-opaque-state"},{"type":"text","text":"visible-reply"},{"type":"tool_use","id":"fixture-call","name":"lookup","input":{"query":"synthetic-query"}}]}`, `{"content":[{"type":"text","text":"done"}]}`, "synthetic-opaque-state"},
		{api.Gemini, `{"candidates":[{"content":{"parts":[{"thought":true,"text":"synthetic-hidden","thoughtSignature":"synthetic-opaque-state"},{"text":"visible-reply"},{"functionCall":{"id":"fixture-call","name":"lookup","args":{"query":"synthetic-query"}},"thoughtSignature":"synthetic-opaque-state"}]}}]}`, `{"candidates":[{"content":{"parts":[{"text":"done"}]}}]}`, "synthetic-opaque-state"},
	} {
		for _, change := range []string{"base-url", "endpoint-id", "path", "unscoped-history", "equivalent-base"} {
			t.Run(string(tc.format)+"/"+change, func(t *testing.T) {
				var requests atomic.Int32
				var expectOpaque atomic.Bool
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						fmt.Fprint(w, tc.first)
						return
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					body := string(raw)
					if got := strings.Contains(body, tc.marker); got != expectOpaque.Load() {
						t.Errorf("opaque state replay = %v, want %v", got, expectOpaque.Load())
					}
					if !expectOpaque.Load() && strings.Contains(body, "synthetic-hidden") {
						t.Error("hidden reasoning reached a different endpoint")
					}
					for _, visible := range []string{"visible-reply", "lookup", "synthetic-query", "synthetic-result"} {
						if !strings.Contains(body, visible) {
							t.Errorf("continuation lost visible history %q", visible)
						}
					}
					fmt.Fprint(w, tc.final)
				})
				server := httptest.NewServer(handler)
				defer server.Close()
				other := httptest.NewServer(handler)
				defer other.Close()
				manager := New(filepath.Join(t.TempDir(), "router.json"))
				cfg := manager.Draft()
				cfg.Endpoints = []Endpoint{{ID: "fixture-endpoint", DisplayName: "Fixture", BaseURL: server.URL + "/origin", APIFormat: string(tc.format), Enabled: true}}
				cfg.Models = []Model{{ID: "fixture-model", DisplayName: "Fixture", EndpointID: "fixture-endpoint", UpstreamModel: "test-model", Enabled: true}}
				cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "fixture-model"}
				publish := func() {
					t.Helper()
					if err := manager.SaveDraft(cfg); err != nil {
						t.Fatal(err)
					}
					if _, err := manager.Publish(); err != nil {
						t.Fatal(err)
					}
				}
				publish()
				provider := manager.Provider(WorkloadDialogue, false, 0)
				pinned := manager.WithSnapshot(context.Background(), manager.Snapshot())
				req := core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}}
				first, err := provider.Chat(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				if first.Message.ProviderState == nil {
					t.Fatal("missing continuation state")
				}
				// Exercise persisted history and the fresh client created for each run.
				raw, err := json.Marshal(first.Message)
				if err != nil {
					t.Fatal(err)
				}
				var reloaded core.ChatMessage
				if err := json.Unmarshal(raw, &reloaded); err != nil {
					t.Fatal(err)
				}
				req.Messages = append(req.Messages, reloaded, core.ChatMessage{Role: core.RoleTool, ToolCallID: "fixture-call", Content: "synthetic-result"})
				continuation := func(ctx context.Context, wantOpaque bool) {
					t.Helper()
					expectOpaque.Store(wantOpaque)
					result, err := provider.Chat(ctx, req)
					if err != nil || result.Message.Content != "done" {
						t.Fatalf("continuation failed: %+v, %v", result, err)
					}
				}
				continuation(context.Background(), true)
				cfg = manager.Draft()
				switch change {
				case "base-url":
					cfg.Endpoints[0].BaseURL = other.URL + "/origin"
				case "endpoint-id":
					cfg.Endpoints[0].ID = "other-endpoint"
					cfg.Models[0].EndpointID = "other-endpoint"
				case "path":
					cfg.Endpoints[0].BaseURL = server.URL + "/other"
				case "equivalent-base":
					version := "/v1/"
					if tc.format == api.Gemini {
						version = "/v1beta/"
					}
					cfg.Endpoints[0].BaseURL += version
				case "unscoped-history":
					// Match the fingerprint layout written before endpoint binding.
					legacy, err := json.Marshal(struct {
						Content any
						Calls   []core.ToolCall
					}{reloaded.Content, reloaded.ToolCalls})
					if err != nil {
						t.Fatal(err)
					}
					sum := sha256.Sum256(legacy)
					req.Messages[1].ProviderState.Fingerprint = hex.EncodeToString(sum[:])
				}
				publish()
				continuation(context.Background(), change == "equivalent-base")
				if change != "unscoped-history" {
					// A pinned run still belongs to the old endpoint after publication.
					continuation(pinned, true)
				}
			})
		}
	}
}
