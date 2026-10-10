package mcp

import (
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestSQLConfigStoreRoundTrip(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "mcp.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	store := NewSQLConfigStore(db, "test-instance")
	cfg := &Config{Servers: []ServerConfig{{
		ID: "server-a", Name: "Example", Enabled: true,
		Transport: TransportConfig{Type: TransportStdio, Command: "program", Args: []string{"--flag"},
			Env: map[string]string{"TEST_OPTION": "value"}},
		Tools: map[string]ToolPolicy{"tool-a": {Enabled: false}},
	}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewSQLConfigStore(db, "test-instance").Load()
	if err != nil || len(loaded.Servers) != 1 || loaded.Servers[0].Transport.Args[0] != "--flag" ||
		loaded.Servers[0].Tools["tool-a"].Enabled {
		t.Fatalf("MCP SQL round trip failed: %#v, %v", loaded, err)
	}
	if err := store.Save(&Config{}); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil || len(loaded.Servers) != 0 {
		t.Fatalf("MCP replacement failed: %#v, %v", loaded, err)
	}
}
