package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerStatus string

const (
	StatusStopped   ServerStatus = "stopped"
	StatusStarting  ServerStatus = "starting"
	StatusConnected ServerStatus = "connected"
	StatusFailed    ServerStatus = "failed"
)

// ServerRuntime represents a running instance of an MCP server connection.
type ServerRuntime struct {
	mu         sync.RWMutex
	cfg        ServerConfig
	status     ServerStatus
	lastError  string
	generation uint64
	cancel     context.CancelFunc
	retired    bool

	client  *officialmcp.Client
	session *officialmcp.ClientSession
	catalog *ToolCatalog

	// Transport constructor hook (allows injecting mock/in-memory transport for tests)
	transportFactory func(cfg TransportConfig) (officialmcp.Transport, error)
}

type startReservation struct {
	generation      uint64
	lifecycleCtx    context.Context
	cancelLifecycle context.CancelFunc
	transportCfg    TransportConfig
}

func NewServerRuntime(cfg ServerConfig) *ServerRuntime {
	return NewServerRuntimeWithFactory(cfg, nil)
}

func NewServerRuntimeWithFactory(cfg ServerConfig, factory func(cfg TransportConfig) (officialmcp.Transport, error)) *ServerRuntime {
	if cfg.Tools == nil {
		cfg.Tools = make(map[string]ToolPolicy)
	}
	return &ServerRuntime{
		cfg:              cfg,
		status:           StatusStopped,
		catalog:          NewToolCatalog(),
		transportFactory: factory,
	}
}

func (s *ServerRuntime) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.ID
}

func (s *ServerRuntime) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Name
}

func (s *ServerRuntime) Config() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := s.cfg
	copied.Tools = maps.Clone(s.cfg.Tools)
	if copied.Tools == nil {
		copied.Tools = make(map[string]ToolPolicy)
	}
	return copied
}

func (s *ServerRuntime) Status() ServerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *ServerRuntime) LastError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastError
}

func (s *ServerRuntime) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Enabled
}

func (s *ServerRuntime) IsAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.retired && s.cfg.Enabled && s.status == StatusConnected && s.session != nil
}

func (s *ServerRuntime) Catalog() *ToolCatalog {
	return s.catalog
}

func (s *ServerRuntime) IsToolEnabled(remoteName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.retired || !s.cfg.Enabled {
		return false
	}
	item, ok := s.catalog.Get(remoteName)
	if !ok {
		return false
	}
	return item.Enabled
}

// reserveStart claims a generation before any asynchronous startup work is queued.
// Any subsequent Stop/Restart/SetEnabled/Retire advances the generation and makes
// this reservation stale before it can touch a transport or replace a newer session.
func (s *ServerRuntime) reserveStart() (*startReservation, error) {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return nil, errors.New("server runtime has been retired")
	}
	if !s.cfg.Enabled {
		s.status = StatusStopped
		s.mu.Unlock()
		return nil, nil
	}

	s.generation++
	gen := s.generation
	s.status = StatusStarting
	s.lastError = ""

	oldSession := s.session
	s.session = nil
	s.client = nil
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	s.cancel = cancelLifecycle
	transportCfg := s.cfg.Transport
	s.mu.Unlock()

	if oldSession != nil {
		go func(sess *officialmcp.ClientSession) { _ = sess.Close() }(oldSession)
	}

	return &startReservation{
		generation:      gen,
		lifecycleCtx:    lifecycleCtx,
		cancelLifecycle: cancelLifecycle,
		transportCfg:    transportCfg,
	}, nil
}

func (s *ServerRuntime) isStartReservationCurrent(gen uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.retired && s.cfg.Enabled && s.generation == gen
}

// startReservedAsync executes a previously claimed startup reservation in the
// background. The synchronous preflight is intentional: if persistence or another
// caller delayed submission until a newer lifecycle operation already won, no stale
// goroutine is queued at all. runReservedStart still re-checks generation after spawn.
func (s *ServerRuntime) startReservedAsync(reservation *startReservation) {
	if reservation == nil {
		return
	}
	if !s.isStartReservationCurrent(reservation.generation) {
		reservation.cancelLifecycle()
		return
	}

	go func(r *startReservation) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.runReservedStart(ctx, r)
	}(reservation)
}

// StartAsync reserves startup ownership synchronously, then performs the MCP handshake
// in the background. Reserving before spawning prevents queued startup work from
// arriving late and superseding a newer explicit lifecycle operation.
func (s *ServerRuntime) StartAsync() {
	reservation, err := s.reserveStart()
	if err != nil || reservation == nil {
		return
	}
	s.startReservedAsync(reservation)
}

// Retire permanently revokes this runtime's ownership. A retired runtime can
// never be started again, which prevents an AddServer startup goroutine from
// resurrecting a server after RemoveServer/UpdateServer detached it.
func (s *ServerRuntime) Retire() {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return
	}
	s.retired = true
	s.cfg.Enabled = false
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.status = StatusStopped
	session := s.session
	s.session = nil
	s.client = nil
	s.mu.Unlock()

	if session != nil {
		go func() { _ = session.Close() }()
	}
}

// Start connects to the MCP server, initializes protocol, and syncs the tool catalog.
// Uses generation reservations and a decoupled lifecycle context to ensure long-lived
// streaming connections survive short-lived startup/RPC request contexts while stale
// startup attempts cannot supersede newer lifecycle operations.
func (s *ServerRuntime) Start(ctx context.Context) error {
	reservation, err := s.reserveStart()
	if err != nil {
		return err
	}
	if reservation == nil {
		return nil
	}
	return s.runReservedStart(ctx, reservation)
}

func (s *ServerRuntime) runReservedStart(ctx context.Context, reservation *startReservation) error {
	if reservation == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	gen := reservation.generation
	cancelLifecycle := reservation.cancelLifecycle

	// A queued asynchronous start may have been superseded before its goroutine ran.
	// Check before constructing the transport so stale work cannot spawn a stdio process.
	if !s.isStartReservationCurrent(gen) {
		cancelLifecycle()
		return errors.New("server start was aborted or superseded")
	}

	transport, err := s.createTransport(reservation.transportCfg)
	if err != nil {
		cancelLifecycle()
		s.mu.Lock()
		if s.generation == gen && !s.retired {
			s.status = StatusFailed
			s.lastError = err.Error()
		}
		s.mu.Unlock()
		return fmt.Errorf("transport creation failed: %w", err)
	}

	// A test/custom transport factory may itself block. Re-check after it returns
	// and before Connect can start a process or network session.
	if !s.isStartReservationCurrent(gen) {
		cancelLifecycle()
		return errors.New("server start was aborted or superseded")
	}

	client := officialmcp.NewClient(&officialmcp.Implementation{
		Name:    "FrostAgent",
		Version: "0.1.0",
	}, &officialmcp.ClientOptions{
		ToolListChangedHandler: func(changedCtx context.Context, req *officialmcp.ToolListChangedRequest) {
			go func() {
				syncCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_ = s.SyncCatalog(syncCtx)
			}()
		},
	})

	type connResult struct {
		session *officialmcp.ClientSession
		err     error
	}
	connCh := make(chan connResult, 1)
	go func() {
		sess, err := client.Connect(reservation.lifecycleCtx, transport, nil)
		connCh <- connResult{session: sess, err: err}
	}()

	var session *officialmcp.ClientSession
	select {
	case <-ctx.Done():
		cancelLifecycle()
		s.mu.Lock()
		if s.generation == gen && !s.retired {
			s.status = StatusFailed
			s.lastError = fmt.Sprintf("mcp connect timed out: %v", ctx.Err())
		}
		s.mu.Unlock()
		return fmt.Errorf("mcp connect timed out: %w", ctx.Err())
	case res := <-connCh:
		if res.err != nil {
			cancelLifecycle()
			s.mu.Lock()
			if s.generation == gen && !s.retired {
				s.status = StatusFailed
				s.lastError = fmt.Sprintf("mcp connect failed: %v", res.err)
			}
			s.mu.Unlock()
			return fmt.Errorf("mcp connect failed: %w", res.err)
		}
		session = res.session
	}

	// Initial catalog sync (guarded by caller's ctx)
	toolsRes, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close()
		cancelLifecycle()
		s.mu.Lock()
		if s.generation == gen && !s.retired {
			s.status = StatusFailed
			s.lastError = fmt.Sprintf("tools/list failed: %v", err)
		}
		s.mu.Unlock()
		return fmt.Errorf("tools/list failed: %w", err)
	}

	s.mu.Lock()
	// Re-check generation, ownership, and enabled flag; abort if Stop/Retire or
	// a newer Restart superseded this reserved start in the meantime.
	if s.generation != gen || s.retired || !s.cfg.Enabled {
		s.mu.Unlock()
		_ = session.Close()
		cancelLifecycle()
		return errors.New("server start was aborted or superseded")
	}

	policies := maps.Clone(s.cfg.Tools)
	// Publish the catalog while the generation check is still protected by s.mu,
	// so a newer Start cannot connect and then be overwritten by stale catalog data.
	s.catalog.UpdateRemote(toolsRes.Tools, policies)
	s.client = client
	s.session = session
	s.status = StatusConnected
	s.lastError = ""
	s.mu.Unlock()

	// Launch background watcher to detect server process termination or connection drop
	go func(g uint64, sess *officialmcp.ClientSession) {
		waitErr := sess.Wait()
		s.handleTermination(g, sess, waitErr)
	}(gen, session)

	return nil
}

func (s *ServerRuntime) handleTermination(gen uint64, sess *officialmcp.ClientSession, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.retired && s.generation == gen && s.session == sess {
		s.status = StatusFailed
		if err != nil && !errors.Is(err, context.Canceled) {
			s.lastError = fmt.Sprintf("server process or connection terminated: %v", err)
		} else {
			s.lastError = "server connection closed unexpectedly"
		}
		s.session = nil
	}
}

func (s *ServerRuntime) Stop() error {
	s.mu.Lock()
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.status = StatusStopped
	session := s.session
	s.session = nil
	s.client = nil
	s.mu.Unlock()

	if session != nil {
		return session.Close()
	}
	return nil
}

func (s *ServerRuntime) Restart(ctx context.Context) error {
	_ = s.Stop()
	return s.Start(ctx)
}

func (s *ServerRuntime) SyncCatalog(ctx context.Context) error {
	s.mu.RLock()
	status := s.status
	session := s.session
	enabled := s.cfg.Enabled
	retired := s.retired
	policies := maps.Clone(s.cfg.Tools)
	s.mu.RUnlock()

	if retired {
		return errors.New("cannot sync catalog: server runtime has been retired")
	}
	if !enabled {
		return errors.New("cannot sync catalog: server is disabled")
	}

	// If server failed or disconnected, trigger true reconnect/restart
	if status != StatusConnected || session == nil {
		return s.Restart(ctx)
	}

	toolsRes, err := session.ListTools(ctx, nil)
	if err != nil {
		s.mu.Lock()
		s.lastError = fmt.Sprintf("sync catalog failed: %v", err)
		s.mu.Unlock()
		return err
	}

	s.catalog.UpdateRemote(toolsRes.Tools, policies)
	return nil
}

func (s *ServerRuntime) SetEnabled(ctx context.Context, enabled bool) error {
	return s.SetEnabledForRuntime(ctx, enabled, true)
}

// SetEnabledForRuntime updates the desired persisted state while allowing an
// inactive owning instance to keep the connection stopped.
func (s *ServerRuntime) SetEnabledForRuntime(ctx context.Context, enabled, runtimeActive bool) error {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return errors.New("server runtime has been retired")
	}
	if s.cfg.Enabled == enabled {
		if !enabled && s.status == StatusStopped {
			s.mu.Unlock()
			return nil
		}
		if enabled && runtimeActive && (s.status == StatusStarting || s.status == StatusConnected) {
			s.mu.Unlock()
			return nil
		}
	}
	s.cfg.Enabled = enabled
	s.mu.Unlock()

	if enabled && runtimeActive {
		if ctx == nil {
			ctx = context.Background()
		}
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
		}
		return s.Start(ctx)
	}
	return s.Stop()
}

func (s *ServerRuntime) SetToolEnabled(remoteName string, enabled bool) {
	s.mu.Lock()
	if s.retired {
		s.mu.Unlock()
		return
	}
	if s.cfg.Tools == nil {
		s.cfg.Tools = make(map[string]ToolPolicy)
	}
	s.cfg.Tools[remoteName] = ToolPolicy{Enabled: enabled}
	s.mu.Unlock()

	s.catalog.SetPolicy(remoteName, enabled)
}

func (s *ServerRuntime) CallTool(ctx context.Context, remoteName string, args string) (string, error) {
	s.mu.RLock()
	enabled := s.cfg.Enabled
	serverID := s.cfg.ID
	status := s.status
	session := s.session
	retired := s.retired
	s.mu.RUnlock()

	// Double-check 1: server enabled and still owned.
	if retired || !enabled {
		return fmt.Sprintf("Tool %q is currently disabled because MCP server %q has been disabled.", remoteName, serverID), nil
	}

	// Double-check 2: tool enabled in catalog
	item, exists := s.catalog.Get(remoteName)
	if !exists {
		return fmt.Sprintf("Tool %q was not found on MCP server %q.", remoteName, serverID), nil
	}
	if !item.Enabled {
		return fmt.Sprintf("Tool %q is currently disabled.", remoteName), nil
	}

	// Double-check 3: server available
	if status != StatusConnected || session == nil {
		return fmt.Sprintf("Tool %q is temporarily unavailable because MCP server %q is not connected.", remoteName, serverID), nil
	}

	// Parse arguments string into map
	var argMap map[string]any
	if args != "" && args != "{}" {
		if err := json.Unmarshal([]byte(args), &argMap); err != nil {
			return fmt.Sprintf("FrostAgent错误：工具参数 JSON 解析失败: %v", err), nil
		}
	}
	if argMap == nil {
		argMap = map[string]any{}
	}

	callParams := &officialmcp.CallToolParams{
		Name:      remoteName,
		Arguments: argMap,
	}

	result, err := session.CallTool(ctx, callParams)
	if err != nil {
		return fmt.Sprintf("Tool %q execution error on server %q: %v", remoteName, serverID, err), nil
	}

	return FormatToolResult(result), nil
}

func (s *ServerRuntime) createTransport(cfg TransportConfig) (officialmcp.Transport, error) {
	if s.transportFactory != nil {
		return s.transportFactory(cfg)
	}
	return CreateTransport(cfg)
}
