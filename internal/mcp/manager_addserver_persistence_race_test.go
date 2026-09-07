package mcp

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAddServerPersistenceDelayCannotSupersedeNewerRestart covers the lifecycle
// window where AddServer has published the runtime but is still blocked persisting
// configuration. A newer explicit Restart must win permanently; when the older Add
// resumes, its initial-start reservation must already be stale and must not create a
// second transport or replace the newer session.
func TestAddServerPersistenceDelayCannotSupersedeNewerRestart(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "mcp_servers.json"))

	var factoryCalls atomic.Int32
	factory := func(cfg TransportConfig) (officialmcp.Transport, error) {
		factoryCalls.Add(1)

		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		server := officialmcp.NewServer(&officialmcp.Implementation{
			Name:    "persist-race",
			Version: "1.0",
		}, nil)
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

	manager := NewManagerWithFactory(store, nil, factory)

	// Block the durable write. AddServer can reserve and publish its runtime, but
	// cannot reach the point where it submits the reserved initial start.
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
			ID:      "persist-race",
			Name:    "Persistence Race",
			Enabled: true,
			Transport: TransportConfig{
				Type:    TransportStdio,
				Command: "persist-race",
			},
		})
	}()

	var srv *ServerRuntime
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if current, ok := manager.GetServer("persist-race"); ok {
			srv = current
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srv == nil {
		t.Fatal("AddServer never published the runtime")
	}
	if srv.Status() != StatusStarting {
		t.Fatalf("initial startup should already be reserved before persistence, got status=%s", srv.Status())
	}

	// Ensure Add owns the Manager persistence boundary and therefore cannot finish
	// until store.mu is released.
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

	// This is the newer lifecycle operation. It must invalidate Add's earlier
	// reservation and become the only connection attempt that reaches the factory.
	if err := manager.RestartServer(context.Background(), "persist-race"); err != nil {
		t.Fatalf("RestartServer failed while Add persistence was blocked: %v", err)
	}
	if srv.Status() != StatusConnected {
		t.Fatalf("newer Restart did not connect: status=%s lastError=%q", srv.Status(), srv.LastError())
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("expected exactly one transport after explicit Restart, got %d", got)
	}

	srv.mu.RLock()
	winningGeneration := srv.generation
	winningSession := srv.session
	srv.mu.RUnlock()
	if winningSession == nil {
		t.Fatal("newer Restart connected without publishing a session")
	}

	// Let the older Add finish persistence. It will submit only its already-reserved
	// token; synchronous stale preflight must reject it without queueing new work.
	store.mu.Unlock()
	storeLocked = false

	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("AddServer failed after persistence was released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AddServer did not finish after persistence was released")
	}

	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("older Add startup created an extra transport after newer Restart; calls=%d", got)
	}

	srv.mu.RLock()
	finalGeneration := srv.generation
	finalSession := srv.session
	srv.mu.RUnlock()
	if finalGeneration != winningGeneration {
		t.Fatalf("older Add changed lifecycle generation after newer Restart: before=%d after=%d", winningGeneration, finalGeneration)
	}
	if finalSession != winningSession {
		t.Fatal("older Add replaced the session established by newer Restart")
	}
	if srv.Status() != StatusConnected {
		t.Fatalf("older Add disturbed newer connected lifecycle: status=%s lastError=%q", srv.Status(), srv.LastError())
	}

	_ = manager.Close()
}
