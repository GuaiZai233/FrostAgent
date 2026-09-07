package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func waitForStatus(t *testing.T, srv *ServerRuntime, want ServerStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Status() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %s; got %s (lastError=%q)", want, srv.Status(), srv.LastError())
}

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
	waitForStatus(t, srv, StatusFailed)
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

func TestAddServerReturnsBeforeInitialStartupCompletes(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewConfigStore(filepath.Join(tmpDir, "mcp_servers.json"))
	entered := make(chan struct{})
	release := make(chan struct{})

	manager := NewManagerWithFactory(store, nil, func(cfg TransportConfig) (officialmcp.Transport, error) {
		close(entered)
		<-release
		return nil, errors.New("simulated slow startup")
	})

	done := make(chan error, 1)
	go func() {
		done <- manager.AddServer(context.Background(), ServerConfig{
			ID:      "slow",
			Name:    "Slow MCP",
			Enabled: true,
			Transport: TransportConfig{
				Type:    TransportStdio,
				Command: "slow-mcp",
			},
		})
	}()

	// AddServer must complete without waiting for the blocked transport factory.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AddServer failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AddServer blocked on initial runtime startup")
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("background startup never began")
	}
	close(release)

	srv, ok := manager.GetServer("slow")
	if !ok {
		t.Fatal("expected slow server to remain registered")
	}
	waitForStatus(t, srv, StatusFailed)
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

func TestRemovedRuntimeCannotBeStartedAfterDetach(t *testing.T) {
	started := false
	manager := NewManagerWithFactory(nil, nil, func(cfg TransportConfig) (officialmcp.Transport, error) {
		started = true
		return nil, errors.New("transport should not be created for retired runtime")
	})

	cfg := ServerConfig{
		ID:      "detached",
		Name:    "Detached MCP",
		Enabled: false,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "detached-mcp",
		},
	}
	if err := manager.AddServer(context.Background(), cfg); err != nil {
		t.Fatalf("AddServer failed: %v", err)
	}
	srv, ok := manager.GetServer("detached")
	if !ok {
		t.Fatal("expected runtime before removal")
	}
	if err := manager.RemoveServer("detached"); err != nil {
		t.Fatalf("RemoveServer failed: %v", err)
	}

	if err := srv.SetEnabled(context.Background(), true); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("expected retired runtime to reject re-enable, got %v", err)
	}
	if started {
		t.Fatal("retired runtime attempted to create a transport after ownership was revoked")
	}
}

func TestManagerCloseRetiresRuntime(t *testing.T) {
	started := false
	manager := NewManagerWithFactory(nil, nil, func(cfg TransportConfig) (officialmcp.Transport, error) {
		started = true
		return nil, errors.New("transport should not be created after manager close")
	})

	cfg := ServerConfig{
		ID:      "shutdown",
		Name:    "Shutdown MCP",
		Enabled: false,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "shutdown-mcp",
		},
	}
	if err := manager.AddServer(context.Background(), cfg); err != nil {
		t.Fatalf("AddServer failed: %v", err)
	}
	srv, ok := manager.GetServer("shutdown")
	if !ok {
		t.Fatal("expected runtime before manager close")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Manager.Close failed: %v", err)
	}

	if err := srv.SetEnabled(context.Background(), true); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("expected closed manager runtime to reject re-enable, got %v", err)
	}
	if started {
		t.Fatal("runtime attempted to create a transport after manager shutdown")
	}
}
