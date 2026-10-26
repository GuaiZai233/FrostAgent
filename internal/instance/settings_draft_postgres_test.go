package instance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresSettingsDraftCommitAndRollback(t *testing.T) {
	dsn := os.Getenv("FROSTAGENT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("FROSTAGENT_TEST_POSTGRES_DSN is unset")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("fa_draft_%d", time.Now().UnixNano())
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
	root := t.TempDir()
	db, err := storage.OpenWithConfig(context.Background(), root, storage.Config{Backend: storage.Postgres, DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewDatabaseWithDB(root, db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Postgres Draft")
	if err != nil {
		t.Fatal(err)
	}
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"saved"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-saved"}]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("batch commit failed: %s", response.Body.String())
	}
	for _, statement := range []string{
		`CREATE FUNCTION reject_draft() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.key = 'MCP_CONTROL_TOKEN' AND NEW.value = 'synthetic-rejected' THEN RAISE EXCEPTION 'synthetic write failure'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER reject_draft BEFORE INSERT OR UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION reject_draft()`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	response = applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"partial"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-rejected"}]}`)
	if strings.Contains(response.Body.String(), `"success":true`) || m.instances[info.ID].config.Get("BOT_NAME") != "saved" || m.global.Get("MCP_CONTROL_TOKEN") != "synthetic-saved" {
		t.Fatalf("SQL failure left a partial batch: %s", response.Body.String())
	}
	calls := 0
	m.SetGlobalApplyHook(func() error {
		calls++
		if calls == 1 {
			return errors.New("synthetic apply failure")
		}
		return m.ApplyGlobalSettings()
	})
	response = applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"partial"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-new"}]}`)
	if response.Code == http.StatusOK || m.instances[info.ID].config.Get("BOT_NAME") != "saved" || m.global.Get("MCP_CONTROL_TOKEN") != "synthetic-saved" || calls != 2 {
		t.Fatalf("apply failure did not restore both scopes: %s", response.Body.String())
	}
}
