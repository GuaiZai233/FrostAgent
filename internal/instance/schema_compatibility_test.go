package instance

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

const compatibleFutureInstanceID = "1234abcd"

// Construct only storage and the manager facade: these guards must run before
// any instance runtime, network connection, or filesystem mutation is needed.
func compatibilityGuardManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.CreateInstance(context.Background(), storage.InstanceRecord{
		ID: compatibleFutureInstanceID, Name: "Synthetic compatibility", CreatedAt: time.Now(),
	}, 2); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE computer_use_config (instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE, config_json TEXT NOT NULL)`,
		`CREATE TABLE computer_use_projects (instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE, id TEXT NOT NULL, metadata_json TEXT NOT NULL, PRIMARY KEY(instance_id, id))`,
		`CREATE TABLE computer_use_tasks (instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE, id TEXT NOT NULL, generation BIGINT NOT NULL, metadata_json TEXT NOT NULL, PRIMARY KEY(instance_id, id))`,
		`INSERT INTO computer_use_config VALUES ('1234abcd', '{"future_config":"synthetic configuration"}')`,
		`INSERT INTO computer_use_projects VALUES ('1234abcd', 'synthetic-project', '{"future_project":"synthetic metadata"}')`,
		`INSERT INTO computer_use_tasks VALUES ('1234abcd', 'synthetic-task', 3, '{"pending":{"state":"unknown"},"future_task":"retain exactly"}')`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{db: db, root: root}
}

func markCompatibleFutureSchema(t *testing.T, db *storage.DB) {
	t.Helper()
	if _, err := db.SQL.Exec(`UPDATE schema_meta SET version = 15, min_compatible_version = 14 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
}

func compatibilityGuardSnapshot(t *testing.T, db *storage.DB) []string {
	t.Helper()
	var snapshot []string
	for _, query := range []string{
		`SELECT CAST(version AS TEXT) || '/' || CAST(min_compatible_version AS TEXT) FROM schema_meta WHERE id = 1`,
		`SELECT name || '/' || CAST(enabled AS TEXT) || '/' || CAST(deleting AS TEXT) FROM instances WHERE id = '1234abcd'`,
		`SELECT CAST(COUNT(*) AS TEXT) FROM instances`,
		`SELECT CAST(value AS TEXT) FROM counters WHERE key = 'next_instance_number'`,
		`SELECT CAST(COUNT(*) AS TEXT) FROM pending_deletions`,
		`SELECT CAST(COUNT(*) AS TEXT) FROM provider_endpoints`,
		`SELECT config_json FROM computer_use_config WHERE instance_id = '1234abcd'`,
		`SELECT metadata_json FROM computer_use_projects WHERE instance_id = '1234abcd'`,
		`SELECT metadata_json || '/' || CAST(generation AS TEXT) FROM computer_use_tasks WHERE instance_id = '1234abcd'`,
	} {
		var value string
		if err := db.SQL.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, value)
	}
	return snapshot
}

func compatibilityDirectoryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func TestFullInstanceOperationsRejectCompatibleFutureSchemaBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation string
		pending   bool
		run       func(*Manager, []byte) error
	}{
		{"restore", "restore a complete instance backup", false, func(m *Manager, archive []byte) error {
			_, err := m.RestoreInstanceZIP("Synthetic restored", archive)
			return err
		}},
		{"prepare deletion", "prepare instance deletion", false, func(m *Manager, _ []byte) error {
			return m.PrepareDeletion(compatibleFutureInstanceID)
		}},
		{"confirm deletion", "confirm instance deletion", true, func(m *Manager, _ []byte) error {
			return m.ConfirmDeletion(compatibleFutureInstanceID)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := compatibilityGuardManager(t)
			instanceDir := m.dir(compatibleFutureInstanceID)
			if err := os.MkdirAll(instanceDir, 0700); err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(instanceDir, "future-asset.bin")
			if err := os.WriteFile(asset, []byte("synthetic future asset"), 0600); err != nil {
				t.Fatal(err)
			}
			archive, err := backup.BuildInstanceZIP(m.db, compatibleFutureInstanceID, filepath.Join(instanceDir, "sticker"))
			if err != nil {
				t.Fatal(err)
			}
			if test.pending {
				if err := m.db.PrepareDeletion(context.Background(), compatibleFutureInstanceID, backup.FormatVersion); err != nil {
					t.Fatal(err)
				}
			}
			markCompatibleFutureSchema(t, m.db)
			before := compatibilityGuardSnapshot(t, m.db)
			entriesBefore := compatibilityDirectoryNames(t, m.root)
			wantErr := m.db.RequireNativeSchema(context.Background(), test.operation)
			if wantErr == nil {
				t.Fatal("fixture did not enter future-schema compatibility mode")
			}
			if err := test.run(m, archive); err == nil || err.Error() != wantErr.Error() {
				t.Fatalf("expected compatibility guard before manager side effects: %v", err)
			}
			if after := compatibilityGuardSnapshot(t, m.db); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected operation changed data: before=%q after=%q", before, after)
			}
			if entriesAfter := compatibilityDirectoryNames(t, m.root); !reflect.DeepEqual(entriesBefore, entriesAfter) {
				t.Fatalf("rejected operation changed instance directories: before=%q after=%q", entriesBefore, entriesAfter)
			}
			if data, err := os.ReadFile(asset); err != nil || string(data) != "synthetic future asset" {
				t.Fatalf("rejected operation changed unknown asset: %q %v", data, err)
			}
		})
	}
}

func TestDeletionRecoveryPreservesCompatibleFutureSchemaStages(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "pending"}[known], func(t *testing.T) {
			m := compatibilityGuardManager(t)
			m.registry.Instances = []Info{{ID: compatibleFutureInstanceID, Deleting: true}}
			stageID := compatibleFutureInstanceID
			if !known {
				stageID = "feedcafe"
			}
			markCompatibleFutureSchema(t, m.db)
			// With no deletion stage, compatibility mode does not block startup.
			if err := m.recoverDeletionStages(); err != nil {
				t.Fatal(err)
			}
			stage, err := m.createDeletionStage(stageID)
			if err != nil {
				t.Fatal(err)
			}
			stagedInstance := filepath.Join(stage, "instance")
			if err := os.Mkdir(stagedInstance, 0700); err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(stagedInstance, "future-asset.bin")
			if err := os.WriteFile(asset, []byte("synthetic staged asset"), 0600); err != nil {
				t.Fatal(err)
			}
			before := compatibilityGuardSnapshot(t, m.db)
			wantErr := m.db.RequireNativeSchema(context.Background(), "recover instance deletion")
			if err := m.recoverDeletionStages(); err == nil || wantErr == nil || err.Error() != wantErr.Error() {
				t.Fatalf("future-schema deletion recovery was not rejected: %v", err)
			}
			if after := compatibilityGuardSnapshot(t, m.db); !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected recovery changed data: before=%q after=%q", before, after)
			}
			if data, err := os.ReadFile(asset); err != nil || string(data) != "synthetic staged asset" {
				t.Fatalf("recovery changed unknown staged asset: %q %v", data, err)
			}
			if _, err := os.Stat(m.dir(stageID)); !os.IsNotExist(err) {
				t.Fatalf("recovery moved the staged directory: %v", err)
			}
		})
	}
}
