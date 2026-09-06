package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"FrostAgent/internal/core"
	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Manager manages all MCP server runtimes and tool catalogs for an instance.
type Manager struct {
	mu           sync.RWMutex
	servers      map[string]*ServerRuntime
	store        *ConfigStore
	builtinNames map[string]struct{}

	// Bidirectional mapping for exposed sanitized tool names
	exposedToTarget map[string]ToolTarget
	targetToExposed map[ToolTarget]string

	// factory for testing
	transportFactory func(cfg TransportConfig) (officialmcp.Transport, error)
}

func NewManager(store *ConfigStore, builtinNames []string) *Manager {
	return NewManagerWithFactory(store, builtinNames, nil)
}

func NewManagerWithFactory(store *ConfigStore, builtinNames []string, factory func(cfg TransportConfig) (officialmcp.Transport, error)) *Manager {
	builtins := make(map[string]struct{}, len(builtinNames))
	for _, name := range builtinNames {
		builtins[name] = struct{}{}
	}
	return &Manager{
		servers:          make(map[string]*ServerRuntime),
		store:            store,
		builtinNames:     builtins,
		exposedToTarget:  make(map[string]ToolTarget),
		targetToExposed:  make(map[ToolTarget]string),
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

// Load reads persisted MCP server configurations synchronously into memory without starting connections.
// This eliminates initialization race conditions with early HTTP/RPC API requests.
func (m *Manager) Load() error {
	if m.store == nil {
		return nil
	}

	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("failed to load mcp config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.servers = make(map[string]*ServerRuntime, len(cfg.Servers))
	for _, sCfg := range cfg.Servers {
		srv := NewServerRuntimeWithFactory(sCfg, m.transportFactory)
		m.servers[sCfg.ID] = srv
	}
	return nil
}

// StartAll connects to all enabled servers concurrently in the background.
func (m *Manager) StartAll(ctx context.Context) {
	m.mu.RLock()
	serversToStart := make([]*ServerRuntime, 0, len(m.servers))
	for _, srv := range m.servers {
		if srv.IsEnabled() {
			serversToStart = append(serversToStart, srv)
		}
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, srv := range serversToStart {
		wg.Add(1)
		go func(s *ServerRuntime) {
			defer wg.Done()
			_ = s.Start(ctx)
		}(srv)
	}
	wg.Wait()
}

// LoadAndStart loads persisted MCP server configurations and starts enabled servers.
func (m *Manager) LoadAndStart(ctx context.Context) error {
	if err := m.Load(); err != nil {
		return err
	}
	m.StartAll(ctx)
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

func (m *Manager) validateServerConfig(cfg ServerConfig) error {
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

	switch cfg.Transport.Type {
	case TransportStdio:
		if strings.TrimSpace(cfg.Transport.Command) == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
	case TransportStreamableHTTP, TransportSSE:
		u := strings.TrimSpace(cfg.Transport.URL)
		if u == "" {
			return fmt.Errorf("url is required for %s transport", cfg.Transport.Type)
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("url must start with http:// or https://")
		}
	default:
		return fmt.Errorf("unsupported transport type %q (allowed: stdio, streamable_http, sse)", cfg.Transport.Type)
	}
	return nil
}

func (m *Manager) AddServer(ctx context.Context, cfg ServerConfig) error {
	if err := m.validateServerConfig(cfg); err != nil {
		return err
	}

	id := strings.TrimSpace(cfg.ID)
	m.mu.Lock()
	if _, exists := m.servers[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("server id %q already exists", id)
	}

	srv := NewServerRuntimeWithFactory(cfg, m.transportFactory)
	m.servers[id] = srv
	m.mu.Unlock()

	var startErr error
	if cfg.Enabled {
		startErr = srv.Start(ctx)
	}

	if err := m.saveConfig(); err != nil {
		return err
	}
	return startErr
}

func (m *Manager) UpdateServer(ctx context.Context, cfg ServerConfig) error {
	if err := m.validateServerConfig(cfg); err != nil {
		return err
	}

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

	var startErr error
	if cfg.Enabled {
		startErr = newSrv.Start(ctx)
	}

	if err := m.saveConfig(); err != nil {
		return err
	}
	return startErr
}

func (m *Manager) RemoveServer(id string) error {
	m.mu.Lock()
	srv, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q not found", id)
	}
	delete(m.servers, id)
	// Clean up stable tool name mappings associated with this server
	for exposed, target := range m.exposedToTarget {
		if target.ServerID == id {
			delete(m.exposedToTarget, exposed)
			delete(m.targetToExposed, target)
		}
	}
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
		// Logged or handled, persist state so user intent is retained
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

func (m *Manager) RestartServer(ctx context.Context, id string) error {
	srv, exists := m.GetServer(id)
	if !exists {
		return fmt.Errorf("server %q not found", id)
	}
	return srv.Restart(ctx)
}

// CallTool executes a tool through the corresponding server runtime.
func (m *Manager) CallTool(ctx context.Context, serverID, remoteName string, args string) (string, error) {
	srv, exists := m.GetServer(serverID)
	if !exists {
		return fmt.Sprintf("Tool %q is currently unavailable because MCP server %q is not registered.", remoteName, serverID), nil
	}
	return srv.CallTool(ctx, remoteName, args)
}

// updateToolMappingsLocked ensures bidirectional mappings between exposed sanitized names
// and internal (serverID, remoteName) targets are stably registered for all catalog items across all servers.
func (m *Manager) updateToolMappingsLocked() {
	if m.exposedToTarget == nil {
		m.exposedToTarget = make(map[string]ToolTarget)
	}
	if m.targetToExposed == nil {
		m.targetToExposed = make(map[ToolTarget]string)
	}

	for _, srv := range m.servers {
		serverID := srv.ID()
		for _, item := range srv.Catalog().List() {
			target := ToolTarget{ServerID: serverID, RemoteName: item.RemoteName}
			if _, exists := m.targetToExposed[target]; !exists {
				fullName := SanitizeExposedToolName(serverID, item.RemoteName)
				if _, isBuiltin := m.builtinNames[fullName]; isBuiltin {
					continue
				}
				m.exposedToTarget[fullName] = target
				m.targetToExposed[target] = fullName
			}
		}
	}
}

// EffectiveTools computes the current list of tools that are eligible to be passed to LLM.
// EffectiveTool = server.enabled && server.status == connected && tool.enabled && tool.exists
func (m *Manager) EffectiveTools() []core.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Update mappings across all registered servers' catalogs without wiping previously learned identities.
	m.updateToolMappingsLocked()

	var result []core.Tool
	for _, srv := range m.servers {
		if !srv.IsAvailable() {
			continue
		}
		serverID := srv.ID()
		for _, item := range srv.Catalog().EffectiveItems() {
			target := ToolTarget{ServerID: serverID, RemoteName: item.RemoteName}
			fullName, ok := m.targetToExposed[target]
			if !ok {
				fullName = SanitizeExposedToolName(serverID, item.RemoteName)
				if _, isBuiltin := m.builtinNames[fullName]; isBuiltin {
					continue
				}
				m.exposedToTarget[fullName] = target
				m.targetToExposed[target] = fullName
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
func (m *Manager) AllAdapters() map[string]*ToolAdapter {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateToolMappingsLocked()

	adapters := make(map[string]*ToolAdapter)
	for _, srv := range m.servers {
		serverID := srv.ID()
		for _, item := range srv.Catalog().List() {
			target := ToolTarget{ServerID: serverID, RemoteName: item.RemoteName}
			fullName, ok := m.targetToExposed[target]
			if !ok {
				fullName = SanitizeExposedToolName(serverID, item.RemoteName)
				if _, isBuiltin := m.builtinNames[fullName]; isBuiltin {
					continue
				}
				m.exposedToTarget[fullName] = target
				m.targetToExposed[target] = fullName
			}

			adapters[fullName] = NewToolAdapterWithFullName(serverID, item.RemoteName, fullName, item.Description, item.Parameters, m)
		}
	}
	return adapters
}

// LookupAdapter resolves an adapter for a given tool name if it matches an MCP tool.
func (m *Manager) LookupAdapter(fullName string) (*ToolAdapter, bool) {
	m.mu.RLock()
	target, found := m.exposedToTarget[fullName]
	m.mu.RUnlock()

	serverID := target.ServerID
	remoteName := target.RemoteName

	if !found {
		// Fallback to legacy or canonical name parsing
		var ok bool
		serverID, remoteName, ok = ParseFullName(fullName)
		if !ok {
			return nil, false
		}
	}

	srv, exists := m.GetServer(serverID)
	if !exists {
		return NewToolAdapterWithFullName(serverID, remoteName, fullName, "", nil, m), true
	}

	item, hasItem := srv.Catalog().Get(remoteName)
	if !hasItem {
		return NewToolAdapterWithFullName(serverID, remoteName, fullName, "", nil, m), true
	}

	return NewToolAdapterWithFullName(serverID, remoteName, fullName, item.Description, item.Parameters, m), true
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
