package modelcatalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

const sampleList = `{
  "openai/gpt-4o": {"id":"openai/gpt-4o","tool_call":true,"modalities":{"input":["text","image"],"output":["text"]}},
  "vendor-a/tiny-model": {"id":"vendor-a/tiny-model","tool_call":false,"modalities":{"input":["text"],"output":["text"]}},
  "vendor-a/shared": {"id":"vendor-a/shared","tool_call":true,"modalities":{"input":["text"],"output":["text"]}},
  "vendor-b/shared": {"id":"vendor-b/shared","tool_call":false,"modalities":{"input":["text"],"output":["text"]}}
}`

func mustParse(t *testing.T, raw string) *index {
	t.Helper()
	idx, err := parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return idx
}

func TestLookupFullAndBareIdentifiers(t *testing.T) {
	idx := mustParse(t, sampleList)

	caps, found := idx.lookup("openai/gpt-4o")
	if !found || !reflect.DeepEqual(caps, []string{"text", "tools", "vision"}) {
		t.Fatalf("full id lookup = %v, %v", caps, found)
	}

	// A bare name is matched against the last path segment.
	caps, found = idx.lookup("gpt-4o")
	if !found || !reflect.DeepEqual(caps, []string{"text", "tools", "vision"}) {
		t.Fatalf("bare lookup = %v, %v", caps, found)
	}

	caps, found = idx.lookup("  Tiny-Model ")
	if !found || !reflect.DeepEqual(caps, []string{"text"}) {
		t.Fatalf("case/space-insensitive lookup = %v, %v", caps, found)
	}
}

func TestLookupOmitsCapabilitiesWhenMatchesDisagree(t *testing.T) {
	idx := mustParse(t, sampleList)
	// vendor-a says tools, vendor-b does not: tools must not be ticked.
	caps, found := idx.lookup("shared")
	if !found || !reflect.DeepEqual(caps, []string{"text"}) {
		t.Fatalf("disagreeing lookup = %v, %v", caps, found)
	}
}

func TestLookupMissesUnknownAndQualifiedNames(t *testing.T) {
	idx := mustParse(t, sampleList)
	for _, name := range []string{"", "   ", "no-such-model", "unknown-vendor/gpt-4o"} {
		if caps, found := idx.lookup(name); found {
			t.Fatalf("lookup(%q) = %v, want miss", name, caps)
		}
	}
}

func TestParseRejectsEmptyList(t *testing.T) {
	if _, err := parse([]byte(`{}`)); err == nil {
		t.Fatal("empty list must be rejected")
	}
	if _, err := parse([]byte(`not json`)); err == nil {
		t.Fatal("invalid json must be rejected")
	}
}

func TestRefreshFailureKeepsPreviousSnapshotAndCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "models_dev_cache.json")
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(sampleList))
	}))
	defer srv.Close()

	c := New(srv.URL, cachePath, time.Hour)
	c.refresh(context.Background())
	if _, found := c.current.Load().lookup("gpt-4o"); !found {
		t.Fatal("successful refresh did not populate the snapshot")
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	fail.Store(true)
	c.refresh(context.Background())
	if _, found := c.current.Load().lookup("gpt-4o"); !found {
		t.Fatal("failed refresh discarded the previous snapshot")
	}

	// A fresh process starts from the cache when the network is unavailable.
	restarted := New(srv.URL, cachePath, time.Hour)
	if err := restarted.loadCache(); err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if _, found := restarted.current.Load().lookup("gpt-4o"); !found {
		t.Fatal("cache did not restore the snapshot")
	}
}

func TestNoCacheAndFailedFetchInfersNothing(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New(srv.URL, filepath.Join(dir, "missing.json"), time.Hour)
	if err := c.loadCache(); !os.IsNotExist(err) {
		t.Fatalf("loadCache on missing file = %v, want not-exist", err)
	}
	c.refresh(context.Background())
	if c.current.Load() != nil {
		t.Fatal("snapshot must stay empty when there is no cache and fetch fails")
	}
	active.Store(c)
	t.Cleanup(func() { active.Store(nil) })
	if caps, found := Infer("gpt-4o"); found || caps != nil {
		t.Fatalf("Infer without data = %v, %v", caps, found)
	}
}
