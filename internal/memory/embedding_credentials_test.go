package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/provider/llm/openai"
)

func TestEmbeddingSQLiteCredentialRecovery(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		name := "after-failure"
		if inFlight {
			name = "in-flight"
		}
		t.Run(name, func(t *testing.T) {
			exerciseEmbeddingCredentialRecovery(t, func(handler http.HandlerFunc) embeddingFixture {
				return newSQLiteEmbeddingFixture(t, handler)
			}, inFlight)
		})
	}
}

func exerciseEmbeddingCredentialRecovery(t *testing.T, makeFixture func(http.HandlerFunc) embeddingFixture, inFlight bool) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var requests, reflections atomic.Int32
	f := makeFixture(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			reflections.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": `{"topics":[],"outdated_ids":[],"merges":[]}`}}}})
			return
		}
		if requests.Add(1) == 1 && inFlight {
			close(started)
			<-release
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer synthetic-valid-") {
			http.Error(w, "synthetic unauthorized", http.StatusUnauthorized)
			return
		}
		embeddingHandler(w, r)
	})
	// Use a real shared SQL catalog and a separate instance route manager. A
	// provider publication must not require republishing the instance bindings.
	catalog := modelrouter.NewSQL(f.manager.db, "")
	cfg := catalog.Draft()
	fixtureConfig := f.router.Active()
	cfg.Endpoints, cfg.Models = fixtureConfig.Endpoints, fixtureConfig.Models
	if err := catalog.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	setKey := func(key string, publish bool) {
		t.Helper()
		if _, err := catalog.SetDraftEndpointSecret("synthetic-endpoint", key); err != nil {
			t.Fatal(err)
		}
		if publish {
			if _, err := catalog.Publish(); err != nil {
				t.Fatal(err)
			}
		}
	}
	setKey("synthetic-invalid-first", true)
	f.router = modelrouter.NewSQL(f.manager.db, f.manager.instanceID)
	cfg = f.router.Draft()
	cfg.GlobalBindings = fixtureConfig.GlobalBindings
	if err := f.router.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := f.router.Publish(); err != nil {
		t.Fatal(err)
	}
	f.manager.router = f.router
	f.manager.reflector = NewReflector(f.store, NewCatalogStore(filepath.Join(t.TempDir(), "catalog.json")),
		f.router.Provider(modelrouter.WorkloadReflection, false, time.Second), "embed", DefaultConfig())
	f.manager.reflector.Scope = f.manager.Scope
	before := f.router.Snapshot()
	target, err := before.Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := modelrouter.EmbeddingFingerprint(target)
	routeRevision := f.router.Active().Revision
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "only", s.Owner, "warm tea")
	_, _ = f.manager.ensure(s)
	if inFlight {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("initial request did not start")
		}
	} else {
		f.manager.Wait()
		f.manager.mu.Lock()
		state := f.manager.states[s]
		var httpErr *openai.HTTPError
		failed := !state.Ready && state.RetryAt.IsZero() && errors.As(state.LastError, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized
		f.manager.mu.Unlock()
		if !failed {
			t.Fatal("initial unauthorized request did not leave a permanent failure")
		}
		for range 3 {
			f.manager.scan()
			f.manager.Wait()
		}
		if requests.Load() != 1 {
			t.Fatal("unchanged credentials repeatedly retried")
		}
		// Even a publication that fails to repair credentials gets only one
		// attempt; repeated scans must not turn a permanent error into a loop.
		setKey("synthetic-invalid-second", true)
		for range 3 {
			f.manager.scan()
			f.manager.Wait()
		}
		if requests.Load() != 2 {
			t.Fatalf("invalid credential publication attempts: %d", requests.Load())
		}
	}
	failedRequests := requests.Load()
	setKey("synthetic-valid-repaired", false)
	f.manager.scan()
	if !inFlight {
		f.manager.Wait()
	}
	if requests.Load() != failedRequests {
		t.Fatal("unpublished credential draft woke the failed index")
	}
	if _, err := catalog.Publish(); err != nil {
		t.Fatal(err)
	}
	after := f.router.Snapshot()
	target, err = after.Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{})
	if err != nil || modelrouter.EmbeddingFingerprint(target) != fingerprint ||
		f.router.Active().Revision != routeRevision || after.CatalogRevision() == before.CatalogRevision() {
		t.Fatal("credential-only publication changed vector identity or missed shared revision")
	}
	f.manager.scan()
	if inFlight {
		// The scan sees the new revision before the old 401 arrives. It must
		// leave that publication available to recover the failed old request.
		release <- struct{}{}
		f.manager.Wait()
		f.manager.scan()
	}
	f.manager.Wait()
	if requests.Load() != failedRequests+1 || reflections.Load() != 1 {
		t.Fatalf("recovery request/reflection counts: %d/%d", requests.Load(), reflections.Load())
	}
	entries, err := f.manager.Search(context.Background(), s, "beverage", 20)
	if err != nil || len(entries) != 1 || entries[0].ID != "only" {
		t.Fatalf("credential publication did not recover search: %v %v", entries, err)
	}
	f.manager.mu.Lock()
	generation := f.manager.states[s].Generation
	f.manager.mu.Unlock()
	readyRequests := requests.Load()
	setKey("synthetic-valid-rotated", true)
	for range 3 {
		f.manager.scan()
		f.manager.Wait()
	}
	f.manager.mu.Lock()
	state := f.manager.states[s]
	unchanged := state.Ready && state.LastError == nil && state.Generation == generation
	f.manager.mu.Unlock()
	if !unchanged || requests.Load() != readyRequests || reflections.Load() != 1 {
		t.Fatal("healthy credential rotation rebuilt the compatible index")
	}
	if entries, err := f.manager.Search(context.Background(), s, "beverage", 20); err != nil || len(entries) != 1 {
		t.Fatalf("rotated credentials lost usable index: %v %v", entries, err)
	}
}
