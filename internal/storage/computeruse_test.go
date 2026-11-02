package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func computerUseFixture(t *testing.T, db *DB, id string) {
	t.Helper()
	if err := db.CreateInstance(context.Background(), InstanceRecord{
		ID: id, Name: id, CreatedAt: time.Now(),
	}, 1); err != nil {
		t.Fatal(err)
	}
}

func computerUseTestTask(instanceID string) ComputerUseTask {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return ComputerUseTask{
		ID: "synthetic-task", ProjectID: "synthetic-project", ActorID: "synthetic-owner",
		ActorKind: "qq_private", State: "running", Generation: 2, ModelRounds: 3, ToolCalls: 5,
		GUIStopped: false, Prepared: true, Workspace: "synthetic-workspace", Branch: "synthetic-branch",
		CreatedAt: now, UpdatedAt: now,
		Pending: &ComputerUseOperation{
			ID: "synthetic-operation", InstanceID: instanceID, TaskID: "synthetic-task",
			ActorID: "synthetic-owner", Kind: "terminal.exec", RequestHash: strings.Repeat("a", 64),
			Generation: 2, State: "running",
		},
	}
}

func exerciseComputerUseMetadata(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"synthetic-instance", "synthetic-other"} {
		computerUseFixture(t, db, id)
	}
	config, err := db.ComputerUseConfig(ctx, "synthetic-instance")
	if err != nil || config.Enabled || config.AuditRetentionDays != 7 {
		t.Fatalf("unexpected default config: %+v, %v", config, err)
	}
	config.BridgeURL, config.Token, config.Enabled = "https://bridge.invalid:9443", "synthetic-secret", true
	config.AdapterToken = "synthetic-adapter-secret-for-testing-only"
	if err := db.SaveComputerUseConfig(ctx, "synthetic-instance", config); err != nil {
		t.Fatal(err)
	}
	gotConfig, err := db.ComputerUseConfig(ctx, "synthetic-instance")
	if err != nil || gotConfig != config {
		t.Fatalf("config round trip: %+v %v", gotConfig, err)
	}
	project := ComputerUseProject{
		ID: "synthetic-project", Path: "synthetic-repo", Remote: "ssh://git@repo.invalid/project.git",
		Baseline: "main", Shell: "synthetic-shell", Environment: map[string]string{"PATH": "synthetic-path"},
	}
	if err := db.SaveComputerUseProject(ctx, "synthetic-instance", project); err != nil {
		t.Fatal(err)
	}
	task := computerUseTestTask("synthetic-instance")
	exitCode := 0
	task.LastOperation = &ComputerUseOperation{
		ID: "synthetic-previous", InstanceID: "synthetic-instance", TaskID: task.ID, ActorID: task.ActorID,
		Kind: "desktop.input", Generation: 1, RequestHash: strings.Repeat("b", 64), State: "succeeded", ExitCode: &exitCode,
	}
	task.PauseOperation = &ComputerUseOperation{
		ID: "synthetic-pause", InstanceID: "synthetic-instance", TaskID: task.ID, ActorID: task.ActorID,
		Kind: "task.pause", Generation: 2, RequestHash: strings.Repeat("c", 64), State: "accepted",
	}
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", task); err != nil {
		t.Fatal(err)
	}
	stale := task
	stale.Generation, stale.Pending, stale.LastOperation, stale.PauseOperation = 1, nil, nil, nil
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", stale); !errors.Is(err, ErrComputerUseStaleGeneration) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	tasks, err := db.ComputerUseTasks(ctx, "synthetic-instance")
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], task) {
		t.Fatalf("task/pending atomic round trip: %+v, %v", tasks, err)
	}
	if err := db.PauseComputerUseTasks(ctx, "synthetic-instance"); err != nil {
		t.Fatal(err)
	}
	tasks, err = db.ComputerUseTasks(ctx, "synthetic-instance")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("read recovery: %v", err)
	}
	got := tasks[0]
	if got.State != "paused" || got.GUIStopped || !got.ReconcileRequired ||
		!reflect.DeepEqual(got.Pending, task.Pending) || !reflect.DeepEqual(got.LastOperation, task.LastOperation) ||
		!reflect.DeepEqual(got.PauseOperation, task.PauseOperation) || got.Generation != task.Generation {
		t.Fatalf("recovery lost operation facts or claimed GUI stopped: %+v", got)
	}
	otherTasks, err := db.ComputerUseTasks(ctx, "synthetic-other")
	if err != nil || len(otherTasks) != 0 {
		t.Fatalf("cross-instance task leak: %v", err)
	}
	otherConfig, err := db.ComputerUseConfig(ctx, "synthetic-other")
	if err != nil || otherConfig.Token != "" || otherConfig.AdapterToken != "" || otherConfig.Enabled {
		t.Fatalf("cross-instance credentials leak: %v", err)
	}
	if err := db.DeleteComputerUseProject(ctx, "synthetic-other", project.ID); err != nil {
		t.Fatal(err)
	}
	projects, err := db.ComputerUseProjects(ctx, "synthetic-instance")
	if err != nil || len(projects) != 1 || !reflect.DeepEqual(projects[0], project) {
		t.Fatalf("project round trip/isolation: %+v %v", projects, err)
	}
	if _, err := db.SQL.ExecContext(ctx, db.Bind(`DELETE FROM instances WHERE id = ?`), "synthetic-instance"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"computer_use_config", "computer_use_projects", "computer_use_tasks"} {
		var count int
		if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("instance metadata not cascaded: %s count=%d err=%v", table, count, err)
		}
	}
}

func TestComputerUseConfigRejectsOversizedAdapterToken(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	computerUseFixture(t, db, "synthetic-instance")
	config := DefaultComputerUseConfig()
	config.AdapterToken = strings.Repeat("a", 257)
	if err := db.SaveComputerUseConfig(context.Background(), "synthetic-instance", config); err == nil {
		t.Fatal("oversized adapter credential was accepted")
	}
	config.AdapterToken = "synthetic-independent-adapter-credential"
	config.Token = config.AdapterToken
	if err := db.SaveComputerUseConfig(context.Background(), "synthetic-instance", config); err == nil {
		t.Fatal("shared adapter and whole-machine credential was accepted")
	}
}

func TestSQLiteComputerUseMetadata(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exerciseComputerUseMetadata(t, db)
}

func TestPostgresComputerUseMetadata(t *testing.T) {
	db, err := OpenWithConfig(context.Background(), t.TempDir(), postgresMigrationConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exerciseComputerUseMetadata(t, db)
}

func TestComputerUseRejectsCredentialsAndArbitraryContent(t *testing.T) {
	for _, environment := range []map[string]string{
		{"API_KEY": "synthetic-secret"}, {"PATH": "first", "Path": "second"},
		{"PATH": "line\nbreak"},
	} {
		project := ComputerUseProject{ID: "synthetic-project", Environment: environment}
		if err := ValidateComputerUseProject(project); err == nil {
			t.Fatal("credential or ambiguous environment accepted")
		}
	}
	for _, remote := range []string{
		"https://synthetic-token@repo.invalid/project.git",
		"https://repo.invalid/project.git?token=synthetic-secret",
		"ssh://git:synthetic-secret@repo.invalid/project.git",
		"https://%zz@repo.invalid/project.git",
		"ftp://repo.invalid/project.git",
		"git:synthetic-secret@repo.invalid:project.git",
	} {
		if err := ValidateComputerUseProject(ComputerUseProject{ID: "synthetic-project", Remote: remote}); err == nil {
			t.Fatal("remote inline credentials accepted")
		}
	}
	for _, remote := range []string{"git@repo.invalid:project.git", "ssh://git@repo.invalid/project.git", "https://repo.invalid/project.git"} {
		if err := ValidateComputerUseProject(ComputerUseProject{ID: "synthetic-project", Remote: remote}); err != nil {
			t.Fatalf("valid remote %q rejected: %v", remote, err)
		}
	}
	task := computerUseTestTask("synthetic-instance")
	task.Code = "raw source with spaces"
	if err := ValidateComputerUseTask(task); err == nil {
		t.Fatal("raw output accepted as fixed code")
	}
	var decoded ComputerUseTask
	if err := decodeComputerUse(`{"id":"synthetic-task","prompt":"synthetic-secret"}`, &decoded); err == nil {
		t.Fatal("unexpected raw prompt property accepted")
	}
}

func TestSQLiteComputerUseRecoveryDoesNotMutateCompletedTasks(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	computerUseFixture(t, db, "synthetic-instance")
	task := computerUseTestTask("synthetic-instance")
	task.State, task.Pending = "succeeded", nil
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", task); err != nil {
		t.Fatal(err)
	}
	if err := db.PauseComputerUseTasks(ctx, "synthetic-instance"); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ComputerUseTasks(ctx, "synthetic-instance")
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], task) {
		t.Fatalf("completed task changed: %+v %v", tasks, err)
	}
}

func exerciseComputerUseMigrationRollback(t *testing.T, backend Backend) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	config := Config{Backend: backend}
	if backend == Postgres {
		config = postgresMigrationConfig(t)
	}
	db, err := OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	prepareOldSchema(t, db, 14)
	populateMigrationFixture(t, db)
	var step migration
	for _, candidate := range migrations {
		if candidate.version == 15 {
			step = migration{15, append([]string(nil), candidate.statements...)}
		}
	}
	if len(step.statements) != 3 {
		t.Fatal("computer-use v15 migration missing")
	}
	step.statements = append(step.statements, "THIS IS NOT SQL")
	if err := db.applyMigration(ctx, step); err == nil {
		t.Fatal("broken computer-use migration unexpectedly succeeded")
	}
	var version int
	if err := db.SQL.QueryRow("SELECT version FROM schema_meta WHERE id = 1").Scan(&version); err != nil || version != 14 {
		t.Fatalf("failed migration advanced schema: %d %v", version, err)
	}
	for _, table := range []string{"computer_use_config", "computer_use_projects", "computer_use_tasks"} {
		var count int
		if err := db.SQL.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err == nil {
			t.Fatalf("failed migration retained table %s", table)
		}
	}
	assertMigrationFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenWithConfig(ctx, root, config)
	if err != nil {
		t.Fatal(err)
	}
	assertMigrationFixture(t, db)
	if err := db.SQL.QueryRow("SELECT version FROM schema_meta WHERE id = 1").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("retry migration failed: %d %v", version, err)
	}
}

func TestSQLiteComputerUseMigrationRollbackAndRetry(t *testing.T) {
	exerciseComputerUseMigrationRollback(t, SQLite)
}

func TestPostgresComputerUseMigrationRollbackAndRetry(t *testing.T) {
	exerciseComputerUseMigrationRollback(t, Postgres)
}

func TestSQLiteComputerUseTaskWriteFailurePreservesPendingFact(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	computerUseFixture(t, db, "synthetic-instance")
	task := computerUseTestTask("synthetic-instance")
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", task); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`CREATE TRIGGER synthetic_write_failure BEFORE UPDATE ON computer_use_tasks
		BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	changed := task
	changed.Generation, changed.Pending, changed.State = 3, nil, "succeeded"
	if err := db.SaveComputerUseTask(ctx, "synthetic-instance", changed); err == nil {
		t.Fatal("injected write failure was ignored")
	}
	tasks, err := db.ComputerUseTasks(ctx, "synthetic-instance")
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], task) {
		t.Fatalf("failed write lost pending operation: %+v %v", tasks, err)
	}
}
