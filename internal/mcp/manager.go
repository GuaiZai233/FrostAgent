package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"FrostAgent/internal/core"
	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrManagerClosed = errors.New("MCP manager is closed")

// Manager manages all MCP server runtimes and tool catalogs for an instance.
type Manager struct {
	mu           sync.RWMutex
	persistMu    sync.Mutex
	runtimeMu    sync.Mutex
	servers      map[string]*ServerRuntime
	store        *ConfigStore
	builtinNames map[string]struct{}

	// Bidirectional mapping for exposed sanitized tool names
	exposedToTarget map[string]ToolTarget
	targetToExposed map[ToolTarget]string
	closed          bool
	active          bool

	// factory for testing
	transportFactory func(cfg TransportConfig) (officialmcp.Transport, error)
}

func NewManager(store *ConfigStore, builtinNames []string) *Manager {
	return newManager(store, builtinNames, nil, true)
}

func NewManagerWithFactory(store *ConfigStore, builtinNames []string, factory func(cfg TransportConfig) (officialmcp.Transport, error)) *Manager {
	return newManager(store, builtinNames, factory, true)
}

// NewInactiveManager creates a manager whose persisted configuration remains
// editable while all live MCP connections stay stopped. SetRuntimeActive(true)
// activates configured servers when the owning instance starts.
func NewInactiveManager(store *ConfigStore, builtinNames []string) *Manager {
	return newManager(store, builtinNames, nil, false)
}

func newManager(store *ConfigStore, builtinNames []string, factory func(cfg TransportConfig) (officialmcp.Transport, error), active bool) *Manager {
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
		active:           active,
	}
}

func (m *Manager) RegisterBuiltin(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
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
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return ErrManagerClosed
	}
	if m.store == nil {
		return nil
	}

	cfg, err := m.store.Load()
	if err != nil {
		return fmt.Errorf("failed to load mcp config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrManagerClosed
	}
	m.servers = make(map[string]*ServerRuntime, len(cfg.Servers))
	for _, sCfg := range cfg.Servers {
		srv := NewServerRuntimeWithFactory(sCfg, m.transportFactory)
		m.servers[sCfg.ID] = srv
	}
	return nil
}

// StartAll connects to all enabled servers concurrently in the background.
func (m *Manager) StartAll(ctx context.Context) {
	m.runtimeMu.Lock()
	m.mu.Lock()
	if m.closed || !m.active {
		m.mu.Unlock()
		m.runtimeMu.Unlock()
		return
	}
	type reservedStart struct {
		server      *ServerRuntime
		reservation *startReservation
	}
	starts := make([]reservedStart, 0, len(m.servers))
	for _, srv := range m.servers {
		if srv.IsEnabled() {
			reservation, err := srv.reserveStart()
			if err == nil && reservation != nil {
				starts = append(starts, reservedStart{server: srv, reservation: reservation})
			}
		}
	}
	m.mu.Unlock()
	m.runtimeMu.Unlock()

	var wg sync.WaitGroup
	for _, start := range starts {
		wg.Add(1)
		go func(item reservedStart) {
			defer wg.Done()
			_ = item.server.runReservedStart(ctx, item.reservation)
		}(start)
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.validateServerConfig(cfg); err != nil {
		return err
	}

	id := strings.TrimSpace(cfg.ID)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	if _, exists := m.servers[id]; exists {
		m.mu.Unlock()
		return fmt.Errorf("server id %q already exists", id)
	}

	srv := NewServerRuntimeWithFactory(cfg, m.transportFactory)

	// Reserve the initial startup generation before the runtime becomes visible to
	// other control-plane operations. If persistence blocks and a newer Restart,
	// Stop/disable, Update, or Remove happens meanwhile, that operation advances the
	// generation and makes this older Add reservation stale before it can start work.
	var initialStart *startReservation
	if cfg.Enabled && m.active {
		var err error
		initialStart, err = srv.reserveStart()
		if err != nil {
			m.mu.Unlock()
			return err
		}
	}

	m.servers[id] = srv
	m.mu.Unlock()

	// Persist the configuration before attempting to connect. Connection failure is
	// runtime state (status=failed/lastError), not a failed configuration create.
	if err := m.saveConfig(); err != nil {
		// Persistence failure is a true AddServer failure. Retire the runtime before
		// removing ownership so the reserved start is cancelled and no concurrent
		// lifecycle operation can leave a detached process.
		m.mu.Lock()
		if current, exists := m.servers[id]; exists && current == srv {
			srv.Retire()
			delete(m.servers, id)
		}
		m.mu.Unlock()
		return err
	}

	// Initial connection is runtime work, not part of the create transaction. Execute
	// only the reservation created before publication; if a newer lifecycle operation
	// occurred while persistence was in flight, startReservedAsync rejects it as stale.
	if initialStart != nil {
		srv.startReservedAsync(initialStart)
	}
	return nil
}

func (m *Manager) UpdateServer(ctx context.Context, cfg ServerConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.validateServerConfig(cfg); err != nil {
		return err
	}

	id := strings.TrimSpace(cfg.ID)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q not found", id)
	}

	// Revoke ownership before replacement so an older asynchronous Add start can
	// never resurrect this runtime after it has been superseded.
	srv.Retire()
	newSrv := NewServerRuntimeWithFactory(cfg, m.transportFactory)
	m.servers[id] = newSrv
	var replacementStart *startReservation
	if cfg.Enabled && m.active {
		var reserveErr error
		replacementStart, reserveErr = newSrv.reserveStart()
		if reserveErr != nil {
			m.mu.Unlock()
			return reserveErr
		}
	}
	m.mu.Unlock()

	var startErr error
	if replacementStart != nil {
		startErr = newSrv.runReservedStart(ctx, replacementStart)
	}

	if err := m.saveConfig(); err != nil {
		return err
	}
	return startErr
}

func (m *Manager) RemoveServer(id string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q not found", id)
	}

	// Retire while Manager still owns the runtime. This makes ownership revocation
	// atomic with respect to any pending asynchronous Start call.
	srv.Retire()
	delete(m.servers, id)
	// Clean up stable tool name mappings associated with this server
	for exposed, target := range m.exposedToTarget {
		if target.ServerID == id {
			delete(m.exposedToTarget, exposed)
			delete(m.targetToExposed, target)
		}
	}
	m.mu.Unlock()

	return m.saveConfig()
}

func (m *Manager) SetServerEnabled(ctx context.Context, id string, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf("server %q not found", id)
	}
	startErr := srv.SetEnabledForRuntime(ctx, enabled, m.active)
	m.mu.RUnlock()
	if err := m.saveConfig(); err != nil {
		return err
	}
	if startErr != nil {
		return startErr
	}

	return nil
}

func (m *Manager) SetToolEnabled(serverID, remoteName string, enabled bool) error {
	srv, _, err := m.serverForMutation(serverID)
	if err != nil {
		return err
	}

	srv.SetToolEnabled(remoteName, enabled)
	return m.saveConfig()
}

func (m *Manager) SyncServer(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		return fmt.Errorf("server %q not found", id)
	}
	if !m.active {
		return errors.New("MCP runtime is inactive while the instance is stopped")
	}
	return srv.SyncCatalog(ctx)
}

func (m *Manager) RestartServer(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		return fmt.Errorf("server %q not found", id)
	}
	if !m.active {
		return errors.New("MCP runtime is inactive while the instance is stopped")
	}
	return srv.Restart(ctx)
}

func (m *Manager) serverForMutation(id string) (*ServerRuntime, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, false, ErrManagerClosed
	}
	srv, exists := m.servers[id]
	if !exists {
		return nil, false, fmt.Errorf("server %q not found", id)
	}
	return srv, m.active, nil
}

// SetRuntimeActive follows the lifecycle of the owning FrostAgent instance.
// Disabling an instance synchronously stops every live connection while keeping
// the desired enabled flags and persisted configuration intact.
func (m *Manager) SetRuntimeActive(active bool) error {
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrManagerClosed
	}
	if m.active == active {
		m.mu.Unlock()
		return nil
	}
	if active {
		m.active = true
		starts := make([]struct {
			server      *ServerRuntime
			reservation *startReservation
		}, 0, len(m.servers))
		for _, srv := range m.servers {
			if srv.IsEnabled() {
				reservation, err := srv.reserveStart()
				if err == nil && reservation != nil {
					starts = append(starts, struct {
						server      *ServerRuntime
						reservation *startReservation
					}{server: srv, reservation: reservation})
				}
			}
		}
		m.mu.Unlock()
		for _, start := range starts {
			start.server.startReservedAsync(start.reservation)
		}
		return nil
	}

	m.active = false
	servers := make([]*ServerRuntime, 0, len(m.servers))
	for _, srv := range m.servers {
		servers = append(servers, srv)
	}
	m.mu.Unlock()
	var stopErr error
	for _, srv := range servers {
		stopErr = errors.Join(stopErr, srv.Stop())
	}
	return stopErr
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
	m.runtimeMu.Lock()
	defer m.runtimeMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.active = false

	// Closing a Manager permanently revokes ownership of every runtime. Retire,
	// rather than Stop, so a pending StartAsync goroutine cannot restart after shutdown.
	for _, srv := range m.servers {
		srv.Retire()
	}
	m.mu.Unlock()

	// Wait until any save admitted before closure has either completed or
	// observed the closed flag. After this point no configuration write remains.
	m.persistMu.Lock()
	m.persistMu.Unlock()
	return nil
}

func (m *Manager) saveConfig() error {
	// Serialize snapshot creation together with the durable write. ConfigStore already
	// serializes file replacement, but without this Manager-level boundary an older
	// snapshot can be written after a newer mutation and resurrect deleted config.
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return ErrManagerClosed
	}
	if m.store == nil {
		m.mu.RUnlock()
		return nil
	}
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
