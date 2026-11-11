package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestCompatibilityModuleMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var before int
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := db.ensureCompatibilityModules(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var count, after int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM module_migrations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("module migration count=%d, %v", count, err)
	}
	if err := db.SQL.QueryRow(`SELECT version FROM schema_meta WHERE id=1`).Scan(&after); err != nil || before != after {
		t.Fatal("supplemental migration changed core version")
	}
}

func TestCompatibilityModuleMigrationRollsBack(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`DELETE FROM module_migrations`, `ALTER TABLE provider_endpoints DROP COLUMN api_format`, `CREATE TRIGGER reject_module BEFORE INSERT ON module_migrations BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ensureCompatibilityModules(ctx); err == nil {
		t.Fatal("failed module migration succeeded")
	}
	columns, err := db.tableColumns(ctx, db.SQL, "provider_endpoints")
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.name == "api_format" {
			t.Fatal("failed migration left a partial column")
		}
	}
	if _, err := db.SQL.Exec(`DROP TRIGGER reject_module`); err != nil {
		t.Fatal(err)
	}
	if err := db.ensureCompatibilityModules(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibilityRestartPreservesRestoredTask(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES ('synthetic-instance','Synthetic','now')`); err != nil {
		t.Fatal(err)
	}
	task := ComputerUseTask{ID: "synthetic-task", ProjectID: "synthetic-project", ActorID: "synthetic-actor", ActorKind: "qq_private", State: "paused", Code: "restored_requires_rebind", Generation: 7, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", ReconcileRequired: true}
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", task); err != nil {
		t.Fatal(err)
	}
	if err := db.PauseComputerUseTasks(ctx, "synthetic-instance"); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ComputerUseTasks(ctx, "synthetic-instance")
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tasks[0], task) {
		before, _ := json.Marshal(task)
		after, _ := json.Marshal(tasks[0])
		t.Fatalf("restored history changed: %s -> %s", before, after)
	}
}
