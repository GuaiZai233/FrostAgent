package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"strings"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/mcp"
)

// MaskedSecret is the replacement placeholder returned for sensitive keys.
const MaskedSecret = "******"

var sensitiveKeyWords = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "AUTH", "CREDENTIAL", "PRIVATE",
}

func isSensitiveKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, w := range sensitiveKeyWords {
		if strings.Contains(upper, w) {
			return true
		}
	}
	return false
}

func isMaskedSecret(val string) bool {
	return val == MaskedSecret || (strings.Contains(val, "***") && len(val) <= 12)
}

func maskEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	masked := make(map[string]string, len(env))
	for k, v := range env {
		if isSensitiveKey(k) && v != "" {
			masked[k] = MaskedSecret
		} else {
			masked[k] = v
		}
	}
	return masked
}

func maskHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	masked := make(map[string]string, len(headers))
	for k, v := range headers {
		if (isSensitiveKey(k) || strings.EqualFold(k, "Authorization")) && v != "" {
			masked[k] = MaskedSecret
		} else {
			masked[k] = v
		}
	}
	return masked
}

func isLoopbackAddr(addr string) bool {
	if addr == "" {
		return true // In-process or test invocation
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func checkControlPlaneAuth(peerAddr string, authHeader string, isStdioMutation bool) error {
	token := strings.TrimSpace(os.Getenv("MCP_CONTROL_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	}

	if token != "" {
		expected := "Bearer " + token
		if authHeader != expected {
			return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing MCP control plane token"))
		}
		return nil
	}

	// When no explicit auth token is configured, enforce loopback boundary for stdio command execution
	// to prevent unauthenticated remote clients from achieving RCE.
	if isStdioMutation {
		if os.Getenv("ALLOW_REMOTE_MCP_MANAGEMENT") == "true" {
			return nil
		}
		if !isLoopbackAddr(peerAddr) {
			return connect.NewError(connect.CodePermissionDenied, errors.New("managing stdio MCP servers is restricted to localhost or requires MCP_CONTROL_TOKEN authorization"))
		}
	}
	return nil
}

// Service implements frostagent.v1.MCPServiceHandler.
type Service struct {
	manager *mcp.Manager
}

var _ frostagentv1connect.MCPServiceHandler = (*Service)(nil)

func New(manager *mcp.Manager) *Service {
	return &Service{manager: manager}
}

func (s *Service) ListMCPServers(
	ctx context.Context,
	req *connect.Request[v1.ListMCPServersRequest],
) (*connect.Response[v1.ListMCPServersResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.ListMCPServersResponse{Servers: []*v1.MCPServerInfo{}}), nil
	}

	servers := s.manager.ListServers()
	respServers := make([]*v1.MCPServerInfo, 0, len(servers))
	for _, srv := range servers {
		respServers = append(respServers, s.buildServerInfo(srv))
	}

	return connect.NewResponse(&v1.ListMCPServersResponse{Servers: respServers}), nil
}

func (s *Service) GetMCPServer(
	ctx context.Context,
	req *connect.Request[v1.GetMCPServerRequest],
) (*connect.Response[v1.GetMCPServerResponse], error) {
	if s.manager == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("mcp manager not initialized"))
	}

	srv, exists := s.manager.GetServer(req.Msg.Id)
	if !exists {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("server %q not found", req.Msg.Id))
	}

	return connect.NewResponse(&v1.GetMCPServerResponse{
		Server: s.buildServerInfo(srv),
	}), nil
}

func (s *Service) AddMCPServer(
	ctx context.Context,
	req *connect.Request[v1.AddMCPServerRequest],
) (*connect.Response[v1.AddMCPServerResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.AddMCPServerResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	isStdio := req.Msg.TransportType == string(mcp.TransportStdio)
	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), isStdio); err != nil {
		return nil, err
	}

	cfg := mcp.ServerConfig{
		ID:      req.Msg.Id,
		Name:    req.Msg.Name,
		Enabled: req.Msg.Enabled,
		Transport: mcp.TransportConfig{
			Type:       mcp.TransportType(req.Msg.TransportType),
			Command:    req.Msg.Command,
			Args:       req.Msg.Args,
			Env:        req.Msg.Env,
			WorkingDir: req.Msg.WorkingDir,
			URL:        req.Msg.Url,
			Headers:    req.Msg.Headers,
		},
		Tools: make(map[string]mcp.ToolPolicy),
	}

	if err := s.manager.AddServer(ctx, cfg); err != nil {
		return connect.NewResponse(&v1.AddMCPServerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.AddMCPServerResponse{Success: true}), nil
}

func (s *Service) UpdateMCPServer(
	ctx context.Context,
	req *connect.Request[v1.UpdateMCPServerRequest],
) (*connect.Response[v1.UpdateMCPServerResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.UpdateMCPServerResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	isStdio := req.Msg.TransportType == string(mcp.TransportStdio)
	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), isStdio); err != nil {
		return nil, err
	}

	envCopy := maps.Clone(req.Msg.Env)
	headersCopy := maps.Clone(req.Msg.Headers)

	cfg := mcp.ServerConfig{
		ID:      req.Msg.Id,
		Name:    req.Msg.Name,
		Enabled: req.Msg.Enabled,
		Transport: mcp.TransportConfig{
			Type:       mcp.TransportType(req.Msg.TransportType),
			Command:    req.Msg.Command,
			Args:       req.Msg.Args,
			Env:        envCopy,
			WorkingDir: req.Msg.WorkingDir,
			URL:        req.Msg.Url,
			Headers:    headersCopy,
		},
	}

	// Preserve existing tool policies and unmasked secrets if incoming fields contain placeholder
	if existing, ok := s.manager.GetServer(req.Msg.Id); ok {
		oldCfg := existing.Config()
		cfg.Tools = oldCfg.Tools

		for k, v := range cfg.Transport.Env {
			if isMaskedSecret(v) {
				if original, found := oldCfg.Transport.Env[k]; found {
					cfg.Transport.Env[k] = original
				}
			}
		}
		for k, v := range cfg.Transport.Headers {
			if isMaskedSecret(v) {
				if original, found := oldCfg.Transport.Headers[k]; found {
					cfg.Transport.Headers[k] = original
				}
			}
		}
	}

	if err := s.manager.UpdateServer(ctx, cfg); err != nil {
		return connect.NewResponse(&v1.UpdateMCPServerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.UpdateMCPServerResponse{Success: true}), nil
}

func (s *Service) DeleteMCPServer(
	ctx context.Context,
	req *connect.Request[v1.DeleteMCPServerRequest],
) (*connect.Response[v1.DeleteMCPServerResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.DeleteMCPServerResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), false); err != nil {
		return nil, err
	}

	if err := s.manager.RemoveServer(req.Msg.Id); err != nil {
		return connect.NewResponse(&v1.DeleteMCPServerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.DeleteMCPServerResponse{Success: true}), nil
}

func (s *Service) ToggleMCPServer(
	ctx context.Context,
	req *connect.Request[v1.ToggleMCPServerRequest],
) (*connect.Response[v1.ToggleMCPServerResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.ToggleMCPServerResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), false); err != nil {
		return nil, err
	}

	if err := s.manager.SetServerEnabled(ctx, req.Msg.Id, req.Msg.Enabled); err != nil {
		return connect.NewResponse(&v1.ToggleMCPServerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.ToggleMCPServerResponse{Success: true}), nil
}

func (s *Service) ToggleMCPTool(
	ctx context.Context,
	req *connect.Request[v1.ToggleMCPToolRequest],
) (*connect.Response[v1.ToggleMCPToolResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.ToggleMCPToolResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), false); err != nil {
		return nil, err
	}

	if err := s.manager.SetToolEnabled(req.Msg.ServerId, req.Msg.ToolName, req.Msg.Enabled); err != nil {
		return connect.NewResponse(&v1.ToggleMCPToolResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.ToggleMCPToolResponse{Success: true}), nil
}

func (s *Service) SyncMCPServer(
	ctx context.Context,
	req *connect.Request[v1.SyncMCPServerRequest],
) (*connect.Response[v1.SyncMCPServerResponse], error) {
	if s.manager == nil {
		return connect.NewResponse(&v1.SyncMCPServerResponse{
			Success: false,
			Error:   "mcp manager not initialized",
		}), nil
	}

	if err := checkControlPlaneAuth(req.Peer().Addr, req.Header().Get("Authorization"), false); err != nil {
		return nil, err
	}

	if err := s.manager.SyncServer(ctx, req.Msg.Id); err != nil {
		return connect.NewResponse(&v1.SyncMCPServerResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.SyncMCPServerResponse{Success: true}), nil
}

func (s *Service) buildServerInfo(srv *mcp.ServerRuntime) *v1.MCPServerInfo {
	cfg := srv.Config()
	catItems := srv.Catalog().List()

	tools := make([]*v1.MCPToolInfo, 0, len(catItems))
	enabledCount := 0
	for _, item := range catItems {
		if item.Enabled {
			enabledCount++
		}
		paramsJSON, _ := json.Marshal(item.Parameters)
		tools = append(tools, &v1.MCPToolInfo{
			Name:           item.RemoteName,
			FullName:       mcp.BuildFullName(cfg.ID, item.RemoteName),
			Description:    item.Description,
			Enabled:        item.Enabled,
			ParametersJson: string(paramsJSON),
		})
	}

	return &v1.MCPServerInfo{
		Id:                cfg.ID,
		Name:              cfg.Name,
		Enabled:           cfg.Enabled,
		Status:            string(srv.Status()),
		LastError:         srv.LastError(),
		TransportType:     string(cfg.Transport.Type),
		Command:           cfg.Transport.Command,
		Args:              cfg.Transport.Args,
		Env:               maskEnv(cfg.Transport.Env),
		WorkingDir:        cfg.Transport.WorkingDir,
		Url:               cfg.Transport.URL,
		Headers:           maskHeaders(cfg.Transport.Headers),
		ToolsCount:        int32(len(catItems)),
		EnabledToolsCount: int32(enabledCount),
		Tools:             tools,
	}
}
