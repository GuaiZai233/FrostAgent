package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"FrostAgent/internal/core"
)

// Manager manages all MCP server runtimes and tool catalogs for an instance.
type Manager struct {
	mu           sync.RWMutex
	servers      map[string]*ServerRuntime
	store        *ConfigStore
	builtinNames map[string]struct{}

	// factory for testing
	transportFactory func(cfg TransportConfig) (Transport, error)
}

func NewManager(store *ConfigStore, builtinNames []string) *Manager {
	return NewManagerWithFactory(store, builtinNames, nil)
}

func NewManagerWithFactory(store *ConfigStore, builtinNames []string, factory func(cfg TransportConfig) (Transport, error)) *Manager {
	builtins := make(map[string]struct{}, len(builtinNames))
	for _, name := range builtinNames {
		builtins[name] = struct{}{}
	}
	return &Manager{
		servers:          make(map[string]*ServerRuntime),
		store:            store,
		builtinNames:     builtins,
		transportFactory: factory,
	}
}

func (m *Manager) RegisterBuiltin(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builtinNames[name] = struct{}{}
}

func (m *Manager) IsBuiltin(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.builtinNames[name]
	return exists
}

// LoadAndStart loads persisted MCP server configurations and starts enabled servers.
func (m *Manager) LoadAndStart(ctx context.Context) error {
	if m.store == nil {
		return nil
	}

	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("failed to load mcp config: %w", err)
	}

	m.mu.Lock()
	for _, sCfg := range cfg.Servers {
		srv := NewServerRuntimeWithFactory(sCfg, m.transportFactory)
		m.servers[sCfg.ID] = srv
	}
	serversToStart := make([]*ServerRuntime, 0, len(m.servers))
	for _, srv := range m.servers {
		if srv.IsEnabled() {
			serversToStart = append(serversToStart, srv)
		}
	}
	m.mu.Unlock()

	// Start servers concurrently
	var wg sync.WaitGroup
	for _, srv := range serversToStart {
		wg.Add(1)
		go func(s *ServerRuntime) {
			defer wg.Done()
			_ = s.Start(ctx)
		}(srv)
	}
	wg.Wait()

	return nil
}

func (m *Manager) GetServer(id string) (*ServerRuntime, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	srv, exists := m.servers[id]
	return srv, exists
}

func (m *Manager) ListServers() []*ServerRuntime {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make([]*ServerRuntime, 0, len(m.servers))
	for _, s := range m.servers {
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].ID() < res[j].ID()
	})
	return res
}

func (m *Manager) AddServer(ctx context.Context, cfg ServerConfig) error {
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		return fmt.Errorf("server id cannot be empty")
	}
	if strings.Contains(id, "__") {
		return fmt.Errorf("server id cannot contain '__'")
	}
	if m.IsBuiltin(id) {
		return fmt.Errorf("server id conflicts with builtin tool: %s", id)
	}

	m.mu.Lock()
	if _, exists := m.servers[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("server id %q already exists", id)
	}

	srv := NewServerRuntimeWithFactory(cfg, m.transportFactory)
	m.servers[id] = srv
	m.mu.Unlock()

	if cfg.Enabled {
		_ = srv.Start(ctx)
	}

	return m.saveConfig()
}

func (m *Manager) UpdateServer(ctx context.Context, cfg ServerConfig) error {
	id := strings.TrimSpace(cfg.ID)
	m.mu.Lock()
	srv, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q not found", id)
	}
	m.mu.Unlock()

	_ = srv.Stop()

	newSrv := NewServerRuntimeWithFactory(cfg, m.transportFactory)
	m.mu.Lock()
	m.servers[id] = newSrv
	m.mu.Unlock()

	if cfg.Enabled {
		_ = newSrv.Start(ctx)
	}

	return m.saveConfig()
}

func (m *Manager) RemoveServer(id string) error {
	m.mu.Lock()
	srv, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q not found", id)
	}
	delete(m.servers, id)
	m.mu.Unlock()

	_ = srv.Stop()
	return m.saveConfig()
}

func (m *Manager) SetServerEnabled(ctx context.Context, id string, enabled bool) error {
	srv, exists := m.GetServer(id)
	if !exists {
		return fmt.Errorf("server %q not found", id)
	}

	if err := srv.SetEnabled(enabled); err != nil && enabled {
		// Log but persist state so user intent is recorded
	}

	return m.saveConfig()
}

func (m *Manager) SetToolEnabled(serverID, remoteName string, enabled bool) error {
	srv, exists := m.GetServer(serverID)
	if !exists {
		return fmt.Errorf("server %q not found", serverID)
	}

	srv.SetToolEnabled(remoteName, enabled)
	return m.saveConfig()
}

func (m *Manager) SyncServer(ctx context.Context, id string) error {
	srv, exists := m.GetServer(id)
	if !exists {
		return fmt.Errorf("server %q not found", id)
	}
	return srv.SyncCatalog(ctx)
}

// CallTool executes a tool through the corresponding server runtime.
func (m *Manager) CallTool(ctx context.Context, serverID, remoteName string, args string) (string, error) {
	srv, exists := m.GetServer(serverID)
	if !exists {
		return fmt.Sprintf("Tool %q is currently unavailable because MCP server %q is not registered.", remoteName, serverID), nil
	}
	return srv.CallTool(ctx, remoteName, args)
}

// EffectiveTools computes the current list of tools that are eligible to be passed to LLM.
// EffectiveTool = server.enabled && server.status == connected && tool.enabled && tool.exists
func (m *Manager) EffectiveTools() []core.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.Tool
	for _, srv := range m.servers {
		if !srv.IsAvailable() {
			continue
		}
		serverID := srv.ID()
		for _, item := range srv.Catalog().EffectiveItems() {
			fullName := BuildFullName(serverID, item.RemoteName)
			// Guard against overwriting builtins
			if _, isBuiltin := m.builtinNames[fullName]; isBuiltin {
				continue
			}
			result = append(result, core.Tool{
				Name:        fullName,
				Description: item.Description,
				Parameters:  item.Parameters,
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// AllAdapters returns adapters for all tools present in catalog across all servers.
// Having adapters for even disabled/unavailable servers allows the execution phase
// to perform the double-check and return graceful degradation messages instead of "tool not found".
func (m *Manager) AllAdapters() map[string]*ToolAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()

	adapters := make(map[string]*ToolAdapter)
	for _, srv := range m.servers {
		serverID := srv.ID()
		for _, item := range srv.Catalog().List() {
			fullName := BuildFullName(serverID, item.RemoteName)
			if _, isBuiltin := m.builtinNames[fullName]; isBuiltin {
				continue
			}
			adapters[fullName] = NewToolAdapter(serverID, item.RemoteName, item.Description, item.Parameters, m)
		}
	}
	return adapters
}

// LookupAdapter resolves an adapter for a given tool name if it matches an MCP tool.
func (m *Manager) LookupAdapter(fullName string) (*ToolAdapter, bool) {
	serverID, remoteName, ok := ParseFullName(fullName)
	if !ok {
		return nil, false
	}

	srv, exists := m.GetServer(serverID)
	if !exists {
		// Even if server is deleted, return adapter pointing to this manager so double-check gives clear message
		return NewToolAdapter(serverID, remoteName, "", nil, m), true
	}

	item, hasItem := srv.Catalog().Get(remoteName)
	if !hasItem {
		return NewToolAdapter(serverID, remoteName, "", nil, m), true
	}

	return NewToolAdapter(serverID, remoteName, item.Description, item.Parameters, m), true
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, srv := range m.servers {
		_ = srv.Stop()
	}
	return nil
}

func (m *Manager) saveConfig() error {
	if m.store == nil {
		return nil
	}
	m.mu.RLock()
	cfg := Config{
		Servers: make([]ServerConfig, 0, len(m.servers)),
	}
	for _, s := range m.servers {
		cfg.Servers = append(cfg.Servers, s.Config())
	}
	m.mu.RUnlock()

	sort.Slice(cfg.Servers, func(i, j int) bool {
		return cfg.Servers[i].ID < cfg.Servers[j].ID
	})

	return m.store.Save(&cfg)
}
