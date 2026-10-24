package instance

import (
	"context"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/storage"
)

func seedInstanceComputerUse(t *testing.T, m *Manager, id string) {
	t.Helper()
	ctx := context.Background()
	config := storage.DefaultComputerUseConfig()
	config.Enabled, config.Token, config.BridgeURL = true, "synthetic-bridge-secret", "https://bridge.invalid"
	if err := m.db.SaveComputerUseConfig(ctx, id, config); err != nil {
		t.Fatal(err)
	}
	project := storage.ComputerUseProject{ID: "synthetic-project", Path: "synthetic-path", Baseline: "main", Shell: "synthetic-shell"}
	if err := m.db.SaveComputerUseProject(ctx, id, project); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	task := storage.ComputerUseTask{
		ID: "synthetic-task", ProjectID: project.ID, ActorID: "synthetic-actor", ActorKind: "management",
		State: "running", Generation: 1, Prepared: true, BridgeURL: config.BridgeURL, CreatedAt: now, UpdatedAt: now,
		Pending: &storage.ComputerUseOperation{
			ID: "synthetic-operation", InstanceID: id, TaskID: "synthetic-task", ActorID: "synthetic-actor",
			Kind: "file.write", RequestHash: strings.Repeat("a", 64), State: "unknown", Generation: 1,
		},
	}
	if err := m.db.SaveComputerUseTask(ctx, id, task); err != nil {
		t.Fatal(err)
	}
}

func assertInstanceComputerUseDetached(t *testing.T, m *Manager, id string) {
	t.Helper()
	ctx := context.Background()
	config, err := m.db.ComputerUseConfig(ctx, id)
	if err != nil || config.Enabled || config.Token != "" || config.BridgeURL != "" {
		t.Fatalf("destination retained bridge permission: %+v %v", config, err)
	}
	projects, err := m.db.ComputerUseProjects(ctx, id)
	if err != nil || len(projects) != 1 || projects[0].ID != "synthetic-project" {
		t.Fatalf("project metadata missing: %+v %v", projects, err)
	}
	tasks, err := m.db.ComputerUseTasks(ctx, id)
	if err != nil || len(tasks) != 1 || tasks[0].State != "paused" ||
		tasks[0].Pending != nil || tasks[0].Prepared || !tasks[0].ReconcileRequired ||
		tasks[0].LastOperation == nil || tasks[0].LastOperation.State != "unknown" {
		t.Fatalf("task metadata not safely paused: %+v %v", tasks, err)
	}
}

func TestSQLCopyAndZIPRestoreComputerUseMetadataWithoutBridgePermission(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	source, err := m.Create("Synthetic Source")
	if err != nil || source.Error != "" {
		t.Fatalf("source: %+v %v", source, err)
	}
	target, err := m.Create("Synthetic Target")
	if err != nil || target.Error != "" {
		t.Fatalf("target: %+v %v", target, err)
	}
	seedInstanceComputerUse(t, m, source.ID)
	seedInstanceComputerUse(t, m, target.ID)
	if err := m.Copy(target.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	assertInstanceComputerUseDetached(t, m, target.ID)
	archive, err := m.InstanceZIP(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := m.RestoreInstanceZIP("Synthetic Restored", archive)
	if err != nil {
		t.Fatal(err)
	}
	assertInstanceComputerUseDetached(t, m, restored.ID)
	sourceConfig, err := m.db.ComputerUseConfig(context.Background(), source.ID)
	if err != nil || !sourceConfig.Enabled || sourceConfig.Token != "synthetic-bridge-secret" {
		t.Fatalf("copy/restore changed source trust: %+v %v", sourceConfig, err)
	}
}
