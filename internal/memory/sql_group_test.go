package memory

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLGroupsIsolatePlatformsAndShareQQAliases(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "groups.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	manager := NewSQLGroupManager(db, "test-instance", nil)
	qq, err := manager.GetGroupStoreForPlatform("qq", "qq:group:test-group")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := manager.GetGroupStoreForPlatform("qq", "group:test-group")
	if err != nil || alias != qq {
		t.Fatalf("QQ aliases did not share a store: %v", err)
	}
	astrbot, err := manager.GetGroupStoreForPlatform("aiocqhttp", "test-group")
	if err != nil || astrbot != qq {
		t.Fatalf("AstrBot QQ alias did not share a store: %v", err)
	}
	other, err := manager.GetGroupStoreForPlatform("discord", "test-group")
	if err != nil || other == qq {
		t.Fatalf("platforms shared a store: %v", err)
	}
	if _, err := qq.ObserveMember("user-a", "Example", "Card", "member", "test"); err != nil {
		t.Fatal(err)
	}
	if err := qq.Save(MemoryEntry{ID: "entry-a", Owner: "user-a", Content: "qq memory", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := qq.Save(MemoryEntry{ID: "distilled-a", Owner: "user-a", Content: "accepted history", Source: SourceDistill, SourceSenderID: "user-a", SourceMessageID: "accepted-message", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := qq.PurgeDistilledEntries(""); err != nil {
		t.Fatal(err)
	}
	accepted, err := qq.ListAll()
	if err != nil || len(accepted) != 2 {
		t.Fatalf("missing message ID removed accepted SQL history: %#v, %v", accepted, err)
	}
	profile, err := qq.GetProfile()
	if err != nil || len(profile.Members) != 1 || profile.Members["user-a"].Nickname != "Example" {
		t.Fatalf("group member was not stored: %#v, %v", profile, err)
	}
	otherMemory, err := other.ListAll()
	if err != nil || len(otherMemory) != 0 {
		t.Fatalf("cross-platform memory leaked: %#v, %v", otherMemory, err)
	}
	groups, err := manager.ListGroups()
	if err != nil || len(groups) != 1 || groups[0].Platform != "qq" {
		t.Fatalf("wrong group listing: %#v, %v", groups, err)
	}
	if err := manager.DeleteGroupForPlatform("qq", "test-group"); err != nil {
		t.Fatal(err)
	}
	memories, err := qq.ListAll()
	if err != nil || len(memories) != 0 {
		t.Fatalf("group deletion retained memories: %#v, %v", memories, err)
	}
}

func TestSQLObservedMemberNamePreservesAliasesAndRejectsStaleEdit(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "groups.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	manager := NewSQLGroupManager(db, "test-instance", nil)
	group, err := manager.GetGroupStoreForPlatform("qq", "group-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := group.ObserveMember("user-one", "Nickname", "Card", "member", "test"); err != nil {
		t.Fatal(err)
	}
	if err := group.UpdateMemberPreferredName("user-one", "Old", []string{"Alias"}); err != nil {
		t.Fatal(err)
	}
	stale := ""
	if changed, err := group.SetObservedMemberPreferredName("user-one", "Wrong", &stale); err != nil || changed {
		t.Fatalf("stale reflection changed member: changed=%t err=%v", changed, err)
	}
	current := "Old"
	if changed, err := group.SetObservedMemberPreferredName("user-one", "New", &current); err != nil || !changed {
		t.Fatalf("current reflection did not change member: changed=%t err=%v", changed, err)
	}
	if changed, err := group.SetObservedMemberPreferredName("missing", "Invented", nil); err != nil || changed {
		t.Fatalf("missing member was created: changed=%t err=%v", changed, err)
	}
	if err := group.Save(MemoryEntry{ID: "evidence-one", Owner: "user-one", Content: "请叫我新称呼", Source: SourceManual, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	entries, err := group.ListAll()
	if err != nil || len(entries) != 1 {
		t.Fatalf("load evidence: %#v, %v", entries, err)
	}
	current = "New"
	if changed, err := group.SetObservedMemberPreferredName("user-one", "Fresh", &current, entries[0]); err != nil || !changed {
		t.Fatalf("current evidence did not update name: changed=%t err=%v", changed, err)
	}
	if _, err := db.SQL.Exec(`UPDATE memory_entries SET owner = 'other-user'
		WHERE instance_id = 'test-instance' AND scope_type = 'group' AND platform = 'qq'
		AND group_id = 'group-one' AND id = 'evidence-one'`); err != nil {
		t.Fatal(err)
	}
	current = "Fresh"
	if changed, err := group.SetObservedMemberPreferredName("user-one", "WrongOwner", &current, entries[0]); err != nil || changed {
		t.Fatalf("changed evidence owner did not block name: changed=%t err=%v", changed, err)
	}
	if _, err := db.SQL.Exec(`UPDATE memory_entries SET owner = 'user-one'
		WHERE instance_id = 'test-instance' AND scope_type = 'group' AND platform = 'qq'
		AND group_id = 'group-one' AND id = 'evidence-one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`UPDATE memory_entries SET owner = 'qq:user:user-one'
		WHERE instance_id = 'test-instance' AND scope_type = 'group' AND platform = 'qq'
		AND group_id = 'group-one' AND id = 'evidence-one'`); err != nil {
		t.Fatal(err)
	}
	if changed, err := group.SetObservedMemberPreferredName("user-one", "Legacy", &current, entries[0]); err != nil || !changed {
		t.Fatalf("canonical evidence owner alias was rejected: changed=%t err=%v", changed, err)
	}
	entry := entries[0]
	entry.Content = "旧称呼已失效"
	if err := group.UpdateEntry(entry); err != nil {
		t.Fatal(err)
	}
	current = "Legacy"
	if changed, err := group.SetObservedMemberPreferredName("user-one", "Stale", &current, entries[0]); err != nil || changed {
		t.Fatalf("stale evidence changed name: changed=%t err=%v", changed, err)
	}
	profile, err := group.GetProfile()
	if err != nil || profile.Members["user-one"].PreferredName != "Legacy" ||
		len(profile.Members["user-one"].Aliases) != 1 || profile.Members["missing"] != nil {
		t.Fatalf("unexpected SQL member profile: %#v, %v", profile, err)
	}
}
