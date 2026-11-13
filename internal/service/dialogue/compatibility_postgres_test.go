package dialogue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresDialogueCompatibilityDefaults(t *testing.T) {
	testPostgresDialogueCompatibilityColumn(t, false)
}

func TestPostgresDialogueCompatibilityIdentity(t *testing.T) {
	testPostgresDialogueCompatibilityColumn(t, true)
}

func testPostgresDialogueCompatibilityColumn(t *testing.T, identity bool) {
	base := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if base == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset; CI supplies a disposable database")
	}
	columns := []string{"scene", "relation"}
	if identity {
		columns = []string{"position"}
	}
	for _, column := range columns {
		t.Run(column, func(t *testing.T) {
			parsed, err := url.Parse(base)
			if err != nil {
				t.Fatal(err)
			}
			admin, err := sql.Open("pgx", base)
			if err != nil {
				t.Fatal(err)
			}
			schema := fmt.Sprintf("fa_dialogue_defaults_%d", time.Now().UnixNano())
			if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
				admin.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
					t.Error(err)
				}
				admin.Close()
			})
			query := parsed.Query()
			query.Set("search_path", schema)
			parsed.RawQuery = query.Encode()
			config := storage.Config{Backend: storage.Postgres, DSN: parsed.String()}
			root := t.TempDir()
			db, err := storage.OpenWithConfig(context.Background(), root, config)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.SQL.Exec(`INSERT INTO instances (id, name, created_at)
				VALUES ('synthetic-dialogue-instance', 'Synthetic dialogue', '2026-01-01T00:00:00Z')`); err != nil {
				t.Fatal(err)
			}
			original := []llm.DialogueExample{{ID: "synthetic-original", User: "original question", Preferred: "original answer"}}
			if err := SaveExamplesSQL(db, "synthetic-dialogue-instance", original); err != nil {
				t.Fatalf("native layout broke the real dialogue writer: %v", err)
			}
			change, code, diagnostic := " SET DEFAULT NULL", "23502", "default"
			if identity {
				change, code, diagnostic = " ADD GENERATED ALWAYS AS IDENTITY", "428C9", "identity"
			}
			if _, err := db.SQL.Exec(`ALTER TABLE dialogues ALTER COLUMN ` + column + change); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SQL.Exec(db.Bind(`UPDATE schema_meta SET version = ?, min_compatible_version = 14 WHERE id = 1`), storage.SchemaVersion+1); err != nil {
				t.Fatal(err)
			}
			before := dialogueCompatibilitySnapshot(t, db.SQL)
			err = SaveExamplesSQL(db, "synthetic-dialogue-instance", []llm.DialogueExample{{ID: "synthetic-replacement", User: "replacement", Preferred: "replacement"}})
			var pgError *pgconn.PgError
			if !errors.As(err, &pgError) || pgError.Code != code || (!identity && pgError.ColumnName != column) {
				t.Fatalf("changed column did not break the real dialogue writer: %v", err)
			}
			if after := dialogueCompatibilitySnapshot(t, db.SQL); !reflect.DeepEqual(before, after) {
				t.Fatal("failed dialogue save did not roll back rows, column attributes or metadata")
			}
			loaded, err := LoadExamplesSQL(db, "synthetic-dialogue-instance")
			if err != nil || !reflect.DeepEqual(loaded, original) {
				t.Fatalf("original examples were lost: %#v, %v", loaded, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			opened, err := storage.OpenWithConfig(context.Background(), root, config)
			if err == nil {
				opened.Close()
				t.Fatal("startup accepted a column that breaks the real dialogue writer")
			}
			if !strings.Contains(err.Error(), "dialogues") || !strings.Contains(err.Error(), column) || !strings.Contains(err.Error(), diagnostic) {
				t.Fatalf("startup did not identify the incompatible column: %v", err)
			}
			probe, err := sql.Open("pgx", config.DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			if after := dialogueCompatibilitySnapshot(t, probe); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected startup changed original examples, column attributes or metadata")
			}
		})
	}
}

func dialogueCompatibilitySnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var snapshot []string
	for _, query := range []string{
		`SELECT jsonb_agg(to_jsonb(d) ORDER BY instance_id, position)::text FROM dialogues d`,
		`SELECT jsonb_agg(to_jsonb(i) ORDER BY id)::text FROM instances i`,
		`SELECT jsonb_agg(to_jsonb(m) ORDER BY id)::text FROM schema_meta m`,
		`SELECT jsonb_agg(jsonb_build_array(attname, attgenerated, attidentity) ORDER BY attnum)::text
			FROM pg_catalog.pg_attribute WHERE attrelid = 'dialogues'::regclass AND attnum > 0 AND NOT attisdropped`,
		`SELECT jsonb_agg(jsonb_build_array(a.attname, pg_catalog.pg_get_expr(d.adbin, d.adrelid)) ORDER BY a.attnum)::text
			FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
			WHERE d.adrelid = 'dialogues'::regclass`,
	} {
		var value string
		if err := db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, value)
	}
	return snapshot
}
