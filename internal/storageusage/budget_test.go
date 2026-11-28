package storageusage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// backendDB opens a real database for each supported backend. PostgreSQL runs
// only when CI supplies a DSN; each test gets its own schema.
func backendDB(t *testing.T, backend storage.Backend) *storage.DB {
	t.Helper()
	config := storage.Config{Backend: backend}
	if backend == storage.Postgres {
		base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
		if base == "" {
			t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset; the CI postgres job supplies a real database")
		}
		parsed, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("fa_usage_%d", time.Now().UnixNano())
		admin, err := sql.Open("pgx", base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
			admin.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		config.DSN = parsed.String()
	}
	db, err := storage.OpenWithConfig(context.Background(), t.TempDir(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		if _, err := db.SQL.Exec(db.Bind(`INSERT INTO instances(id,name,created_at) VALUES(?,?,?)`), id, id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func slowViewSQL(backend storage.Backend) string {
	if backend == storage.Postgres {
		return `CREATE VIEW slow_scan AS SELECT 'synthetic-a'::text AS instance_id, s.x::text AS v FROM (SELECT 1 AS x FROM pg_sleep(30)) s`
	}
	return `CREATE VIEW slow_scan AS WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 2000000000) SELECT 'synthetic-a' AS instance_id, x AS v FROM n`
}

func TestSlowScanIsCutOffAndSharedConnectionStaysUsable(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := backendDB(t, backend)
			if _, err := db.SQL.Exec(slowViewSQL(backend)); err != nil {
				t.Fatal(err)
			}
			collector := New(db, "synthetic-a", "", "", "")
			collector.queryTimeout = 200 * time.Millisecond

			begin := time.Now()
			_, err := collector.tableBytes(context.Background(), table{name: "slow_scan", category: CategoryOther, columns: []string{"v"}})
			if err == nil {
				t.Fatal("a scan far over the budget must fail")
			}
			if elapsed := time.Since(begin); elapsed > 5*time.Second {
				t.Fatalf("scan ran %v despite a 200ms budget", elapsed)
			}

			// Business SQL on the same single connection must work right away.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var one int
			if err := db.SQL.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatalf("shared connection unusable after the cut-off: %v", err)
			}
			if _, err := db.SQL.ExecContext(ctx, db.Bind(`INSERT INTO memory_entries(instance_id,scope_type,id,owner,content,source,created_at,updated_at) VALUES('synthetic-a','user','1','o','after','s','c','u')`)); err != nil {
				t.Fatalf("write after the cut-off failed: %v", err)
			}
		})
	}
}

func TestMeasureOnRealSchemaWithImagesAndMissingOptionalTables(t *testing.T) {
	for _, backend := range []storage.Backend{storage.SQLite, storage.Postgres} {
		t.Run(string(backend), func(t *testing.T) {
			db := backendDB(t, backend)
			// The image store's real migration shape: Base64 TEXT payload, decoded size.
			if _, err := db.SQL.Exec(`CREATE TABLE IF NOT EXISTS session_images (
				instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
				key TEXT NOT NULL, content_type TEXT NOT NULL, size BIGINT NOT NULL, data TEXT NOT NULL,
				created_at BIGINT NOT NULL, last_used_at BIGINT NOT NULL, PRIMARY KEY (instance_id, key))`); err != nil {
				t.Fatal(err)
			}
			encoded := base64.StdEncoding.EncodeToString(make([]byte, 2048))
			if _, err := db.SQL.Exec(db.Bind(`INSERT INTO session_images VALUES('synthetic-a','k','image/png',2048,?,1,1)`), encoded); err != nil {
				t.Fatal(err)
			}
			snapshot, err := New(db, "synthetic-a", "", "", "").measure(context.Background())
			if err != nil {
				t.Fatalf("measure failed (optional tables may be absent): %v", err)
			}
			if got := bytesOf(t, snapshot, CategoryImages); got < 2048 || got > 2048+32 {
				t.Fatalf("images bytes = %d, want the decoded size", got)
			}
		})
	}
}
