package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/mcp"
)

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
	}

	// Preserve existing tool policies
	if existing, ok := s.manager.GetServer(req.Msg.Id); ok {
		cfg.Tools = existing.Config().Tools
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
		Env:               cfg.Transport.Env,
		WorkingDir:        cfg.Transport.WorkingDir,
		Url:               cfg.Transport.URL,
		Headers:           cfg.Transport.Headers,
		ToolsCount:        int32(len(catItems)),
		EnabledToolsCount: int32(enabledCount),
		Tools:             tools,
	}
}
