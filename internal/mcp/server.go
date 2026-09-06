package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"
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
	mu        sync.RWMutex
	cfg       ServerConfig
	status    ServerStatus
	lastError string
	client    *Client
	catalog   *ToolCatalog

	// Transport constructor hook (allows injecting mock transport for tests)
	transportFactory func(cfg TransportConfig) (Transport, error)
}

func NewServerRuntime(cfg ServerConfig) *ServerRuntime {
	return NewServerRuntimeWithFactory(cfg, nil)
}

func NewServerRuntimeWithFactory(cfg ServerConfig, factory func(cfg TransportConfig) (Transport, error)) *ServerRuntime {
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
	copied.Tools = make(map[string]ToolPolicy, len(s.cfg.Tools))
	maps.Copy(copied.Tools, s.cfg.Tools)
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
	return s.cfg.Enabled && s.status == StatusConnected && s.client != nil
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
func (s *ServerRuntime) Start(ctx context.Context) error {
	s.mu.Lock()
	if !s.cfg.Enabled {
		s.status = StatusStopped
		s.mu.Unlock()
		return nil
	}
	s.status = StatusStarting
	s.lastError = ""
	s.mu.Unlock()

	transport, err := s.createTransport(s.cfg.Transport)
	if err != nil {
		s.mu.Lock()
		s.status = StatusFailed
		s.lastError = err.Error()
		s.mu.Unlock()
		return fmt.Errorf("transport creation failed: %w", err)
	}

	client := NewClient(transport)

	// Initialize handshake with timeout
	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err = client.Initialize(initCtx, ClientInfo{
		Name:    "FrostAgent",
		Version: "0.1.0",
	})
	if err != nil {
		_ = client.Close()
		s.mu.Lock()
		s.status = StatusFailed
		s.lastError = fmt.Sprintf("handshake failed: %v", err)
		s.mu.Unlock()
		return fmt.Errorf("mcp handshake failed: %w", err)
	}

	// Sync tool catalog
	listCtx, listCancel := context.WithTimeout(ctx, 15*time.Second)
	defer listCancel()

	tools, err := client.ListTools(listCtx)
	if err != nil {
		_ = client.Close()
		s.mu.Lock()
		s.status = StatusFailed
		s.lastError = fmt.Sprintf("tools/list failed: %v", err)
		s.mu.Unlock()
		return fmt.Errorf("tools/list failed: %w", err)
	}

	s.mu.Lock()
	s.client = client
	s.status = StatusConnected
	s.lastError = ""
	policies := s.cfg.Tools
	s.mu.Unlock()

	s.catalog.UpdateRemote(tools, policies)
	return nil
}

func (s *ServerRuntime) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.status = StatusStopped
	if s.client != nil {
		err := s.client.Close()
		s.client = nil
		return err
	}
	return nil
}

func (s *ServerRuntime) SyncCatalog(ctx context.Context) error {
	s.mu.RLock()
	client := s.client
	policies := s.cfg.Tools
	enabled := s.cfg.Enabled
	s.mu.RUnlock()

	if !enabled || client == nil {
		return errors.New("cannot sync catalog: server is not connected")
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		s.mu.Lock()
		s.lastError = fmt.Sprintf("sync catalog failed: %v", err)
		s.mu.Unlock()
		return err
	}

	s.catalog.UpdateRemote(tools, policies)
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
	client := s.client
	s.mu.RUnlock()

	// Double-check 1: server enabled
	if !enabled {
		return fmt.Sprintf("Tool %q is currently disabled because MCP server %q has been disabled.", remoteName, serverID), nil
	}

	// Double-check 2: tool enabled
	item, exists := s.catalog.Get(remoteName)
	if !exists {
		return fmt.Sprintf("Tool %q was not found on MCP server %q.", remoteName, serverID), nil
	}
	if !item.Enabled {
		return fmt.Sprintf("Tool %q is currently disabled.", remoteName), nil
	}

	// Double-check 3: server available
	if status != StatusConnected || client == nil {
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

	result, err := client.CallTool(ctx, remoteName, argMap)
	if err != nil {
		return fmt.Sprintf("Tool %q execution error on server %q: %v", remoteName, serverID, err), nil
	}

	return FormatToolResult(result), nil
}

func (s *ServerRuntime) createTransport(cfg TransportConfig) (Transport, error) {
	if s.transportFactory != nil {
		return s.transportFactory(cfg)
	}

	switch cfg.Type {
	case TransportStdio:
		if cfg.Command == "" {
			return nil, errors.New("stdio command cannot be empty")
		}
		return NewStdioTransport(cfg.Command, cfg.Args, cfg.Env, cfg.WorkingDir)
	case TransportStreamableHTTP:
		if cfg.URL == "" {
			return nil, errors.New("streamable_http URL cannot be empty")
		}
		return NewStreamableHTTPTransport(cfg.URL, cfg.Headers)
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", cfg.Type)
	}
}
