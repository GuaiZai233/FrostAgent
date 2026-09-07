package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAddServerStartupFailureKeepsCreatedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewConfigStore(filepath.Join(tmpDir, "mcp_servers.json"))
	manager := NewManagerWithFactory(store, nil, func(cfg TransportConfig) (officialmcp.Transport, error) {
		return nil, errors.New("simulated transport startup failure")
	})

	err := manager.AddServer(context.Background(), ServerConfig{
		ID:      "broken",
		Name:    "Broken MCP",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "broken-mcp",
		},
	})
	if err != nil {
		t.Fatalf("AddServer should succeed once the configuration is persisted, got %v", err)
	}

	srv, ok := manager.GetServer("broken")
	if !ok {
		t.Fatal("expected failed runtime to remain registered")
	}
	if srv.Status() != StatusFailed {
		t.Fatalf("expected failed runtime status, got %s", srv.Status())
	}
	if !strings.Contains(srv.LastError(), "simulated transport startup failure") {
		t.Fatalf("expected startup error to remain visible, got %q", srv.LastError())
	}

	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("load persisted config: %v", err)
	}
	if len(persisted.Servers) != 1 || persisted.Servers[0].ID != "broken" {
		t.Fatalf("expected broken server config to be persisted, got %+v", persisted.Servers)
	}
}

func TestAddServerPersistenceFailureRollsBackRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	blocker := filepath.Join(tmpDir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
		t.Fatalf("create blocker file: %v", err)
	}

	store := NewConfigStore(filepath.Join(blocker, "mcp_servers.json"))
	manager := NewManagerWithFactory(store, nil, nil)
	cfg := ServerConfig{
		ID:      "retryable",
		Name:    "Retryable MCP",
		Enabled: false,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "retryable-mcp",
		},
	}

	if err := manager.AddServer(context.Background(), cfg); err == nil {
		t.Fatal("expected persistence failure")
	}
	if _, ok := manager.GetServer("retryable"); ok {
		t.Fatal("persistence failure must roll back the in-memory server registration")
	}

	// A retry must fail for the persistence problem again, not because the first
	// attempt left a ghost entry behind in Manager.servers.
	err := manager.AddServer(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected persistence failure on retry")
	}
	if strings.Contains(err.Error(), "already exists") {
		t.Fatalf("retry was blocked by stale registration: %v", err)
	}
}
