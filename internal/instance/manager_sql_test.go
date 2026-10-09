package instance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseManagerIgnoresLegacyFilesAndRecoversInstance(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "instances.json"), []byte(`{"version":1,"next_number":9,"instances":[{"id":"deadbeef","name":"Legacy"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := m.List(); len(entries) != 0 {
		t.Fatalf("legacy JSON instances were loaded: %#v", entries)
	}
	info, err := m.Create("SQL Test")
	if err != nil {
		t.Fatal(err)
	}
	if info.Error != "" {
		t.Fatalf("new database instance failed to initialize: %s", info.Error)
	}
	if err := m.instances[info.ID].config.Update("BOT_NAME", "Database Bot", false); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	entries, next := m.List()
	if len(entries) != 1 || entries[0].ID != info.ID || next != 2 {
		t.Fatalf("SQL instance registry was not recovered: %#v, %d", entries, next)
	}
	if got := m.instances[info.ID].config.Get("BOT_NAME"); got != "Database Bot" {
		t.Fatalf("SQL setting was not recovered: %q", got)
	}
	for _, name := range []string{".env", "brain.json", "model_router.json", "mcp_servers.json", "dialogue.yml"} {
		if _, err := os.Stat(filepath.Join(root, "instance_"+info.ID, name)); !os.IsNotExist(err) {
			t.Fatalf("legacy runtime file %s was created: %v", name, err)
		}
	}
}

func TestPendingDeletionSurvivesRestartAndRemovesAllInstanceData(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	root := t.TempDir()
	m, err := NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Create("To Delete")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.instances[info.ID].config.Update("BOT_NAME", "Delete Me", false); err != nil {
		t.Fatal(err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != ErrDeleting {
		t.Fatalf("pending instance was enabled: %v", err)
	}
	m.Close()

	m, err = NewDatabase(root)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	list, _ := m.List()
	if len(list) != 1 || !list[0].Deleting || list[0].Enabled {
		t.Fatalf("pending deletion was not recovered: %#v", list)
	}
	archive, err := m.InstanceZIP(info.ID)
	if err != nil || len(archive) == 0 {
		t.Fatalf("backup unavailable during pending deletion: %v", err)
	}
	if err := m.ConfirmDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = m.List()
	if len(list) != 0 {
		t.Fatalf("deleted instance remains in registry: %#v", list)
	}
	var count int
	if err := m.db.SQL.QueryRow(`SELECT COUNT(*) FROM settings WHERE instance_id = ?`, info.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("instance settings remain: %d, %v", count, err)
	}
	if _, err := os.Stat(filepath.Join(root, "instance_"+info.ID)); !os.IsNotExist(err) {
		t.Fatalf("instance directory remains: %v", err)
	}
}

func TestPendingDeletionCanBeCancelled(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Keep")
	if err != nil || info.Error != "" {
		t.Fatalf("create instance: %#v, %v", info, err)
	}
	if err := m.PrepareDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelDeletion(info.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if len(list) != 1 || list[0].Deleting {
		t.Fatalf("cancelled deletion remains pending: %#v", list)
	}
	if m.instances[info.ID].runtime == nil {
		t.Fatal("management runtime was not restored")
	}
}
