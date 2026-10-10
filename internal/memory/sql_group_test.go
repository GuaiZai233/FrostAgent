package memory

import (
	"FrostAgent/internal/storage"
	"context"
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
