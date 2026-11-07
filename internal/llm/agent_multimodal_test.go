package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

func TestExtractLastUserMessageContentParts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content any
		want    string
	}{
		{"plain", "current text", "current text"},
		{"mixed", []core.ContentPart{{Type: "text", Text: "first"}, {Type: "image_url", Text: "ignore image metadata", ImageURL: &core.ImageURL{URL: "data:image/gif;base64,R0lGODlh"}}, {Type: "text", Text: "second"}}, "first\nsecond"},
		{"image only", []core.ContentPart{{Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/gif;base64,R0lGODlh"}}}, ""},
		{"empty", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []ChatMessage{{Role: "user", Content: "stale query"}, {Role: "user", Content: tc.content}, {Role: "assistant", Content: "answer"}}
			if got := extractLastUserMessage(messages); got != tc.want {
				t.Fatalf("query=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestEngineMultimodalRecallAndStickerNotice(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "10")
	t.Setenv(sticker.ActivityModeEnvKey, sticker.ActivityBotTurns)
	for _, group := range []bool{false, true} {
		name := "private"
		if group {
			name = "group"
		}
		t.Run(name, func(t *testing.T) {
			queries := make(chan []string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Input []string `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				queries <- req.Input
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{1, 0}}}})
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			scope := runtimescope.New(nil, nil, nil)
			t.Cleanup(func() { scope.Cancel(); scope.Wait() })
			router := modelrouter.New(filepath.Join(dir, "router.json"))
			cfg := router.Draft()
			cfg.Endpoints = []modelrouter.Endpoint{{ID: "endpoint", DisplayName: "Endpoint", BaseURL: server.URL + "/v1", Enabled: true}}
			cfg.Models = []modelrouter.Model{{ID: "embedding", DisplayName: "Embedding", EndpointID: "endpoint", UpstreamModel: "synthetic-embedding", Enabled: true, Capabilities: []string{"embedding"}}}
			cfg.GlobalBindings[modelrouter.WorkloadEmbedding] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "embedding"}
			if err := router.SaveDraft(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := router.Publish(); err != nil {
				t.Fatal(err)
			}
			store := memory.NewStore(filepath.Join(dir, "brain.json"))
			groups := memory.NewGroupManager(dir, scope)
			entry := memory.MemoryEntry{ID: "synthetic-memory", Owner: "synthetic-owner", Content: "recalled synthetic fact", Source: memory.SourceManual}
			searchScope := memory.SearchScope{Type: memory.ScopePrivate, Owner: entry.Owner}
			runCtx := RunContext{Owner: entry.Owner}
			if group {
				searchScope = memory.SearchScope{Type: memory.ScopeGroup, Platform: "synthetic-platform", GroupID: "synthetic-group"}
				runCtx.Owner, runCtx.OwnerType = "group:synthetic-group", memory.OwnerGroup
				runCtx.RouteScope = modelrouter.Scope{Platform: searchScope.Platform, GroupID: searchScope.GroupID}
				groupStore, err := groups.GetGroupStoreForPlatform(searchScope.Platform, searchScope.GroupID)
				if err != nil {
					t.Fatal(err)
				}
				if err := groupStore.Save(entry); err != nil {
					t.Fatal(err)
				}
			} else if err := store.Save(entry); err != nil {
				t.Fatal(err)
			}
			embeddings, err := memory.NewEmbeddings(scope, store, groups, router, nil)
			if err != nil {
				t.Fatal(err)
			}
			embeddings.Update(searchScope)
			embeddings.Wait()
			for len(queries) > 0 {
				<-queries
			}
			provider := &mockTraceProvider{}
			engine := &Engine{Provider: provider, MemoryReader: memory.NewReader(store, 5), MemoryGateway: memory.NewGateway(), MemoryEmbeddings: embeddings, GroupManager: groups}
			parts := []core.ContentPart{
				{Type: "text", Text: "compare image"},
				{Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/gif;base64,R0lGODlh"}},
				{Type: "text", Text: "with synthetic memory"},
			}
			original := append([]core.ContentPart(nil), parts...)
			result := engine.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: parts}}, runCtx)
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			select {
			case got := <-queries:
				if !reflect.DeepEqual(got, []string{"compare image\nwith synthetic memory"}) {
					t.Fatalf("embedding query=%q", got)
				}
			default:
				t.Fatal("multimodal turn skipped embedding search")
			}
			messages := provider.lastRequest.Messages
			system, _ := messages[0].Content.(string)
			if !strings.Contains(system, entry.Content) {
				t.Fatalf("recalled memory missing: %s", system)
			}
			got, ok := messages[len(messages)-1].Content.([]core.ContentPart)
			if !ok || len(got) != len(parts) {
				t.Fatalf("lost multimodal content: %+v", messages)
			}
			if got[0].Text != engine.stickerActivityPrompt()+"\n\n"+original[0].Text || !reflect.DeepEqual(got[1:], original[1:]) {
				t.Fatalf("notice missing or image/text changed: %+v", got)
			}
			if !reflect.DeepEqual(parts, original) {
				t.Fatalf("caller content mutated: %+v", parts)
			}
		})
	}
}
