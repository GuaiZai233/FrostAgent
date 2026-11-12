package modelrouter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestEndpointFormatsPersistAndRoute(t *testing.T) {
	for _, format := range []api.Format{api.Legacy, api.ChatCompletions, api.Responses, api.Anthropic, api.Gemini, api.Custom} {
		t.Run(string(format), func(t *testing.T) {
			wantPath := "/v1/chat/completions"
			body := `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`
			switch format {
			case api.Legacy:
				wantPath = "/chat/completions"
			case api.Responses:
				wantPath = "/v1/responses"
				body = `{"output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`
			case api.Anthropic:
				wantPath = "/v1/messages"
				body = `{"content":[{"type":"text","text":"done"}]}`
			case api.Gemini:
				wantPath = "/v1beta/models/test-model:generateContent"
				body = `{"candidates":[{"content":{"parts":[{"text":"done"}]}}]}`
			case api.Custom:
				wantPath = "/exact"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != wantPath {
					t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
				}
				if format == api.Custom && r.URL.RawQuery != "route=fixture" {
					t.Errorf("lost custom query: %s", r.URL.RawQuery)
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			manager := New(filepath.Join(t.TempDir(), "router.json"))
			cfg := manager.Draft()
			base := server.URL
			if format == api.Custom {
				base += "/exact?route=fixture"
			}
			cfg.Endpoints = []Endpoint{{ID: "fixture-endpoint", DisplayName: "Fixture", BaseURL: base, APIFormat: string(format), Enabled: true}}
			cfg.Models = []Model{{ID: "fixture-model", DisplayName: "Fixture", EndpointID: "fixture-endpoint", UpstreamModel: "test-model", Enabled: true}}
			cfg.GlobalBindings[WorkloadDialogue] = Binding{Mode: BindingModel, ModelID: "fixture-model"}
			if err := manager.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, _, err := manager.TestModel("fixture-model"); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Publish(); err != nil {
				t.Fatal(err)
			}
			manager = New(manager.path)
			result, err := manager.Provider(WorkloadDialogue, false, 0).Chat(context.Background(), core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser, Content: "hello"}}})
			if err != nil || result.Message.Content != "done" || calls != 2 {
				t.Fatalf("route result: %+v, %v; calls=%d", result, err, calls)
			}
			target, err := manager.Snapshot().Resolve(WorkloadDialogue, Scope{})
			if err != nil || target.APIFormat != string(format) {
				t.Fatalf("target lost format: %+v %v", target, err)
			}
			if format == api.Custom {
				if _, err := manager.ListUpstreamModels("fixture-endpoint"); err == nil || !strings.Contains(err.Error(), "自定义") {
					t.Errorf("custom listing: %v", err)
				}
			}
		})
	}
}

func TestEndpointFormatsSQLRoundTrip(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := catalogTestDB(t, backend)
			catalog := NewSQL(db, "")
			cfg := catalog.Draft()
			for i, format := range []api.Format{api.Legacy, api.ChatCompletions, api.Responses, api.Anthropic, api.Gemini, api.Custom} {
				cfg.Endpoints = append(cfg.Endpoints, Endpoint{ID: fmt.Sprintf("endpoint-%d", i), DisplayName: fmt.Sprintf("Endpoint %d", i), BaseURL: "https://model.invalid", APIFormat: string(format), Enabled: true})
			}
			if err := catalog.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
			reopened := NewSQL(db, "")
			if err := reopened.LoadError(); err != nil {
				t.Fatal(err)
			}
			got := reopened.Active()
			for i, endpoint := range got.Endpoints {
				if endpoint.APIFormat != cfg.Endpoints[i].APIFormat {
					t.Errorf("format changed: %+v", endpoint)
				}
			}
			tx, err := db.SQL.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			imported, err := readCatalogTx(context.Background(), tx)
			if err != nil {
				t.Fatal(err)
			}
			for i, endpoint := range imported.Endpoints {
				if endpoint.APIFormat != cfg.Endpoints[i].APIFormat {
					t.Errorf("restore lost format: %+v", endpoint)
				}
			}
		})
	}
}

func TestEndpointModelsProtocol(t *testing.T) {
	for _, format := range []api.Format{api.ChatCompletions, api.Responses, api.Anthropic, api.Gemini} {
		t.Run(string(format), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/v1/models"
				if format == api.Gemini {
					want = "/v1beta/models"
				}
				if r.URL.Path != want {
					t.Errorf("model listing path = %s", r.URL.Path)
				}
				if format == api.Gemini {
					fmt.Fprint(w, `{"models":[{"name":"models/z-model"},{"name":"models/a-model"}]}`)
				} else {
					fmt.Fprint(w, `{"data":[{"id":"z-model"},{"id":"a-model"}]}`)
				}
			}))
			defer server.Close()
			manager := New(filepath.Join(t.TempDir(), "router.json"))
			cfg := manager.Draft()
			cfg.Endpoints = []Endpoint{{ID: "fixture", DisplayName: "Fixture", BaseURL: server.URL, APIFormat: string(format)}}
			if err := manager.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			models, err := manager.ListUpstreamModels("fixture")
			if err != nil || strings.Join(models, ",") != "a-model,z-model" {
				t.Fatalf("models = %v %v", models, err)
			}
		})
	}
}
