package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/runtimescope"
)

type embeddingFixture struct {
	manager *Embeddings
	store   *Store
	router  *modelrouter.Manager
}

func newEmbeddingFixture(t *testing.T, handler http.HandlerFunc) embeddingFixture {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	root := t.TempDir()
	router := modelrouter.New(filepath.Join(root, "router.json"))
	cfg := router.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: "synthetic-endpoint", DisplayName: "Test", BaseURL: server.URL + "/v1", Enabled: true}}
	cfg.Models = []modelrouter.Model{{ID: "synthetic-model", DisplayName: "Embedding", EndpointID: "synthetic-endpoint", UpstreamModel: "embed", Enabled: true, Capabilities: []string{"embedding"}}}
	cfg.GlobalBindings[modelrouter.WorkloadEmbedding] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "synthetic-model"}
	cfg.GlobalBindings[modelrouter.WorkloadReflection] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "synthetic-model"}
	if err := router.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Publish(); err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(nil, nil, nil)
	t.Cleanup(func() { scope.Cancel(); scope.Wait() })
	store := NewStore(filepath.Join(root, "brain.json"))
	groups := NewGroupManager(root, scope)
	manager, err := NewEmbeddings(scope, store, groups, router, nil)
	if err != nil {
		t.Fatal(err)
	}
	return embeddingFixture{manager, store, router}
}
func embeddingHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input []string `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	data := []map[string]any{}
	for i, text := range req.Input {
		vector := []float32{1, 0}
		if strings.Contains(text, "cold") {
			vector = []float32{0, 1}
		}
		data = append(data, map[string]any{"index": i, "embedding": vector})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func seedEmbeddingMemory(t *testing.T, store *Store, id, owner, content string) {
	t.Helper()
	now := time.Now()
	if err := store.Save(MemoryEntry{ID: id, Owner: owner, Content: content, Tags: []string{"topic"}, Source: SourceManual, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
}
func rebuildFixture(t *testing.T, f embeddingFixture, s SearchScope) {
	t.Helper()
	f.manager.Update(s)
	f.manager.Wait()
	f.manager.mu.Lock()
	state := f.manager.states[s.normalized()]
	f.manager.mu.Unlock()
	if state == nil || !state.Ready || state.LastError != nil {
		t.Fatalf("index failed: %+v", state)
	}
}

func TestEmbeddingIsolationAndContentFreshness(t *testing.T) {
	f := newEmbeddingFixture(t, embeddingHandler)
	seedEmbeddingMemory(t, f.store, "own-memory", "synthetic-owner", "warm tea")
	for i := range 25 {
		seedEmbeddingMemory(t, f.store, fmt.Sprint("other-", i), "other-owner", "warm tea")
	}
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	rebuildFixture(t, f, s)
	entries, err := f.manager.Search(context.Background(), s, "hot beverage", 1)
	if err != nil || len(entries) != 1 || entries[0].ID != "own-memory" {
		t.Fatalf("scope crowded out: %v %v", entries, err)
	}
	if err := f.store.IncrementAccessCount("own-memory"); err != nil {
		t.Fatal(err)
	}
	entries, err = f.manager.Search(context.Background(), s, "hot beverage", 1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("recall invalidated vector: %v %v", entries, err)
	}
	if err := f.store.UpdateEntry(MemoryEntry{ID: "own-memory", Content: "cold water", Tags: []string{"topic"}}); err != nil {
		t.Fatal(err)
	}
	entries, err = f.manager.Search(context.Background(), s, "hot beverage", 1)
	if (err != nil && !errors.Is(err, ErrEmbeddingNotReady)) || len(entries) != 0 {
		t.Fatalf("stale content recalled: %v %v", entries, err)
	}
	rebuildFixture(t, f, s)
	if err := f.store.Delete("own-memory"); err != nil {
		t.Fatal(err)
	}
	entries, err = f.manager.Search(context.Background(), s, "cold water", 1)
	if err != nil || len(entries) != 0 {
		t.Fatalf("deleted memory recalled: %v %v", entries, err)
	}
}

func TestEmbeddingHotReloadRestartsCancelledFirstBuild(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		embeddingHandler(w, r)
	})
	seedEmbeddingMemory(t, f.store, "first", "synthetic-owner", "warm tea")
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	if _, err := f.manager.ensure(s); !errors.Is(err, ErrEmbeddingNotReady) {
		t.Fatalf("initial index state: %v", err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("initial build did not start")
	}
	f.manager.Cancel()
	f.manager.Wait()
	close(release)
	nextScope := runtimescope.New(nil, nil, nil)
	t.Cleanup(func() { nextScope.Cancel(); nextScope.Wait() })
	next, err := NewEmbeddings(nextScope, f.store, f.manager.groups, f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	next.CarryIndexesFrom(f.manager)
	if _, err := next.ensure(s); !errors.Is(err, ErrEmbeddingNotReady) {
		t.Fatalf("restarted index state: %v", err)
	}
	next.Wait()
	entries, err := next.Search(context.Background(), s, "warm tea", 20)
	if err != nil || len(entries) != 1 || entries[0].ID != "first" {
		t.Fatalf("hot reload did not recover initial index: %v %v", entries, err)
	}
}

func TestEmbeddingCompletePublicationAndFailure(t *testing.T) {
	var fail atomic.Bool
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "synthetic failure", http.StatusBadRequest)
			return
		}
		embeddingHandler(w, r)
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "first", "synthetic-owner", "warm tea")
	rebuildFixture(t, f, s)
	seedEmbeddingMemory(t, f.store, "second", "synthetic-owner", "cold water")
	fail.Store(true)
	f.manager.Update(s)
	f.manager.Wait()
	f.manager.mu.Lock()
	state := f.manager.states[s.normalized()]
	if len(state.Vectors) != 1 || state.Vectors["first"].Hash == "" || state.LastError == nil {
		t.Fatalf("failed update replaced old index: %+v", state)
	}
	f.manager.mu.Unlock()
	fail.Store(false)
	entries, err := f.manager.Search(context.Background(), s, "warm tea", 20)
	if err != nil || len(entries) != 1 || entries[0].ID != "first" {
		t.Fatalf("partial publication: %v %v", entries, err)
	}
	rebuildFixture(t, f, s)
	entries, err = f.manager.Search(context.Background(), s, "warm tea", 20)
	if err != nil || len(entries) != 2 {
		t.Fatalf("complete index missing: %v %v", entries, err)
	}
}

func TestEmbeddingSkippedInputKeepsOriginalMemory(t *testing.T) {
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Input[0], "oversized") {
			http.Error(w, "context_length_exceeded", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float32{1, 0}}}})
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "large", "synthetic-owner", "oversized original body")
	seedEmbeddingMemory(t, f.store, "small", "synthetic-owner", "tea")
	rebuildFixture(t, f, s)
	all, _ := f.store.ListAll()
	if len(all) != 2 {
		t.Fatal("oversized memory was deleted")
	}
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 1 || entries[0].ID != "small" {
		t.Fatalf("skipped input blocked other entries: %v %v", entries, err)
	}
}

type retryEmbeddingProvider struct {
	Calls int
	Err   error
}

func (p *retryEmbeddingProvider) Embed(context.Context, core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	p.Calls++
	return nil, p.Err
}
func TestEmbeddingRetryBudgetAndCancellation(t *testing.T) {
	provider := &retryEmbeddingProvider{Err: &openai.HTTPError{StatusCode: 429}}
	_, err := embedRetry(context.Background(), provider, core.EmbeddingRequest{})
	if err == nil || provider.Calls != 3 {
		t.Fatalf("retry count: %d %v", provider.Calls, err)
	}
	provider = &retryEmbeddingProvider{Err: &openai.HTTPError{StatusCode: 400}}
	_, _ = embedRetry(context.Background(), provider, core.EmbeddingRequest{})
	if provider.Calls != 1 {
		t.Fatal("permanent error retried")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider = &retryEmbeddingProvider{Err: context.Canceled}
	_, err = embedRetry(ctx, provider, core.EmbeddingRequest{})
	if !errors.Is(err, context.Canceled) || provider.Calls != 1 {
		t.Fatalf("cancelled retry: %d %v", provider.Calls, err)
	}
}

func TestEmbeddingSummaryRefreshUsesNewContent(t *testing.T) {
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "new content summary"}}}})
			return
		}
		embeddingHandler(w, r)
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "edited", "synthetic-owner", "old content")
	f.store.refreshEditedSummaries = true
	if err := f.store.UpdateEntry(MemoryEntry{ID: "edited", Content: "new content"}); err != nil {
		t.Fatal(err)
	}
	all, _ := f.store.ListAll()
	if !all[0].SummaryNeedsRefresh || all[0].Summary != "" {
		t.Fatal("old summary retained")
	}
	rebuildFixture(t, f, s)
	all, _ = f.store.ListAll()
	if all[0].SummaryNeedsRefresh || all[0].Summary != "new content summary" {
		t.Fatalf("summary not regenerated: %+v", all[0])
	}
}

func TestEmbeddingCancelDuringSummaryKeepsDirtySource(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			close(started)
			<-release
			return
		}
		embeddingHandler(w, r)
	})
	seedEmbeddingMemory(t, f.store, "edited", "synthetic-owner", "old")
	f.store.refreshEditedSummaries = true
	if err := f.store.UpdateEntry(MemoryEntry{ID: "edited", Content: "new"}); err != nil {
		t.Fatal(err)
	}
	f.manager.Update(SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("summary request not started")
	}
	f.manager.Cancel()
	f.manager.Wait()
	entries, _ := f.store.ListAll()
	if len(entries) != 1 || !entries[0].SummaryNeedsRefresh || entries[0].Summary != "" {
		t.Fatalf("cancelled worker committed summary: %+v", entries)
	}
}

func TestEmbeddingLegacyCompactCannotCrowdTopK(t *testing.T) {
	f := newEmbeddingFixture(t, embeddingHandler)
	now := time.Now()
	for i := range 25 {
		if err := f.store.Save(MemoryEntry{ID: fmt.Sprint("compact-", i), Owner: "synthetic-owner", Content: "warm", Source: SourceCompact, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	seedEmbeddingMemory(t, f.store, "valid", "synthetic-owner", "cold valid fact")
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	rebuildFixture(t, f, s)
	entries, err := f.manager.Search(context.Background(), s, "warm", 1)
	if err != nil || len(entries) != 1 || entries[0].ID != "valid" {
		t.Fatalf("compact crowded valid fact: %v %v", entries, err)
	}
}

func TestEmbeddingNoUsableIndexStartsRebuild(t *testing.T) {
	f := newEmbeddingFixture(t, embeddingHandler)
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "only", "synthetic-owner", "warm")
	rebuildFixture(t, f, s)
	if err := f.store.UpdateEntry(MemoryEntry{ID: "only", Content: "cold"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.ensure(s); !errors.Is(err, ErrEmbeddingNotReady) {
		t.Fatalf("unusable index reported ready: %v", err)
	}
	f.manager.Wait()
	entries, err := f.manager.Search(context.Background(), s, "cold", 20)
	if err != nil || len(entries) != 1 || entries[0].Content != "cold" {
		t.Fatalf("unusable index not recovered: %v %v", entries, err)
	}
}

func TestEmbeddingEditWhileDisabledRefreshesSummaryOnReenable(t *testing.T) {
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "cold water summary"}}}})
			return
		}
		embeddingHandler(w, r)
	})
	now := time.Now()
	if err := f.store.Save(MemoryEntry{ID: "edited", Owner: "synthetic-owner", Content: "warm tea", Summary: "warm tea summary", Source: SourceManual, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	rebuildFixture(t, f, s)
	f.store.refreshEditedSummaries = false
	if err := f.store.UpdateEntry(MemoryEntry{ID: "edited", Content: "cold water"}); err != nil {
		t.Fatal(err)
	}
	all, _ := f.store.ListAll()
	if !all[0].SummaryNeedsRefresh {
		t.Fatal("disabled edit did not retain summary invalidation")
	}
	f.store.refreshEditedSummaries = true
	rebuildFixture(t, f, s)
	all, _ = f.store.ListAll()
	if all[0].Summary != "cold water summary" || all[0].SummaryNeedsRefresh {
		t.Fatalf("stale summary reused: %+v", all[0])
	}
}
