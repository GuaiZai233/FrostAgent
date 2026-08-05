package memory

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestValidateReflectionMerges(t *testing.T) {
	entries := []MemoryEntry{
		{ID: "mem_001", Owner: "alice", Content: "用户喜欢打舞萌"},
		{ID: "mem_002", Owner: "alice", Content: "用户的舞萌 dx rating 为 w6"},
		{ID: "mem_003", Owner: "alice", Content: "用户喜欢音游"},
	}

	t.Run("accepts a bounded non-conflicting merge", func(t *testing.T) {
		merges, protected, rejected := validateReflectionMerges(
			entries,
			[]reflectMerge{{
				SourceIDs: []string{"mem_001", "mem_002"},
				Content:   "用户是 dx rating 为 w6 的舞萌爱好者",
				Tags:      []string{"舞萌", "dx rating", "舞萌"},
			}},
			map[string]bool{},
		)
		if rejected != 0 || len(merges) != 1 {
			t.Fatalf("expected one accepted merge, got accepted=%d rejected=%d", len(merges), rejected)
		}
		if !protected["mem_001"] || !protected["mem_002"] {
			t.Fatalf("expected all merge sources to be protected: %#v", protected)
		}
		if !slices.Equal(merges[0].Tags, []string{"舞萌", "dx rating"}) {
			t.Fatalf("unexpected cleaned tags: %#v", merges[0].Tags)
		}
	})

	t.Run("rejects overlapping groups", func(t *testing.T) {
		merges, _, rejected := validateReflectionMerges(
			entries,
			[]reflectMerge{
				{SourceIDs: []string{"mem_001", "mem_002"}, Content: "first", Tags: []string{"tag"}},
				{SourceIDs: []string{"mem_002", "mem_003"}, Content: "second", Tags: []string{"tag"}},
			},
			map[string]bool{},
		)
		if len(merges) != 0 || rejected != 2 {
			t.Fatalf("expected both overlapping groups rejected, got accepted=%d rejected=%d", len(merges), rejected)
		}
	})

	t.Run("rejects merge and protects source when also outdated", func(t *testing.T) {
		merges, protected, rejected := validateReflectionMerges(
			entries,
			[]reflectMerge{{
				SourceIDs: []string{"mem_001", "mem_002"},
				Content:   "merged",
				Tags:      []string{"tag"},
			}},
			map[string]bool{"mem_001": true},
		)
		if len(merges) != 0 || rejected != 1 || !protected["mem_001"] {
			t.Fatalf("expected conflicting merge rejected and source protected")
		}
	})
}

func TestApplyReflectionMergeArchivesSources(t *testing.T) {
	store := NewStore(t.TempDir() + "/brain.json")
	seedMemoryEntries(t, store, []MemoryEntry{
		{
			ID:          "mem_001",
			Owner:       "alice",
			Content:     "用户喜欢打舞萌",
			Tags:        []string{"舞萌"},
			Source:      SourceExtract,
			Visibility:  VisibilityPrivate,
			Importance:  0.6,
			AccessCount: 2,
		},
		{
			ID:          "mem_002",
			Owner:       "alice",
			Content:     "用户的舞萌 dx rating 为 w6",
			Tags:        []string{"舞萌", "dx rating"},
			Source:      SourceExtract,
			Visibility:  VisibilityPublic,
			Importance:  0.9,
			AccessCount: 5,
		},
	})

	snapshots, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := store.applyReflectionWithMerges(
		"alice",
		[]validatedMerge{{
			Sources: snapshots,
			Content: "用户是 dx rating 为 w6 的舞萌爱好者",
			Tags:    []string{"舞萌", "dx rating", "w6"},
		}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.MergedEntries) != 1 || applied.MergedSourceCount != 2 {
		t.Fatalf("unexpected merge result: %#v", applied)
	}
	if len(applied.Remaining) != 1 {
		t.Fatalf("expected one active memory, got %d", len(applied.Remaining))
	}

	merged := applied.Remaining[0]
	if merged.Source != SourceReflect || merged.Visibility != VisibilityPrivate {
		t.Fatalf("unexpected merged source/visibility: %q/%q", merged.Source, merged.Visibility)
	}
	if merged.Importance != 0.9 || merged.AccessCount != 5 {
		t.Fatalf("unexpected merged ranking fields: importance=%v access=%d", merged.Importance, merged.AccessCount)
	}
	if !slices.Equal(merged.MergedFrom, []string{"mem_001", "mem_002"}) {
		t.Fatalf("unexpected provenance: %#v", merged.MergedFrom)
	}

	brain, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(brain.MergeArchives) != 1 || len(brain.MergeArchives[0].Sources) != 2 {
		t.Fatalf("expected complete source archive, got %#v", brain.MergeArchives)
	}
}

func TestApplyReflectionMergeRejectsStaleOrCrossOwnerSources(t *testing.T) {
	tests := []struct {
		name        string
		secondOwner string
		makeStale   bool
	}{
		{name: "stale snapshot", secondOwner: "alice", makeStale: true},
		{name: "cross owner", secondOwner: "bob"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(t.TempDir() + "/brain.json")
			seedMemoryEntries(t, store, []MemoryEntry{
				{ID: "mem_001", Owner: "alice", Content: "one", Tags: []string{"one"}},
				{ID: "mem_002", Owner: tt.secondOwner, Content: "two", Tags: []string{"two"}},
			})
			all, err := store.ListAll()
			if err != nil {
				t.Fatal(err)
			}
			if tt.makeStale {
				time.Sleep(time.Millisecond)
				if err := store.UpdateImportance("mem_001", 0.7); err != nil {
					t.Fatal(err)
				}
			}

			applied, err := store.applyReflectionWithMerges(
				"alice",
				[]validatedMerge{{Sources: all, Content: "merged", Tags: []string{"tag"}}},
				[]string{"mem_001"},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(applied.MergedEntries) != 0 || len(applied.RemovedIDs) != 0 {
				t.Fatalf("unsafe merge changed active memories: %#v", applied)
			}
			entries, err := store.ListAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 2 {
				t.Fatalf("expected both sources retained, got %d", len(entries))
			}
		})
	}
}

func TestReflectorMergeSynchronizesVectorAndCatalog(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/brain.json")
	seedMemoryEntries(t, store, []MemoryEntry{
		{ID: "mem_001", Owner: "alice", Content: "用户喜欢打舞萌", Tags: []string{"舞萌"}, Importance: 0.7},
		{ID: "mem_002", Owner: "alice", Content: "用户的舞萌 dx rating 为 w6", Tags: []string{"w6"}, Importance: 0.9},
	})
	entries, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatal(err)
	}

	vectorStore := NewVectorStore(dir+"/vectors.json", fixedTestEmbedder{})
	for _, entry := range entries {
		if err := vectorStore.Index(context.Background(), entry.ID, entry.Content); err != nil {
			t.Fatal(err)
		}
	}
	catalog := NewCatalogStore(dir + "/catalog.json")
	reflector := &Reflector{store: store, catalog: catalog, vs: vectorStore}
	payload, err := json.Marshal(reflectResult{
		Topics: []MemoryTopic{{Name: "舞萌", Importance: 0.9}},
		Merges: []reflectMerge{{
			SourceIDs: []string{"mem_001", "mem_002"},
			Content:   "用户是 dx rating 为 w6 的舞萌爱好者",
			Tags:      []string{"舞萌", "dx rating", "w6"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reflector.applyResult("alice", entries, string(payload)); err != nil {
		t.Fatal(err)
	}

	remaining, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("expected one merged memory, got %d", len(remaining))
	}
	vectorFile, err := vectorStore.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(vectorFile.Records) != 1 || vectorFile.Records[0].ID != remaining[0].ID {
		t.Fatalf("vector index was not replaced: %#v", vectorFile.Records)
	}
	storedCatalog, err := catalog.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if storedCatalog == nil || storedCatalog.MemoryCount != 1 {
		t.Fatalf("unexpected catalog: %#v", storedCatalog)
	}
}

type fixedTestEmbedder struct{}

func (fixedTestEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range texts {
		vectors[i] = []float32{float32(len([]rune(texts[i]))), 1}
	}
	return vectors, nil
}

func seedMemoryEntries(t *testing.T, store *Store, entries []MemoryEntry) {
	t.Helper()
	for _, entry := range entries {
		if err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
}
