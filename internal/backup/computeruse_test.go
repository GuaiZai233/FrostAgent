package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/storage"
)

func computerUseBackupDatabase(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"synthetic-source", "synthetic-target"} {
		if err := db.CreateInstance(context.Background(), storage.InstanceRecord{
			ID: id, Name: id, CreatedAt: time.Now(),
		}, 1); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedComputerUseBackup(t *testing.T, db *storage.DB, id string) storage.ComputerUseTask {
	t.Helper()
	ctx := context.Background()
	config := storage.DefaultComputerUseConfig()
	config.Enabled, config.Token, config.CAPEM = true, "synthetic-bridge-secret", "synthetic-private-ca"
	config.BridgeURL = "https://synthetic-bridge.invalid"
	if err := db.SaveComputerUseConfig(ctx, id, config); err != nil {
		t.Fatal(err)
	}
	project := storage.ComputerUseProject{ID: "synthetic-project", Path: "synthetic-repo", Remote: "https://repo.invalid/project.git",
		Baseline: "main", Shell: "synthetic-shell", Environment: map[string]string{"PATH": "synthetic-private-path"}}
	if err := db.SaveComputerUseProject(ctx, id, project); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	task := storage.ComputerUseTask{
		ID: "synthetic-task", ProjectID: project.ID, ActorID: "synthetic-actor", ActorKind: "qq_private",
		BridgeURL: config.BridgeURL, State: "running", Generation: 3, Prepared: true, GUIStopped: true,
		Workspace: "synthetic-workspace", Branch: "synthetic-branch", CreatedAt: now, UpdatedAt: now,
		Pending: &storage.ComputerUseOperation{ID: "synthetic-operation", InstanceID: id, TaskID: "synthetic-task",
			ActorID: "synthetic-actor", Kind: "terminal.exec", RequestHash: strings.Repeat("a", 64),
			Generation: 3, State: "unknown", Code: "bridge_restart"},
		PauseOperation: &storage.ComputerUseOperation{ID: "synthetic-pause", InstanceID: id, TaskID: "synthetic-task",
			ActorID: "synthetic-actor", Kind: "task.pause", RequestHash: strings.Repeat("b", 64),
			Generation: 3, State: "running"},
	}
	if err := db.SaveComputerUseTask(ctx, id, task); err != nil {
		t.Fatal(err)
	}
	return task
}

func readComputerUseZIP(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, member := range reader.File {
		reader, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[member.Name] = data
	}
	return files
}

func writeComputerUseZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for name, data := range files {
		member, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestComputerUseInstanceZIPExportsMetadataOnlyAndRestoresPaused(t *testing.T) {
	db := computerUseBackupDatabase(t)
	task := seedComputerUseBackup(t, db, "synthetic-source")
	archive, err := BuildInstanceZIP(db, "synthetic-source", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := readComputerUseZIP(t, archive)
	for _, member := range files {
		for _, secret := range []string{"synthetic-bridge-secret", "synthetic-private-ca", "synthetic-private-path"} {
			if bytes.Contains(member, []byte(secret)) {
				t.Fatalf("archive member contains private config: %s", secret)
			}
		}
	}
	decoded, err := DecodeInstanceZIP(archive)
	if err != nil || decoded.ComputerUse == nil {
		t.Fatalf("decode computer-use member: %v", err)
	}
	if len(decoded.ComputerUse.Tasks) != 1 || !reflect.DeepEqual(decoded.ComputerUse.Tasks[0], task) {
		t.Fatalf("export lost operation facts: %+v", decoded.ComputerUse)
	}
	seedComputerUseBackup(t, db, "synthetic-target")
	if err := ImportComputerUse(db, "synthetic-target", *decoded.ComputerUse); err != nil {
		t.Fatal(err)
	}
	config, err := db.ComputerUseConfig(context.Background(), "synthetic-target")
	if err != nil || config.Enabled || config.Token != "" || config.BridgeURL != "" || config.CAPEM != "" {
		t.Fatalf("restored credentials or live binding: %+v %v", config, err)
	}
	tasks, err := db.ComputerUseTasks(context.Background(), "synthetic-target")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("restore tasks: %+v %v", tasks, err)
	}
	got := tasks[0]
	if got.State != "paused" || !got.ReconcileRequired || got.Prepared || got.GUIStopped ||
		got.BridgeURL != "" || got.Pending != nil || got.PauseOperation != nil ||
		!reflect.DeepEqual(got.LastOperation, task.Pending) || got.Generation != task.Generation {
		t.Fatalf("restore enabled automatic execution or lost unknown fact: %+v", got)
	}
	projects, err := db.ComputerUseProjects(context.Background(), "synthetic-target")
	if err != nil || len(projects) != 1 || len(projects[0].Environment) != 0 {
		t.Fatalf("restored project environment: %+v %v", projects, err)
	}
	// Existing v1 instance backups remain valid when the optional member is absent.
	delete(files, "computer_use.json")
	older, err := DecodeInstanceZIP(writeComputerUseZIP(t, files))
	if err != nil || older.ComputerUse != nil {
		t.Fatalf("old ZIP compatibility lost: %+v %v", older.ComputerUse, err)
	}
}

func TestComputerUseImportIsAtomicWithSettings(t *testing.T) {
	db := computerUseBackupDatabase(t)
	originalTask := seedComputerUseBackup(t, db, "synthetic-target")
	config, err := instanceconfig.OpenDatabase(db, "synthetic-target", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update("BOT_NAME", "Original", false); err != nil {
		t.Fatal(err)
	}
	settings, err := ExportSettings(db, "synthetic-target")
	if err != nil {
		t.Fatal(err)
	}
	settings.Values["BOT_NAME"] = "Changed"
	invalid := ComputerUse{FormatVersion: FormatVersion, Tasks: []storage.ComputerUseTask{{ID: "invalid"}}}
	if err := ImportSettingsWithComputerUse(db, "synthetic-target", settings, invalid); err == nil {
		t.Fatal("invalid computer metadata accepted")
	}
	config, err = instanceconfig.OpenDatabase(db, "synthetic-target", false)
	if err != nil || config.Get("BOT_NAME") != "Original" {
		t.Fatalf("settings changed after rollback: %v", err)
	}
	tasks, err := db.ComputerUseTasks(context.Background(), "synthetic-target")
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0], originalTask) {
		t.Fatalf("tasks changed after rollback: %+v %v", tasks, err)
	}
	bridgeConfig, err := db.ComputerUseConfig(context.Background(), "synthetic-target")
	if err != nil || bridgeConfig.Token != "synthetic-bridge-secret" {
		t.Fatalf("credentials removed after rollback: %v", err)
	}
	valid := ComputerUse{FormatVersion: FormatVersion}
	if err := ImportSettingsWithComputerUse(db, "synthetic-target", settings, valid); err != nil {
		t.Fatal(err)
	}
	config, err = instanceconfig.OpenDatabase(db, "synthetic-target", false)
	if err != nil || config.Get("BOT_NAME") != "Changed" {
		t.Fatalf("valid transaction did not apply: %v", err)
	}
	bridgeConfig, err = db.ComputerUseConfig(context.Background(), "synthetic-target")
	if err != nil || bridgeConfig.Enabled || bridgeConfig.Token != "" {
		t.Fatalf("copy retained destination bridge binding: %+v %v", bridgeConfig, err)
	}
}

func TestComputerUseZIPRejectsUnknownFieldsAndDuplicateIDs(t *testing.T) {
	db := computerUseBackupDatabase(t)
	seedComputerUseBackup(t, db, "synthetic-source")
	archive, err := BuildInstanceZIP(db, "synthetic-source", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := readComputerUseZIP(t, archive)
	for _, payload := range []string{
		`{"format_version":1,"prompt":"synthetic-secret"}`,
		`{"format_version":1} {"prompt":"synthetic-secret"}`,
		`{"format_version":1,"tasks":[{"id":"task","raw_output":"synthetic-secret"}]}`,
		`{"format_version":1,"projects":[{"id":"synthetic"},{"id":"synthetic"}]}`,
	} {
		files["computer_use.json"] = []byte(payload)
		if _, err := DecodeInstanceZIP(writeComputerUseZIP(t, files)); err == nil {
			t.Fatal("invalid computer-use backup was accepted")
		}
	}
}

func TestComputerUseImportStripsUploadedEnvironment(t *testing.T) {
	db := computerUseBackupDatabase(t)
	data := ComputerUse{FormatVersion: FormatVersion, Projects: []storage.ComputerUseProject{
		{ID: "synthetic-project", Environment: map[string]string{"PATH": "synthetic-uploaded-path"}},
	}}
	if err := ImportComputerUse(db, "synthetic-target", data); err != nil {
		t.Fatal(err)
	}
	projects, err := db.ComputerUseProjects(context.Background(), "synthetic-target")
	if err != nil || len(projects) != 1 || projects[0].Environment != nil {
		t.Fatalf("uploaded environment not stripped: %+v %v", projects, err)
	}
	raw, _ := json.Marshal(data)
	if !bytes.Contains(raw, []byte("synthetic-uploaded-path")) {
		t.Fatal("import mutated caller data")
	}
}
