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

func TestInactiveManagerPersistsEnabledServerWithoutStarting(t *testing.T) {
	started := make(chan struct{}, 1)
	store := NewConfigStore(filepath.Join(t.TempDir(), "mcp_servers.json"))
	manager := NewManagerWithFactory(store, nil, func(cfg TransportConfig) (officialmcp.Transport, error) {
		started <- struct{}{}
		return nil, errors.New("synthetic transport failure")
	})
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.SetRuntimeActive(false); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddServer(context.Background(), ServerConfig{
		ID:      "offline",
		Name:    "Offline",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "synthetic",
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("inactive manager started an enabled MCP server")
	case <-time.After(50 * time.Millisecond):
	}
	updated := manager.ListServers()[0].Config()
	updated.Name = "Updated Offline"
	if err := manager.UpdateServer(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetServerEnabled(context.Background(), "offline", false); err != nil {
		t.Fatal(err)
	}
	if err := manager.SetServerEnabled(context.Background(), "offline", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("editing inactive MCP configuration started a connection")
	case <-time.After(50 * time.Millisecond):
	}
	srv, ok := manager.GetServer("offline")
	if !ok {
		t.Fatal("inactive manager lost the configured server")
	}
	if !srv.IsEnabled() || srv.Status() != StatusStopped {
		t.Fatalf("inactive desired state was not retained: enabled=%t status=%v", srv.IsEnabled(), srv.Status())
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Servers) != 1 || !persisted.Servers[0].Enabled {
		t.Fatalf("inactive enabled state was not persisted: %+v", persisted.Servers)
	}
	if err = manager.SetRuntimeActive(true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("activating the owning instance did not start configured MCP servers")
	}
}

func TestClosedManagerRejectsAllMutationsWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp_servers.json")
	manager := NewManager(NewConfigStore(path), nil)
	cfg := ServerConfig{
		ID:      "existing",
		Name:    "Existing",
		Enabled: false,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "synthetic",
		},
	}
	if err := manager.AddServer(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}

	late := cfg
	late.ID = "late"
	for name, mutate := range map[string]func() error{
		"add":         func() error { return manager.AddServer(context.Background(), late) },
		"update":      func() error { return manager.UpdateServer(context.Background(), cfg) },
		"remove":      func() error { return manager.RemoveServer(cfg.ID) },
		"toggle":      func() error { return manager.SetServerEnabled(context.Background(), cfg.ID, true) },
		"tool policy": func() error { return manager.SetToolEnabled(cfg.ID, "tool", true) },
		"sync":        func() error { return manager.SyncServer(context.Background(), cfg.ID) },
		"restart":     func() error { return manager.RestartServer(context.Background(), cfg.ID) },
		"activate":    func() error { return manager.SetRuntimeActive(true) },
		"load":        manager.Load,
	} {
		if err := mutate(); !errors.Is(err, ErrManagerClosed) {
			t.Fatalf("%s after close error = %v, want ErrManagerClosed", name, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("closed manager changed its persisted configuration")
	}
}

func TestReservedAsyncStartCannotSupersedeNewerRestart(t *testing.T) {
	factoryCalls := 0
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		factoryCalls++
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		server := officialmcp.NewServer(&officialmcp.Implementation{Name: "reserved-start", Version: "1.0"}, nil)
		server.AddTool(&officialmcp.Tool{
			Name:        "ping",
			Description: "ping",
			InputSchema: map[string]any{"type": "object"},
		}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
			return &officialmcp.CallToolResult{
				Content: []officialmcp.Content{&officialmcp.TextContent{Text: "pong"}},
			}, nil
		})
		if _, err := server.Connect(context.Background(), serverTransport, nil); err != nil {
			return nil, err
		}
		return clientTransport, nil
	}

	srv := NewServerRuntimeWithFactory(ServerConfig{
		ID:      "reserved",
		Name:    "Reserved Start",
		Enabled: true,
		Transport: TransportConfig{
			Type:    TransportStdio,
			Command: "reserved",
		},
	}, factory)
	defer srv.Stop()

	// Simulate StartAsync having synchronously reserved ownership while its queued
	// goroutine has not yet begun transport work.
	oldReservation, err := srv.reserveStart()
	if err != nil || oldReservation == nil {
		t.Fatalf("reserve old async start: reservation=%v err=%v", oldReservation, err)
	}

	// A newer explicit restart must supersede that queued reservation.
	if err := srv.Restart(context.Background()); err != nil {
		t.Fatalf("newer Restart failed: %v", err)
	}
	if srv.Status() != StatusConnected {
		t.Fatalf("expected newer restart to connect, got %s", srv.Status())
	}
	if factoryCalls != 1 {
		t.Fatalf("expected exactly one transport for newer restart, got %d", factoryCalls)
	}

	// When the old queued work finally runs, it must exit before creating a
	// transport or canceling/replacing the newer session.
	err = srv.runReservedStart(context.Background(), oldReservation)
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("expected stale reserved start to be rejected, got %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("stale reserved start created another transport; calls=%d", factoryCalls)
	}
	if srv.Status() != StatusConnected {
		t.Fatalf("stale reserved start disturbed newer connection, status=%s", srv.Status())
	}
}

func TestConcurrentAddRemovePersistsLatestManagerState(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "mcp_servers.json"))
	manager := NewManagerWithFactory(store, nil, nil)

	// Hold the store lock so Add can take a Manager snapshot and then block before
	// its durable write. This creates the stale-snapshot window deterministically.
	store.mu.Lock()
	storeLocked := true
	defer func() {
		if storeLocked {
			store.mu.Unlock()
		}
	}()

	addDone := make(chan error, 1)
	go func() {
		addDone <- manager.AddServer(context.Background(), ServerConfig{
			ID:      "racy",
			Name:    "Racy MCP",
			Enabled: false,
			Transport: TransportConfig{
				Type:    TransportStdio,
				Command: "racy-mcp",
			},
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := manager.GetServer("racy"); ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := manager.GetServer("racy"); !ok {
		t.Fatal("AddServer never inserted the test server")
	}

	// Wait until Add's saveConfig owns the Manager persistence boundary and is
	// blocked inside ConfigStore.Save on store.mu.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !manager.persistMu.TryLock() {
			break
		}
		manager.persistMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	if manager.persistMu.TryLock() {
		manager.persistMu.Unlock()
		t.Fatal("AddServer never entered the persistence critical section")
	}

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- manager.RemoveServer("racy")
	}()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := manager.GetServer("racy"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := manager.GetServer("racy"); ok {
		t.Fatal("RemoveServer did not remove the server from memory")
	}

	// Let the older Add write finish first. Remove's save is waiting on persistMu;
	// once it acquires the boundary it must take a fresh snapshot and win last.
	store.mu.Unlock()
	storeLocked = false

	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("AddServer failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AddServer did not finish after store was released")
	}
	select {
	case err := <-removeDone:
		if err != nil {
			t.Fatalf("RemoveServer failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveServer did not finish after Add persistence completed")
	}

	if _, ok := manager.GetServer("racy"); ok {
		t.Fatal("server unexpectedly reappeared in Manager memory")
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatalf("load final persisted config: %v", err)
	}
	for _, cfg := range persisted.Servers {
		if cfg.ID == "racy" {
			t.Fatalf("stale Add snapshot resurrected deleted server on disk: %+v", persisted.Servers)
		}
	}
}
