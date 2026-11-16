package memory

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/api"
)

func TestEmbeddingChatFormatChangeKeepsReadyIndex(t *testing.T) {
	var requests atomic.Int32
	f := newSQLiteEmbeddingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("embedding path = %s", r.URL.Path)
		}
		embeddingHandler(w, r)
	})
	publish := func(format api.Format) {
		t.Helper()
		cfg := f.router.Draft()
		cfg.Endpoints[0].APIFormat = string(format)
		if err := f.router.SaveDraft(cfg); err != nil {
			t.Fatal(err)
		}
		if _, err := f.router.Publish(); err != nil {
			t.Fatal(err)
		}
	}
	publish(api.ChatCompletions)
	s := SearchScope{Type: ScopePrivate, Owner: "synthetic-owner"}
	seedEmbeddingMemory(t, f.store, "synthetic-memory", s.Owner, "warm tea")
	rebuildFixture(t, f, s)
	f.manager.mu.Lock()
	generation := f.manager.states[s].Generation
	f.manager.mu.Unlock()
	before := requests.Load()
	for _, format := range []api.Format{api.Responses, api.ChatCompletions, api.Legacy} {
		publish(format)
		_, err := f.manager.ensure(s)
		f.manager.Wait()
		if err != nil {
			t.Fatalf("chat format change made index unavailable: %v", err)
		}
		f.manager.mu.Lock()
		state := f.manager.states[s]
		unchanged := state.Ready && state.Generation == generation && !state.Pending && len(state.Vectors) == 1
		f.manager.mu.Unlock()
		if !unchanged {
			t.Fatal("chat format change invalidated ready vectors")
		}
		if got := requests.Load(); got != before {
			t.Fatalf("chat format change triggered rebuild: %d -> %d", before, got)
		}
	}
}
