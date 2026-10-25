package instance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

const syntheticStartupError = "SQL logic error: no such column: max_input_tokens (1)"

func seedDisabledStartupError(t *testing.T) (root, id string) {
	t.Helper()
	root = t.TempDir()
	manager, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	info, err := manager.Create("Synthetic recovery")
	if err != nil || info.Error != "" {
		t.Fatalf("seed instance: %+v %v", info, err)
	}
	if err := manager.update(info.ID, func(info *Info) { info.Error = syntheticStartupError }); err != nil {
		t.Fatal(err)
	}
	manager.Close()
	return root, info.ID
}

func TestDatabaseStartupClearsRecoveredDisabledError(t *testing.T) {
	root, id := seedDisabledStartupError(t)
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce a failed startup on the other branch's v12. Startup must
	// reconcile the columns and the old instance error, without enabling it.
	for _, statement := range []string{
		`ALTER TABLE provider_models DROP COLUMN max_input_tokens`,
		`ALTER TABLE memory_entries DROP COLUMN summary_dirty`,
		`UPDATE schema_meta SET version=12 WHERE id=1`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()
	for range 2 {
		manager, err := NewDatabase(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(manager.Close)
		infos, _ := manager.List()
		if len(infos) != 1 || infos[0].ID != id || infos[0].Error != "" || infos[0].Enabled {
			t.Fatalf("recovered instance retained an error or was enabled: %+v", infos)
		}
		item := manager.instances[id]
		if item.runtime == nil || item.config == nil || item.runtime.Scope.Context().Err() == nil {
			t.Fatal("recovery must construct a stopped management runtime")
		}
		records, _, err := manager.db.LoadInstances(context.Background())
		if err != nil || len(records) != 1 || records[0].Error != "" || records[0].Enabled {
			t.Fatalf("recovery status was not persisted: %+v %v", records, err)
		}
		response := httptest.NewRecorder()
		manager.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/instances", nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "max_input_tokens") {
			t.Fatalf("instance management retained stale error: %d %s", response.Code, response.Body.String())
		}
		manager.Close()
	}
}

func TestDatabaseStartupRetainsCurrentDisabledFailure(t *testing.T) {
	root, id := seedDisabledStartupError(t)
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	global, err := instanceconfig.OpenDatabase(db, "", true)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	current := errors.New("synthetic current initialization failure")
	manager, err := newManager(root, global, "", db,
		func(_ *Manager, _ *managed, _ *instanceconfig.Store, _ string, enabled bool) (*Runtime, error) {
			if enabled {
				t.Fatal("recovery attempted to enable the instance")
			}
			return nil, current
		})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	infos, _ := manager.List()
	if len(infos) != 1 || infos[0].ID != id || infos[0].Error != current.Error() || infos[0].Enabled {
		t.Fatalf("failed initialization was reported healthy: %+v", infos)
	}
}

func TestDatabaseStartupErrorClearFailureDoesNotReportSuccess(t *testing.T) {
	root, _ := seedDisabledStartupError(t)
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.SQL.Exec(`CREATE TRIGGER reject_error_clear BEFORE UPDATE ON instances WHEN NEW.error='' BEGIN SELECT RAISE(ABORT,'synthetic status persistence failure'); END`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	infos, _ := manager.List()
	records, _, err := manager.db.LoadInstances(context.Background())
	if err != nil || len(infos) != 1 || len(records) != 1 || infos[0].Error != syntheticStartupError || records[0].Error != syntheticStartupError || infos[0].Enabled || records[0].Enabled {
		t.Fatalf("failed status write was not retained: %+v %+v %v", infos, records, err)
	}
}
