package memory

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/provider/llm/openai"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
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

func newSQLEmbeddingFixture(t *testing.T, db *storage.DB, handler http.HandlerFunc) embeddingFixture {
	t.Helper()
	const instanceID = "synthetic-recovery-instance"
	if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at)VALUES(?,?,'now')`), instanceID, instanceID); err != nil {
		t.Fatal(err)
	}
	f := newEmbeddingFixture(t, handler)
	f.store = NewSQLStore(db, instanceID)
	var err error
	f.manager, err = NewEmbeddings(f.manager.Scope, f.store, NewSQLGroupManager(db, instanceID, f.manager.Scope), f.router, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func newSQLiteEmbeddingFixture(t *testing.T, handler http.HandlerFunc) embeddingFixture {
	t.Helper()
	root := t.TempDir()
	db, err := storage.OpenWithConfig(context.Background(), root, storage.Config{Backend: storage.SQLite, DSN: filepath.Join(root, "memory.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newSQLEmbeddingFixture(t, db, handler)
}

func TestEmbeddingSQLitePartialChanges(t *testing.T) {
	exerciseEmbeddingPartialChanges(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLiteEmbeddingFixture(t, handler)
	})
}

func exerciseEmbeddingPartialChanges(t *testing.T, makeFixture func(http.HandlerFunc) embeddingFixture) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var block atomic.Bool
	f := makeFixture(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "cold edited summary"}}}})
			return
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), "标签") && block.CompareAndSwap(true, false) {
			close(started)
			<-release
		}
		embeddingHandler(w, r)
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "stable", s.Owner, "warm tea")
	seedEmbeddingMemory(t, f.store, "edited", s.Owner, "warm old content")
	rebuildFixture(t, f, s)
	block.Store(true)
	if err := f.store.UpdateEntry(MemoryEntry{ID: "edited", Content: "cold edited"}); err != nil {
		t.Fatal(err)
	}
	seedEmbeddingMemory(t, f.store, "added", s.Owner, "cold new content")
	f.manager.scan()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("partial changes did not schedule rebuild")
	}
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 1 || entries[0].ID != "stable" {
		t.Fatalf("valid old index unavailable during rebuild: %v %v", entries, err)
	}
	// Release once here; the defer also releases on an earlier test failure.
	release <- struct{}{}
	f.manager.Wait()
	entries, err = f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 3 {
		t.Fatalf("partial changes not repaired: %v %v", entries, err)
	}
	for _, e := range entries {
		if e.ID == "edited" && e.Content != "cold edited" {
			t.Fatalf("edited memory still stale: %+v", e)
		}
	}
}

func TestEmbeddingSQLiteTransientRecovery(t *testing.T) {
	exerciseEmbeddingTransientRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLiteEmbeddingFixture(t, handler)
	})
}

func exerciseEmbeddingTransientRecovery(t *testing.T, makeFixture func(http.HandlerFunc) embeddingFixture) {
	t.Helper()
	var fail atomic.Bool
	var requests, reflections atomic.Int32
	fail.Store(true)
	f := makeFixture(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			reflections.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": `{"topics":[],"outdated_ids":[],"merges":[]}`}}}})
			return
		}
		requests.Add(1)
		if fail.Load() {
			http.Error(w, "synthetic temporary outage", http.StatusServiceUnavailable)
			return
		}
		embeddingHandler(w, r)
	})
	f.manager.reflector = NewReflector(f.store, NewCatalogStore(filepath.Join(t.TempDir(), "catalog.json")),
		f.router.Provider(modelrouter.WorkloadReflection, false, time.Second), "embed", DefaultConfig())
	f.manager.reflector.Scope = f.manager.Scope
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "only", s.Owner, "warm tea")
	_, _ = f.manager.ensure(s)
	f.manager.Wait()
	if requests.Load() != 3 || reflections.Load() != 1 {
		t.Fatalf("initial request budget/reflection: %d/%d", requests.Load(), reflections.Load())
	}
	fail.Store(false)
	f.manager.scan()
	f.manager.Wait()
	if requests.Load() != 3 {
		t.Fatal("scan retried before backoff elapsed")
	}
	f.manager.mu.Lock()
	state := f.manager.states[s]
	if state.Ready || state.RetryAt.IsZero() || state.Failures != 1 {
		t.Fatalf("failure not scheduled for recovery: %+v", state)
	}
	state.RetryAt = time.Now().Add(-time.Second)
	f.manager.mu.Unlock()
	f.manager.scan()
	f.manager.Wait()
	if requests.Load() != 4 || reflections.Load() != 1 {
		t.Fatalf("recovery repeated requests/reflection: %d/%d", requests.Load(), reflections.Load())
	}
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 1 {
		t.Fatalf("outage did not self-heal: %v %v", entries, err)
	}
}

func TestEmbeddingPermanentFailureDoesNotScanRetry(t *testing.T) {
	var requests atomic.Int32
	f := newEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "synthetic invalid model", http.StatusBadRequest)
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "only", s.Owner, "warm tea")
	_, _ = f.manager.ensure(s)
	f.manager.Wait()
	for range 3 {
		f.manager.scan()
		f.manager.Wait()
	}
	if requests.Load() != 1 {
		t.Fatalf("permanent error repeatedly retried: %d", requests.Load())
	}
}

func TestEmbeddingSQLiteDimensionRecovery(t *testing.T) {
	for _, allPrivate := range []bool{false, true} {
		t.Run(fmt.Sprint("all-private-", allPrivate), func(t *testing.T) {
			exerciseEmbeddingDimensionRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
				return newSQLiteEmbeddingFixture(t, handler)
			}, allPrivate)
		})
	}
}

func exerciseEmbeddingDimensionRecovery(t *testing.T, makeFixture func(http.HandlerFunc) embeddingFixture, allPrivate bool) {
	t.Helper()
	var dimensions atomic.Int32
	dimensions.Store(2)
	f := makeFixture(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := []map[string]any{}
		for i := range req.Input {
			vector := make([]float32, int(dimensions.Load()))
			vector[0] = 1
			data = append(data, map[string]any{"index": i, "embedding": vector})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "first", s.Owner, "warm tea")
	rebuildFixture(t, f, s)
	expected := 1
	if allPrivate {
		seedEmbeddingMemory(t, f.store, "second", "synthetic-other-owner", "cold water")
		rebuildFixture(t, f, SearchScope{Type: ScopePrivate, Owner: "synthetic-other-owner"})
		s.Owner, expected = "", 2
	}
	if f.manager.vectorType != "" {
		// Reload only persisted hashes, as on PostgreSQL cold startup.
		fresh, err := NewEmbeddings(f.manager.Scope, f.store, f.manager.groups, f.router, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.manager = fresh
	}
	dimensions.Store(3)
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if !errors.Is(err, ErrEmbeddingNotReady) || !errors.Is(err, errEmbeddingDimensionsChanged) || len(entries) != 0 {
		t.Fatalf("dimension change silently returned results: %v %v", entries, err)
	}
	f.manager.Wait()
	entries, err = f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != expected {
		t.Fatalf("same-name dimension change not recovered: %v %v", entries, err)
	}
}

func TestEmbeddingStaleQueryCannotInvalidateNewIndex(t *testing.T) {
	exerciseEmbeddingStaleQuery(t, func(handler http.HandlerFunc) embeddingFixture {
		return newSQLiteEmbeddingFixture(t, handler)
	})
}

func exerciseEmbeddingStaleQuery(t *testing.T, makeFixture func(http.HandlerFunc) embeddingFixture) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var dimensions atomic.Int32
	var pause atomic.Bool
	dimensions.Store(2)
	f := makeFixture(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		vector := make([]float32, int(dimensions.Load()))
		vector[0] = 1
		if len(req.Input) == 1 && req.Input[0] == "beverage" && pause.CompareAndSwap(true, false) {
			close(started)
			<-release
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": vector}}})
	})
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "only", s.Owner, "warm tea")
	rebuildFixture(t, f, s)
	pause.Store(true)
	queryDone := make(chan error, 1)
	go func() {
		_, err := f.manager.Search(context.Background(), s, "beverage", 20)
		queryDone <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("old query did not start")
	}
	dimensions.Store(3)
	rebuildFixture(t, f, s)
	release <- struct{}{}
	if err := <-queryDone; !errors.Is(err, ErrEmbeddingNotReady) {
		t.Fatalf("old query did not reject incompatible result: %v", err)
	}
	f.manager.mu.Lock()
	ready, active := f.manager.states[s].Ready, f.manager.states[s].Active
	f.manager.mu.Unlock()
	if !ready || active {
		t.Fatal("old query invalidated the completed new index")
	}
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 1 {
		t.Fatalf("new index was not usable: %v %v", entries, err)
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

func TestEmbeddingWrappedNetworkFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		transient bool
	}{
		{"certificate verification", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false},
		{"certificate under dial error", &net.OpError{Op: "dial", Err: x509.HostnameError{}}, false},
		{"unsupported protocol", errors.New("unsupported protocol scheme"), false},
		{"invalid address", &net.AddrError{Err: "invalid address"}, false},
		{"missing DNS name", &net.DNSError{IsNotFound: true}, false},
		{"cancelled dial", &net.OpError{Op: "dial", Err: context.Canceled}, false},
		{"connection failure", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"timeout", context.DeadlineExceeded, true},
		{"closed connection", io.EOF, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := &url.Error{Op: "Post", URL: "https://example.invalid/embeddings", Err: test.err}
			if got := transientEmbeddingError(wrapped); got != test.transient {
				t.Fatalf("transient classification: %v, want %v", got, test.transient)
			}
			if !test.transient {
				provider := &retryEmbeddingProvider{Err: wrapped}
				_, _ = embedRetry(context.Background(), provider, core.EmbeddingRequest{})
				if provider.Calls != 1 {
					t.Fatalf("permanent transport error retried: %d", provider.Calls)
				}
			}
		})
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
