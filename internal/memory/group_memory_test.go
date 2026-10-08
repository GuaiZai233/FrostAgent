package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSafeGroupKey(t *testing.T) {
	// Canonical prefix normalization produces identical keys
	baseKey := SafeGroupKey("mock_grp_100")
	if SafeGroupKey("group:mock_grp_100") != baseKey {
		t.Errorf("expected group: prefix to match canonical key")
	}
	if SafeGroupKey("qq:group:mock_grp_100") != baseKey {
		t.Errorf("expected qq:group: prefix to match canonical key")
	}
	if SafeGroupKey("onebot:group:mock_grp_100") != baseKey {
		t.Errorf("expected onebot:group: prefix to match canonical key")
	}
	if SafeGroupKey("astrbot:group:mock_grp_100") != baseKey {
		t.Errorf("expected astrbot:group: prefix to match canonical key")
	}

	// Injective mapping: distinct IDs with delimiters must NEVER collide
	k1 := SafeGroupKey("mock_a:b")
	k2 := SafeGroupKey("mock_a/b")
	k3 := SafeGroupKey("mock_a_b")
	if k1 == k2 {
		t.Errorf("collision between %q and %q: %s", "mock_a:b", "mock_a/b", k1)
	}
	if k1 == k3 {
		t.Errorf("collision between %q and %q: %s", "mock_a:b", "mock_a_b", k1)
	}
	if k2 == k3 {
		t.Errorf("collision between %q and %q: %s", "mock_a/b", "mock_a_b", k2)
	}

	// Empty and blank fallback
	if SafeGroupKey("") != "unknown_group" {
		t.Errorf("expected unknown_group for empty string")
	}
	if SafeGroupKey("   ") != "unknown_group" {
		t.Errorf("expected unknown_group for whitespace string")
	}

	// Directory traversal and Windows device names safety
	riskyInputs := []string{
		".", "..", "../..", "../../etc/passwd",
		"CON", "PRN", "AUX", "NUL", "COM1", "LPT1",
		"con.txt", "nul.json", "..\\..\\windows",
	}

	tempDir, err := os.MkdirTemp("", "frostagent_safe_key_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	groupsBase := filepath.Clean(filepath.Join(tempDir, "groups"))

	for _, input := range riskyInputs {
		key := SafeGroupKey(input)
		if key == "" || key == "." || key == ".." || strings.Contains(key, "/") || strings.Contains(key, "\\") {
			t.Errorf("SafeGroupKey(%q) produced unsafe key %q", input, key)
		}
		target := filepath.Clean(filepath.Join(groupsBase, key))
		rel, err := filepath.Rel(groupsBase, target)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			t.Errorf("SafeGroupKey(%q) escaped groups boundary: rel=%q", input, rel)
		}
	}
}

func TestResolveCallingNamePriority(t *testing.T) {
	tests := []struct {
		name     string
		member   MemberProfile
		expected string
	}{
		{
			name: "preferred name has highest priority",
			member: MemberProfile{
				UserID:        "mock_u_1",
				Nickname:      "NickOne",
				Card:          "CardOne",
				PreferredName: "PrefOne",
			},
			expected: "PrefOne",
		},
		{
			name: "nickname takes precedence when preferred name is empty",
			member: MemberProfile{
				UserID:        "mock_u_2",
				Nickname:      "NickTwo",
				Card:          "CardTwo",
				PreferredName: "",
			},
			expected: "NickTwo",
		},
		{
			name: "card is strictly disambiguation and never used as calling name",
			member: MemberProfile{
				UserID:        "mock_u_3",
				Nickname:      "",
				Card:          "CardThree",
				PreferredName: "",
			},
			expected: "群友",
		},
		{
			name: "fallback to neutral address when all are empty",
			member: MemberProfile{
				UserID:        "mock_u_4",
				Nickname:      "",
				Card:          "",
				PreferredName: "",
			},
			expected: "群友",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.member.ResolveCallingName()
			if got != tt.expected {
				t.Errorf("ResolveCallingName() = %q; want %q", got, tt.expected)
			}
		})
	}
}

func TestGroupStore_MemberObservationAndRoleSync(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gs, err := NewGroupStore(tempDir, "mock_grp_101")
	if err != nil {
		t.Fatalf("NewGroupStore failed: %v", err)
	}

	// First observation
	m1, err := gs.ObserveMember("mock_u_1", "UserAlpha", "CardAlpha", "member", "onebot")
	if err != nil {
		t.Fatalf("ObserveMember failed: %v", err)
	}
	if m1.Nickname != "UserAlpha" || m1.Role != GroupRoleMember {
		t.Errorf("m1 mismatch: %+v", m1)
	}

	// Update role to admin and change nickname
	m2, err := gs.ObserveMember("mock_u_1", "UserAlphaRenamed", "CardAlpha", "admin", "onebot")
	if err != nil {
		t.Fatalf("ObserveMember 2 failed: %v", err)
	}
	if m2.Nickname != "UserAlphaRenamed" || m2.Role != GroupRoleAdmin {
		t.Errorf("m2 mismatch: %+v", m2)
	}

	// Set preferred name and aliases
	err = gs.UpdateMemberPreferredName("mock_u_1", "AlphaChief", []string{"Chief", "Boss"})
	if err != nil {
		t.Fatalf("UpdateMemberPreferredName failed: %v", err)
	}

	prof, err := gs.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	m3 := prof.GetMember("mock_u_1")
	if m3 == nil {
		t.Fatalf("GetMember failed")
	}
	if m3.PreferredName != "AlphaChief" || len(m3.Aliases) != 2 {
		t.Errorf("m3 profile mismatch: %+v", m3)
	}
	if m3.ResolveCallingName() != "AlphaChief" {
		t.Errorf("m3 calling name = %q; want AlphaChief", m3.ResolveCallingName())
	}

	// Update group name
	if err := gs.UpdateGroupName("Frost Test Group"); err != nil {
		t.Fatalf("UpdateGroupName failed: %v", err)
	}
	prof, _ = gs.GetProfile()
	if prof.GroupName != "Frost Test Group" {
		t.Errorf("GroupName = %q; want 'Frost Test Group'", prof.GroupName)
	}

	// Reopen group store from disk to verify persistence
	gsReopened, err := NewGroupStore(tempDir, "mock_grp_101")
	if err != nil {
		t.Fatalf("reopening GroupStore failed: %v", err)
	}
	reopenedProf, err := gsReopened.GetProfile()
	if err != nil {
		t.Fatalf("reopened GetProfile failed: %v", err)
	}
	if reopenedProf.GroupName != "Frost Test Group" {
		t.Errorf("reopened GroupName = %q; want 'Frost Test Group'", reopenedProf.GroupName)
	}
	reopenedMember := reopenedProf.GetMember("mock_u_1")
	if reopenedMember == nil || reopenedMember.PreferredName != "AlphaChief" {
		t.Errorf("reopened member profile mismatch: %+v", reopenedMember)
	}
}

func TestGroupStore_MemoryCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_mem_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gs, err := NewGroupStore(tempDir, "mock_grp_202")
	if err != nil {
		t.Fatalf("NewGroupStore failed: %v", err)
	}

	now := time.Now().UTC()
	entries := []MemoryEntry{
		{
			ID:        "gmem_1",
			Owner:     "mock_u_a",
			Content:   "mock_u_a likes programming in Go",
			Tags:      []string{"programming", "golang"},
			Source:    SourceDistill,
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "mock_grp_202",
		},
		{
			ID:        "gmem_2",
			Owner:     GroupOwnerExplicit,
			Content:   "Group rule: no advertising allowed",
			Tags:      []string{"rule", "regulation"},
			Source:    SourceManual,
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "mock_grp_202",
		},
		{
			ID:        "gmem_3",
			Owner:     GroupOwnerExplicit,
			Content:   "mock_u_a says mock_u_b is arriving tomorrow",
			Tags:      []string{"mock_u_b", "schedule"},
			Source:    SourceDistill,
			CreatedAt: now,
			UpdatedAt: now,
			ScopeType: ScopeGroup,
			GroupID:   "mock_grp_202",
		},
	}

	for _, e := range entries {
		if err := gs.Save(e); err != nil {
			t.Fatalf("Save(%s) failed: %v", e.ID, err)
		}
	}

	all, err := gs.ListAll()
	if err != nil {
		t.Fatalf("ListAll failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAll len = %d; want 3", len(all))
	}

	byUserA, err := gs.ListByOwner("mock_u_a")
	if err != nil {
		t.Fatalf("ListByOwner(mock_u_a) failed: %v", err)
	}
	if len(byUserA) != 1 || byUserA[0].ID != "gmem_1" {
		t.Errorf("ListByOwner(mock_u_a) mismatch: %+v", byUserA)
	}

	byGroup, err := gs.ListByOwner(GroupOwnerExplicit)
	if err != nil {
		t.Fatalf("ListByOwner(group) failed: %v", err)
	}
	if len(byGroup) != 2 {
		t.Errorf("ListByOwner(group) len = %d; want 2", len(byGroup))
	}

	// Search
	searchRes, err := gs.Search("rule", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(searchRes) != 1 || searchRes[0].ID != "gmem_2" {
		t.Errorf("Search(rule) mismatch: %+v", searchRes)
	}

	// Tag search
	tagRes, err := gs.SearchByTags([]string{"golang"}, 10)
	if err != nil {
		t.Fatalf("SearchByTags failed: %v", err)
	}
	if len(tagRes) != 1 || tagRes[0].ID != "gmem_1" {
		t.Errorf("SearchByTags(golang) mismatch: %+v", tagRes)
	}

	// Recall record
	if err := gs.RecordRecall(tagRes); err != nil {
		t.Fatalf("RecordRecall failed: %v", err)
	}
	afterRecall, _ := gs.ListAll()
	for _, m := range afterRecall {
		if m.ID == "gmem_1" && m.AccessCount != 1 {
			t.Errorf("AccessCount = %d; want 1", m.AccessCount)
		}
	}

	// Delete
	if err := gs.Delete("gmem_1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	allAfterDelete, _ := gs.ListAll()
	if len(allAfterDelete) != 2 {
		t.Errorf("ListAll len after delete = %d; want 2", len(allAfterDelete))
	}
}

func TestGroupManager_SynchronizationAndAliasing(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_sync_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gm := NewGroupManager(tempDir, nil)

	// Aliases must return the exact same store pointer
	s1, err := gm.GetGroupStore("mock_grp_303")
	if err != nil {
		t.Fatalf("GetGroupStore(raw) failed: %v", err)
	}
	s2, err := gm.GetGroupStore("group:mock_grp_303")
	if err != nil {
		t.Fatalf("GetGroupStore(prefixed) failed: %v", err)
	}
	s3, err := gm.GetGroupStore("qq:group:mock_grp_303")
	if err != nil {
		t.Fatalf("GetGroupStore(qq:prefixed) failed: %v", err)
	}

	if s1 != s2 || s2 != s3 {
		t.Fatalf("aliased group stores are not identical pointers: s1=%p, s2=%p, s3=%p", s1, s2, s3)
	}

	// Concurrent writes on same group via different aliases
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			alias := "mock_grp_303"
			if idx%2 == 0 {
				alias = "group:mock_grp_303"
			}
			st, err := gm.GetGroupStore(alias)
			if err != nil {
				t.Errorf("concurrent GetGroupStore failed: %v", err)
				return
			}
			_, _ = st.ObserveMember(
				"mock_u_worker",
				"WorkerName",
				"",
				"member",
				"onebot",
			)
		}(i)
	}
	wg.Wait()

	prof, err := s1.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if prof.GetMember("mock_u_worker") == nil {
		t.Errorf("expected member mock_u_worker to be saved")
	}
}

func TestLegacyGroupMemoriesMigration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_migration_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	brainPath := filepath.Join(tempDir, "brain.json")
	initialBrain := BrainData{
		Entries: []MemoryEntry{
			{
				ID:        "p_mem_1",
				Owner:     "mock_u_private",
				Content:   "Private user likes apples",
				Tags:      []string{"fruit"},
				Source:    SourceManual,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			{
				ID:        "g_mem_1",
				Owner:     "group:mock_grp_401",
				OwnerType: OwnerGroup,
				Content:   "Group 401 discusses weekend trip",
				Tags:      []string{"trip"},
				Source:    SourceExtract,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			{
				ID:        "g_mem_2",
				Owner:     "mock_u_alice",
				GroupID:   "mock_grp_401",
				ScopeType: ScopeGroup,
				Content:   "Alice in 401 loves coffee",
				Tags:      []string{"coffee"},
				Source:    SourceDistill,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			{
				ID:        "g_mem_3",
				Owner:     "qq:group:mock_grp_402",
				Content:   "Group 402 annual meeting notes",
				Tags:      []string{"meeting"},
				Source:    SourceManual,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
		MergeArchives: []MemoryMergeArchive{
			{
				MergedID: "m_archive_1",
				Owner:    "group:mock_grp_401",
				Sources: []MemoryEntry{
					{ID: "src_1", Content: "Archive source 1"},
				},
				MergedAt: time.Now(),
			},
		},
	}

	data, err := json.MarshalIndent(initialBrain, "", "  ")
	if err != nil {
		t.Fatalf("marshal initial brain: %v", err)
	}
	if err := os.WriteFile(brainPath, data, 0600); err != nil {
		t.Fatalf("write initial brain: %v", err)
	}

	store := NewStore(brainPath)
	gm := NewGroupManager(tempDir, nil)

	// Execute migration
	if err := MigrateLegacyGroupMemories(tempDir, store, gm, nil); err != nil {
		t.Fatalf("MigrateLegacyGroupMemories failed: %v", err)
	}

	// 1. Verify brain.json backup exists
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("read tempDir: %v", err)
	}
	backupFound := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "brain.json.bak.") {
			backupFound = true
			break
		}
	}
	if !backupFound {
		t.Errorf("expected brain.json.bak.* backup file to be created")
	}

	// 2. Verify brain.json contains only private memories
	privateEntries, err := store.ListAll()
	if err != nil {
		t.Fatalf("store.ListAll failed: %v", err)
	}
	if len(privateEntries) != 1 || privateEntries[0].ID != "p_mem_1" {
		t.Fatalf("privateEntries len = %d, want 1 with ID p_mem_1", len(privateEntries))
	}

	// 3. Verify group 401 memories migrated
	g401Store, err := gm.GetGroupStore("mock_grp_401")
	if err != nil {
		t.Fatalf("GetGroupStore(mock_grp_401) failed: %v", err)
	}
	g401Memories, err := g401Store.ListAll()
	if err != nil {
		t.Fatalf("g401Store.ListAll failed: %v", err)
	}
	if len(g401Memories) != 2 {
		t.Fatalf("g401Memories len = %d, want 2", len(g401Memories))
	}
	for _, m := range g401Memories {
		if m.ID == "g_mem_1" && m.Owner != GroupOwnerExplicit {
			t.Errorf("g_mem_1 owner = %q, want %q", m.Owner, GroupOwnerExplicit)
		}
		if m.ID == "g_mem_2" && m.Owner != "mock_u_alice" {
			t.Errorf("g_mem_2 owner = %q, want mock_u_alice", m.Owner)
		}
	}

	// 4. Verify group 402 memories migrated
	g402Store, err := gm.GetGroupStore("mock_grp_402")
	if err != nil {
		t.Fatalf("GetGroupStore(mock_grp_402) failed: %v", err)
	}
	g402Memories, err := g402Store.ListAll()
	if err != nil {
		t.Fatalf("g402Store.ListAll failed: %v", err)
	}
	if len(g402Memories) != 1 || g402Memories[0].ID != "g_mem_3" {
		t.Fatalf("g402Memories mismatch: %+v", g402Memories)
	}

	// 5. Verify merge archive migrated to group 401
	g401Archives, err := g401Store.ListMergeArchives()
	if err != nil {
		t.Fatalf("g401Store.ListMergeArchives failed: %v", err)
	}
	if len(g401Archives) != 1 || g401Archives[0].MergedID != "m_archive_1" {
		t.Fatalf("g401Archives mismatch: %+v", g401Archives)
	}

	// 6. Idempotency: run migration again
	if err := MigrateLegacyGroupMemories(tempDir, store, gm, nil); err != nil {
		t.Fatalf("second MigrateLegacyGroupMemories run failed: %v", err)
	}
	g401MemoriesAfter, _ := g401Store.ListAll()
	if len(g401MemoriesAfter) != 2 {
		t.Errorf("idempotency check failed: g401MemoriesAfter len = %d, want 2", len(g401MemoriesAfter))
	}
}

func TestGroupReflection_IsolatedMergeAndCatalog(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "frostagent_group_reflect_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	gs, err := NewGroupStore(tempDir, "mock_grp_505")
	if err != nil {
		t.Fatalf("NewGroupStore failed: %v", err)
	}

	now := time.Now()
	entries := []MemoryEntry{
		{
			ID:        "rf_1",
			Owner:     GroupOwnerExplicit,
			Content:   "Group holds weekly coding seminar every Wednesday",
			Tags:      []string{"seminar", "coding"},
			Source:    SourceDistill,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "rf_2",
			Owner:     GroupOwnerExplicit,
			Content:   "Coding seminar starts at 8pm online",
			Tags:      []string{"seminar", "time"},
			Source:    SourceDistill,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "rf_3",
			Owner:     "mock_u_speaker",
			Content:   "Temporary outdated note about last month's trip",
			Tags:      []string{"trip"},
			Source:    SourceDistill,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	if err := gs.SaveGroupEntriesConditionallyContext(context.Background(), entries, nil); err != nil {
		t.Fatalf("SaveGroupEntriesConditionallyContext failed: %v", err)
	}

	merges := []validatedMerge{
		{
			Sources: []MemoryEntry{entries[0], entries[1]},
			Content: "Group holds weekly coding seminar every Wednesday at 8pm online",
			Tags:    []string{"seminar", "coding", "online"},
		},
	}
	outdated := []string{"rf_3"}

	res, err := gs.applyReflectionWithMerges(merges, outdated)
	if err != nil {
		t.Fatalf("applyReflectionWithMerges failed: %v", err)
	}

	if len(res.Remaining) != 1 {
		t.Fatalf("res.Remaining len = %d, want 1 merged entry", len(res.Remaining))
	}
	if len(res.MergedEntries) != 1 {
		t.Fatalf("res.MergedEntries len = %d, want 1", len(res.MergedEntries))
	}
	if len(res.OutdatedIDs) != 1 || res.OutdatedIDs[0] != "rf_3" {
		t.Fatalf("res.OutdatedIDs mismatch: %+v", res.OutdatedIDs)
	}

	archives, err := gs.ListMergeArchives()
	if err != nil {
		t.Fatalf("ListMergeArchives failed: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("archives len = %d, want 1", len(archives))
	}

	// Verify catalog store can save and delete without touching root files
	cat := gs.CatalogStore()
	err = cat.Replace(UserMemoryCatalog{
		Owner:       "mock_grp_505",
		Topics:      []MemoryTopic{{Name: "seminar"}},
		MemoryCount: 1,
		GeneratedAt: now,
	})
	if err != nil {
		t.Fatalf("catalog.Replace failed: %v", err)
	}

	catalogFile := filepath.Join(tempDir, "groups", SafeGroupKey("mock_grp_505"), "catalog.json")
	if _, err := os.Stat(catalogFile); os.IsNotExist(err) {
		t.Errorf("expected group catalog.json to exist at %s", catalogFile)
	}
}

func TestGateway_DistillMemoriesIncludedAndCompactExcluded(t *testing.T) {
	gw := NewGateway()

	entries := []MemoryEntry{
		{
			ID:      "m_distill",
			Owner:   "mock_u_1",
			Content: "User distilled fact",
			Source:  SourceDistill,
		},
		{
			ID:      "m_extract",
			Owner:   GroupOwnerExplicit,
			Content: "Normal extract fact",
			Source:  SourceExtract,
		},
		{
			ID:      "m_compact_legacy",
			Owner:   GroupOwnerExplicit,
			Content: "Legacy rolling summary that should not be recalled",
			Source:  SourceCompact,
		},
	}

	filtered := gw.FilterGroup(entries)
	if len(filtered) != 2 {
		t.Fatalf("FilterGroup len = %d, want 2 (excluding SourceCompact)", len(filtered))
	}
	for _, m := range filtered {
		if m.Source == SourceCompact {
			t.Errorf("FilterGroup included SourceCompact entry %s", m.ID)
		}
	}
}
