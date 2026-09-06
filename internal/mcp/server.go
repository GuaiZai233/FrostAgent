package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"

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

	client  *officialmcp.Client
	session *officialmcp.ClientSession
	catalog *ToolCatalog

	// Transport constructor hook (allows injecting mock/in-memory transport for tests)
	transportFactory func(cfg TransportConfig) (officialmcp.Transport, error)
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
	return s.cfg.Enabled && s.status == StatusConnected && s.session != nil
}

func (s *ServerRuntime) Catalog() *ToolCatalog {
	return s.catalog
}

func (s *ServerRuntime) IsToolEnabled(remoteName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.cfg.Enabled {
		return false
	}
	item, ok := s.catalog.Get(remoteName)
	if !ok {
		return false
	}
	return item.Enabled
}

// Start connects to the MCP server, initializes protocol, and syncs the tool catalog.
// Uses generation tokens and cancellable context to prevent Start/Stop race conditions.
func (s *ServerRuntime) Start(ctx context.Context) error {
	s.mu.Lock()
	if !s.cfg.Enabled {
		s.status = StatusStopped
		s.mu.Unlock()
		return nil
	}
	s.generation++
	gen := s.generation
	s.status = StatusStarting
	s.lastError = ""
	if s.cancel != nil {
		s.cancel()
	}
	startCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	transportCfg := s.cfg.Transport
	s.mu.Unlock()

	transport, err := s.createTransport(transportCfg)
	if err != nil {
		s.mu.Lock()
		if s.generation == gen {
			s.status = StatusFailed
			s.lastError = err.Error()
		}
		s.mu.Unlock()
		return fmt.Errorf("transport creation failed: %w", err)
	}

	client := officialmcp.NewClient(&officialmcp.Implementation{
		Name:    "FrostAgent",
		Version: "0.1.0",
	}, nil)

	session, err := client.Connect(startCtx, transport, nil)
	if err != nil {
		s.mu.Lock()
		if s.generation == gen {
			s.status = StatusFailed
			s.lastError = fmt.Sprintf("mcp connect failed: %v", err)
		}
		s.mu.Unlock()
		return fmt.Errorf("mcp connect failed: %w", err)
	}

	// Initial catalog sync
	toolsRes, err := session.ListTools(startCtx, nil)
	if err != nil {
		_ = session.Close()
		s.mu.Lock()
		if s.generation == gen {
			s.status = StatusFailed
			s.lastError = fmt.Sprintf("tools/list failed: %v", err)
		}
		s.mu.Unlock()
		return fmt.Errorf("tools/list failed: %w", err)
	}

	s.mu.Lock()
	// Re-check generation and enabled flag; abort if Stop() occurred in the meantime
	if s.generation != gen || !s.cfg.Enabled {
		s.mu.Unlock()
		_ = session.Close()
		return errors.New("server start was aborted or superseded")
	}

	s.client = client
	s.session = session
	s.status = StatusConnected
	s.lastError = ""
	policies := maps.Clone(s.cfg.Tools)
	s.mu.Unlock()

	s.catalog.UpdateRemote(toolsRes.Tools, policies)

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

	if s.generation == gen && s.session == sess {
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
	policies := maps.Clone(s.cfg.Tools)
	s.mu.RUnlock()

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

func (s *ServerRuntime) SetEnabled(enabled bool) error {
	s.mu.Lock()
	s.cfg.Enabled = enabled
	s.mu.Unlock()

	if enabled {
		return s.Start(context.Background())
	}
	return s.Stop()
}

func (s *ServerRuntime) SetToolEnabled(remoteName string, enabled bool) {
	s.mu.Lock()
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
	s.mu.RUnlock()

	// Double-check 1: server enabled
	if !enabled {
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
