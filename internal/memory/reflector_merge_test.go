package memory

import (
	"FrostAgent/internal/core"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
			AccessCount: 2,
		},
		{
			ID:          "mem_002",
			Owner:       "alice",
			Content:     "用户的舞萌 dx rating 为 w6",
			Tags:        []string{"舞萌", "dx rating"},
			Source:      SourceExtract,
			Visibility:  VisibilityPublic,
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
	if merged.AccessCount != 5 {
		t.Fatalf("unexpected merged ranking fields: access=%d", merged.AccessCount)
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
				if err := store.IncrementAccessCount("mem_001"); err != nil {
					t.Fatal(err)
				}
			}

			applied, err := store.applyReflectionWithMerges(
				"alice",
				[]validatedMerge{{Sources: all, Content: "merged", Tags: []string{"tag"}}},
				[]string{"mem_001"},
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

func TestReflectorMergeUpdatesCatalog(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/brain.json")
	seedMemoryEntries(t, store, []MemoryEntry{
		{ID: "mem_001", Owner: "alice", Content: "用户喜欢打舞萌", Tags: []string{"舞萌"}},
		{ID: "mem_002", Owner: "alice", Content: "用户的舞萌 dx rating 为 w6", Tags: []string{"w6"}},
	})
	entries, err := store.ListByOwner("alice")
	if err != nil {
		t.Fatal(err)
	}

	catalog := NewCatalogStore(dir + "/catalog.json")
	reflector := &Reflector{store: store, catalog: catalog}
	payload, err := json.Marshal(reflectResult{
		Topics: []MemoryTopic{{Name: "舞萌"}},
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
	storedCatalog, err := catalog.Get("alice")
	if err != nil {
		t.Fatal(err)
	}
	if storedCatalog == nil || storedCatalog.MemoryCount != 1 {
		t.Fatalf("unexpected catalog: %#v", storedCatalog)
	}
}

func seedMemoryEntries(t *testing.T, store *Store, entries []MemoryEntry) {
	t.Helper()
	for _, entry := range entries {
		if err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
}

type mockReflectLLMProvider struct {
	mu      sync.Mutex
	calls   int
	prompts []string
}

func (m *mockReflectLLMProvider) Chat(ctx context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	m.mu.Lock()
	m.calls++
	m.prompts = append(m.prompts, fmt.Sprintf("%v", req.Messages[0].Content))
	m.mu.Unlock()

	payload, _ := json.Marshal(reflectResult{
		Topics: []MemoryTopic{{Name: "舞萌"}},
		Merges: []reflectMerge{{
			SourceIDs: []string{"mem_001", "mem_002"},
			Content:   "用户是 dx rating 为 w6 的舞萌爱好者",
			Tags:      []string{"舞萌", "dx rating", "w6"},
		}},
	})
	return &core.ChatResponse{
		Message: core.ChatMessage{
			Role:    core.RoleAssistant,
			Content: string(payload),
		},
	}, nil
}

func TestReflectLegacyAndCanonicalOwnerSinglePassAndCatalog(t *testing.T) {
	dir := t.TempDir()
	brainPath := filepath.Join(dir, "brain.json")
	catalogPath := filepath.Join(dir, "catalog.json")

	// 1. Seed raw brain.json containing mixed legacy aiocqhttp:user:X and canonical X entries
	legacyBrain := BrainData{
		Entries: []MemoryEntry{
			{
				ID:         "mem_001",
				Owner:      "aiocqhttp:user:test_user_42",
				Content:    "用户喜欢打舞萌",
				Tags:       []string{"舞萌"},
				Source:     SourceExtract,
				Visibility: VisibilityPrivate,
			},
			{
				ID:         "mem_002",
				Owner:      "test_user_42",
				Content:    "用户的舞萌 dx rating 为 w6",
				Tags:       []string{"w6"},
				Source:     SourceExtract,
				Visibility: VisibilityPrivate,
			},
		},
	}
	brainBytes, err := json.MarshalIndent(legacyBrain, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brainPath, brainBytes, 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Seed legacy catalog on disk
	legacyCatalog := catalogFile{
		Version:   currentCatalogVersion,
		UpdatedAt: time.Now(),
		Users: map[string]UserMemoryCatalog{
			"aiocqhttp:user:test_user_42": {
				Owner:       "aiocqhttp:user:test_user_42",
				Topics:      []MemoryTopic{{Name: "旧主题"}},
				MemoryCount: 1,
				GeneratedAt: time.Now().Add(-1 * time.Hour),
			},
		},
	}
	catalogBytes, err := json.MarshalIndent(legacyCatalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, catalogBytes, 0644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(brainPath)
	catalog := NewCatalogStore(catalogPath)
	mockLLM := &mockReflectLLMProvider{}

	reflector := NewReflector(
		store,
		catalog,
		mockLLM,
		"test-model",
		Config{ReflectTimeout: 5 * time.Second},
	)

	// 3. Perform full reflection
	ctx := context.Background()
	if err := reflector.Reflect(ctx); err != nil {
		t.Fatalf("Reflect failed: %v", err)
	}

	// 4. Assert LLM was called EXACTLY ONCE for this logical QQ owner
	mockLLM.mu.Lock()
	calls := mockLLM.calls
	prompts := mockLLM.prompts
	mockLLM.mu.Unlock()

	if calls != 1 {
		t.Fatalf("expected exactly 1 LLM reflection call for the unified QQ owner, got %d", calls)
	}
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt recorded, got %d", len(prompts))
	}
	// Both memories must enter the SAME reflection cycle
	if !strings.Contains(prompts[0], "mem_001") || !strings.Contains(prompts[0], "mem_002") {
		t.Fatalf("expected both mem_001 and mem_002 in reflection prompt: %s", prompts[0])
	}

	// 5. Assert catalog on disk contains ONLY ONE canonical bucket
	catFile, err := catalog.load()
	if err != nil {
		t.Fatalf("catalog.load() failed: %v", err)
	}
	if len(catFile.Users) != 1 {
		t.Fatalf("expected exactly 1 catalog entry in Users, got %d: %+v", len(catFile.Users), catFile.Users)
	}
	if _, ok := catFile.Users["test_user_42"]; !ok {
		t.Fatalf("expected canonical key 'test_user_42' in catalog.Users, got keys: %+v", catFile.Users)
	}

	// 6. Assert alias lookup works for both canonical and legacy aliases
	catCanonical, err := catalog.Get("test_user_42")
	if err != nil || catCanonical == nil {
		t.Fatalf("Get('test_user_42') failed: %v", err)
	}
	catLegacy, err := catalog.Get("aiocqhttp:user:test_user_42")
	if err != nil || catLegacy == nil {
		t.Fatalf("Get('aiocqhttp:user:test_user_42') failed: %v", err)
	}
	if catCanonical.Owner != "test_user_42" || catLegacy.Owner != "test_user_42" {
		t.Fatalf("expected canonical owner in retrieved catalog: canonical=%s legacy=%s", catCanonical.Owner, catLegacy.Owner)
	}

	// 7. FormatForPrompt also succeeds on both
	promptCanonical, err := catalog.FormatForPrompt("test_user_42")
	if err != nil || promptCanonical == "" {
		t.Fatalf("FormatForPrompt('test_user_42') failed: %v", err)
	}
	promptLegacy, err := catalog.FormatForPrompt("aiocqhttp:user:test_user_42")
	if err != nil || promptLegacy == "" {
		t.Fatalf("FormatForPrompt('aiocqhttp:user:test_user_42') failed: %v", err)
	}
	if promptCanonical != promptLegacy {
		t.Fatalf("expected identical prompt format for alias: canonical=%q legacy=%q", promptCanonical, promptLegacy)
	}

	// 8. Assert store memories have been migrated to canonical owner
	entries, err := store.ListAll()
	if err != nil {
		t.Fatalf("store.ListAll() failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 merged memory entry, got %d", len(entries))
	}
	if entries[0].Owner != "test_user_42" {
		t.Fatalf("expected merged memory owner to be canonical 'test_user_42', got %q", entries[0].Owner)
	}
}

func TestCatalogStoreAliasGetAndReplace(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	catalog := NewCatalogStore(catalogPath)

	// Save using legacy alias
	err := catalog.Replace(UserMemoryCatalog{
		Owner:       "aiocqhttp:user:test_user_99",
		Topics:      []MemoryTopic{{Name: "测试主题"}},
		MemoryCount: 2,
		GeneratedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Replace failed: %v", err)
	}

	// In memory and on disk, it should be canonical "test_user_99"
	catFile, err := catalog.load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(catFile.Users) != 1 {
		t.Fatalf("expected 1 user in catalog, got %d", len(catFile.Users))
	}
	if _, ok := catFile.Users["test_user_99"]; !ok {
		t.Fatalf("expected key 'test_user_99', got %+v", catFile.Users)
	}

	// Retrieve via other aliases
	for _, query := range []string{"test_user_99", "aiocqhttp:user:test_user_99", "onebot:user:test_user_99", "qq:user:test_user_99"} {
		got, err := catalog.Get(query)
		if err != nil || got == nil {
			t.Errorf("Get(%q) failed: %v, got=%v", query, err, got)
		}
	}

	// Delete via onebot alias
	if err := catalog.Delete("onebot:user:test_user_99"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	got, err := catalog.Get("test_user_99")
	if err != nil {
		t.Fatalf("Get after Delete failed: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after Delete, got %+v", got)
	}
}
