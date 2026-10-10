package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestSQLObserveMemberAndRecallDoNotReplaceScopes(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runSQLHotpathTest(t, db, true)
}

func TestPostgresMemoryHotpaths(t *testing.T) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("PostgreSQL test DSN must be a postgres URI: %v", err)
	}
	schema := fmt.Sprintf("fa_memory_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("remove memory test schema: %v", err)
		}
		admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), storage.Config{Backend: storage.Postgres, DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runSQLHotpathTest(t, db, false)
}

func runSQLHotpathTest(t *testing.T, db *storage.DB, sqliteTriggers bool) {
	t.Helper()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	group, err := NewSQLGroupManager(db, "test-instance", nil).GetGroupStoreForPlatform("qq", "group:test-group")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"user-a", "user-b"} {
		if _, err := group.ObserveMember(id, id, "", "member", "test"); err != nil {
			t.Fatal(err)
		}
	}
	private := NewSQLStore(db, "test-instance")
	for _, id := range []string{"memory-a", "memory-b"} {
		entry := MemoryEntry{ID: id, Owner: "user-a", Content: id, Source: SourceManual, CreatedAt: time.Now()}
		if err := private.Save(entry); err != nil {
			t.Fatal(err)
		}
		if err := group.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	if sqliteTriggers {
		for _, trigger := range []string{
			`CREATE TRIGGER no_member_delete BEFORE DELETE ON group_members BEGIN SELECT RAISE(ABORT, 'member scope replaced'); END`,
			`CREATE TRIGGER no_memory_delete BEFORE DELETE ON memory_entries BEGIN SELECT RAISE(ABORT, 'memory scope replaced'); END`,
		} {
			if _, err := db.SQL.Exec(trigger); err != nil {
				t.Fatal(err)
			}
		}
	}
	member, err := group.ObserveMember("user-a", "Updated", "", "unknown", "")
	if err != nil || member.Nickname != "Updated" {
		t.Fatalf("targeted member observation failed: %#v, %v", member, err)
	}
	if err := group.UpdateGroupName("Synthetic Group"); err != nil {
		t.Fatalf("group name update replaced members: %v", err)
	}
	if err := group.UpdateMemberRole("user-a", GroupRoleAdmin); err != nil {
		t.Fatalf("role update replaced members: %v", err)
	}
	if err := group.UpdateMemberPreferredName("user-a", "Preferred", []string{"Alias", "Alias"}); err != nil {
		t.Fatalf("preferred name update replaced members: %v", err)
	}
	if err := private.IncrementAccessCount("memory-a", "memory-a", "missing"); err != nil {
		t.Fatal(err)
	}
	if err := group.IncrementAccessCount("memory-a", "memory-a", "missing"); err != nil {
		t.Fatal(err)
	}
	newEntry := MemoryEntry{ID: "memory-c", Owner: "user-a", Content: "new fact", Source: SourceManual, CreatedAt: time.Now()}
	if err := private.Save(newEntry); err != nil {
		t.Fatalf("private append replaced prior rows: %v", err)
	}
	if err := group.Save(newEntry); err != nil {
		t.Fatalf("group append replaced prior rows: %v", err)
	}
	for _, query := range []string{
		`SELECT access_count FROM memory_entries WHERE scope_type = 'private' AND id = 'memory-a'`,
		`SELECT access_count FROM memory_entries WHERE scope_type = 'group' AND id = 'memory-a'`,
	} {
		var count int
		if err := db.SQL.QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("recall count %q = %d, %v", query, count, err)
		}
	}
	profile, err := group.GetProfile()
	if err != nil || len(profile.Members) != 2 || profile.Members["user-b"].Nickname != "user-b" ||
		profile.GroupName != "Synthetic Group" || profile.Members["user-a"].Role != GroupRoleAdmin ||
		profile.Members["user-a"].PreferredName != "Preferred" || len(profile.Members["user-a"].Aliases) != 1 {
		t.Fatalf("other member changed: %#v, %v", profile, err)
	}
}
