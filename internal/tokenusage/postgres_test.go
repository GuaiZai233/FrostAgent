package tokenusage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestPostgresTokenUsagePersistence(t *testing.T) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("fa_usage_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
		admin.Close()
	}()
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), storage.Config{Backend: storage.Postgres, DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at) VALUES(?,?,?)`), id, id, "now"); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := New(db, "synthetic-a", "")
	b, _ := New(db, "synthetic-a", "")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := a
			if i%2 == 0 {
				store = b
			}
			if err := store.Record("embedding", &core.Usage{PromptTokens: 3, TotalTokens: 3}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := row(t, b, "embedding"); got.Requests != 16 || got.Total.Value != 48 {
		t.Fatalf("lost update: %+v", got)
	}
	other, _ := New(db, "synthetic-b", "")
	if got := row(t, other, "embedding"); got.Requests != 0 {
		t.Fatal("instance isolation failed")
	}
	// PostgreSQL aborts a transaction on a statement error. The marker must
	// be written only after rollback, without waiting for another response.
	if _, err := db.SQL.Exec(`ALTER TABLE settings ADD CONSTRAINT reject_usage CHECK (scope <> 'token_usage' OR key <> 'dialogue')`); err != nil {
		t.Fatal(err)
	}
	if err := a.Record("dialogue", nil); err == nil {
		t.Fatal("expected SQL accounting failure")
	}
	if _, err := db.SQL.Exec(`ALTER TABLE settings DROP CONSTRAINT reject_usage`); err != nil {
		t.Fatal(err)
	}
	reopened, _ := New(db, "synthetic-a", "")
	snap, err := reopened.Snapshot(context.Background())
	if err != nil || !snap.Incomplete {
		t.Fatalf("marker missing after reopen: %+v %v", snap, err)
	}
	if err := other.Restore(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	restored, err := other.Snapshot(context.Background())
	if err != nil || !restored.Incomplete {
		t.Fatalf("restore lost marker: %+v %v", restored, err)
	}
	if got := row(t, other, "embedding"); got.Total.Value != 48 {
		t.Fatal("restore lost usage")
	}
	if err := db.PrepareDeletion(context.Background(), "synthetic-a", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.ConfirmDeletion(context.Background(), "synthetic-a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Record("dialogue", nil); err == nil {
		t.Fatal("deleted usage resurrected")
	}
	var count int
	if err := db.SQL.QueryRow(db.Bind(`SELECT COUNT(*) FROM settings WHERE scope=? AND instance_id=?`), Scope, "synthetic-a").Scan(&count); err != nil || count != 0 {
		t.Fatalf("usage survived deletion: %d %v", count, err)
	}
}
