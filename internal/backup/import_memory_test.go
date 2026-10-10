package backup

import (
	"FrostAgent/internal/memory"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testMemory(id, groupID, content string) memory.MemoryEntry {
	now := time.Now().UTC()
	return memory.MemoryEntry{ID: id, Owner: "synthetic-user", ScopeType: memory.ScopePrivate,
		GroupID: groupID, Content: content, Tags: []string{"synthetic-topic"},
		Source: memory.Source("manual"), CreatedAt: now, UpdatedAt: now}
}

func TestMemoryImportRestoresProfilesArchivesAndMergeSkipsIDs(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "memory.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const id = "test-instance"
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES (?, ?, ?)`, id, "Test", "now"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	private := testMemory("private-a", "", "private fact")
	group := testMemory("group-a", "123", "group fact")
	profile := &memory.GroupProfile{GroupID: "123", GroupName: "Existing Name",
		Members: map[string]*memory.MemberProfile{"synthetic-user": {
			UserID: "synthetic-user", Nickname: "Original", CreatedAt: now, UpdatedAt: now,
		}}}
	data := Memories{FormatVersion: FormatVersion, PrivateEntries: []memory.MemoryEntry{private},
		Groups: []GroupMemory{{Platform: "qq", GroupID: "qq:group:123", Profile: profile,
			Entries: []memory.MemoryEntry{group}, Archives: []memory.MemoryMergeArchive{{
				MergedID: "archive-a", Owner: "synthetic-user", Sources: []memory.MemoryEntry{group}, MergedAt: now,
			}}}, {Platform: "telegram", GroupID: "123", Entries: []memory.MemoryEntry{testMemory("telegram-a", "123", "different platform")}}}}
	if _, err := ImportMemories(db, id, data, false); err != nil {
		t.Fatal(err)
	}
	exported, err := ExportMemories(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.PrivateEntries) != 1 || len(exported.Groups) != 2 {
		t.Fatalf("memory scopes were not restored: %#v", exported)
	}
	var qq GroupMemory
	for _, item := range exported.Groups {
		if item.Platform == "qq" {
			qq = item
		}
	}
	if qq.Profile == nil || qq.Profile.GroupName != "Existing Name" || len(qq.Archives) != 1 {
		t.Fatalf("group profile or archive was lost: %#v", qq)
	}
	data.Groups = data.Groups[:1]
	data.Groups[0].Profile.GroupName = "Imported Name"
	data.Groups[0].Profile.Members["synthetic-user"].Nickname = "Imported"
	data.Groups[0].Entries = append(data.Groups[0].Entries, testMemory("group-b", "123", "same fact, new ID"))
	result, err := ImportMemories(db, id, data, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Skipped != 2 {
		t.Fatalf("merge counts = %+v", result)
	}
	exported, err = ExportMemories(db, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range exported.Groups {
		if item.Platform == "qq" {
			if len(item.Entries) != 2 || item.Profile.GroupName != "Existing Name" ||
				item.Profile.Members["synthetic-user"].Nickname != "Original" || len(item.Archives) != 1 {
				t.Fatalf("merge overwrote existing data or dropped archive: %#v", item)
			}
		}
	}
	var catalogs int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM memory_catalogs WHERE instance_id = ?`, id).Scan(&catalogs); err != nil || catalogs == 0 {
		t.Fatalf("derived catalogs were not regenerated: %d, %v", catalogs, err)
	}
}
