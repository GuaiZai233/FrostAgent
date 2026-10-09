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
