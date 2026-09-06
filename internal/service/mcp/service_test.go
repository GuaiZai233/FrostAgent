package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	mcpcore "FrostAgent/internal/mcp"
)

type mockServiceTransport struct{}

func (m *mockServiceTransport) RoundTrip(ctx context.Context, req *mcpcore.JSONRPCRequest) (*mcpcore.JSONRPCResponse, error) {
	switch req.Method {
	case "initialize":
		res, _ := json.Marshal(mcpcore.InitializeResult{
			ProtocolVersion: mcpcore.ProtocolVersion,
			ServerInfo:      mcpcore.ServerInfo{Name: "svc-mock", Version: "1.0"},
		})
		return &mcpcore.JSONRPCResponse{ID: req.ID, Result: res}, nil
	case "tools/list":
		res, _ := json.Marshal(mcpcore.ToolListResult{
			Tools: []mcpcore.MCPToolDefinition{
				{Name: "echo", Description: "Echo text", InputSchema: json.RawMessage(`{}`)},
			},
		})
		return &mcpcore.JSONRPCResponse{ID: req.ID, Result: res}, nil
	default:
		return &mcpcore.JSONRPCResponse{ID: req.ID}, nil
	}
}

func (m *mockServiceTransport) Notify(ctx context.Context, notif *mcpcore.JSONRPCNotification) error {
	return nil
}

func (m *mockServiceTransport) Close() error {
	return nil
}

func TestMCPServiceRPCs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcpsvc_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := mcpcore.NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	mgr := mcpcore.NewManagerWithFactory(store, []string{"memory"}, func(cfg mcpcore.TransportConfig) (mcpcore.Transport, error) {
		return &mockServiceTransport{}, nil
	})

	svc := New(mgr)
	ctx := context.Background()

	// 1. AddMCPServer
	addResp, err := svc.AddMCPServer(ctx, connect.NewRequest(&v1.AddMCPServerRequest{
		Id:            "test_server",
		Name:          "Test Server",
		Enabled:       true,
		TransportType: "stdio",
		Command:       "echo",
		Args:          []string{"hello"},
	}))
	if err != nil {
		t.Fatalf("AddMCPServer failed: %v", err)
	}
	if !addResp.Msg.Success {
		t.Fatalf("AddMCPServer returned success=false: %s", addResp.Msg.Error)
	}

	// 2. ListMCPServers
	listResp, err := svc.ListMCPServers(ctx, connect.NewRequest(&v1.ListMCPServersRequest{}))
	if err != nil {
		t.Fatalf("ListMCPServers failed: %v", err)
	}
	if len(listResp.Msg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(listResp.Msg.Servers))
	}
	s0 := listResp.Msg.Servers[0]
	if s0.Id != "test_server" || s0.Name != "Test Server" {
		t.Fatalf("unexpected server: %+v", s0)
	}
	if len(s0.Tools) != 1 || s0.Tools[0].Name != "echo" {
		t.Fatalf("expected tool echo, got %+v", s0.Tools)
	}

	// 3. GetMCPServer
	getResp, err := svc.GetMCPServer(ctx, connect.NewRequest(&v1.GetMCPServerRequest{Id: "test_server"}))
	if err != nil {
		t.Fatalf("GetMCPServer failed: %v", err)
	}
	if getResp.Msg.Server.Id != "test_server" {
		t.Fatalf("unexpected get server id: %s", getResp.Msg.Server.Id)
	}

	// 4. ToggleMCPTool
	toggleToolResp, err := svc.ToggleMCPTool(ctx, connect.NewRequest(&v1.ToggleMCPToolRequest{
		ServerId: "test_server",
		ToolName: "echo",
		Enabled:  false,
	}))
	if err != nil || !toggleToolResp.Msg.Success {
		t.Fatalf("ToggleMCPTool failed: %v", err)
	}

	// Verify tool is disabled
	getResp2, _ := svc.GetMCPServer(ctx, connect.NewRequest(&v1.GetMCPServerRequest{Id: "test_server"}))
	if getResp2.Msg.Server.Tools[0].Enabled {
		t.Fatalf("expected tool to be disabled")
	}

	// 5. ToggleMCPServer
	toggleSrvResp, err := svc.ToggleMCPServer(ctx, connect.NewRequest(&v1.ToggleMCPServerRequest{
		Id:      "test_server",
		Enabled: false,
	}))
	if err != nil || !toggleSrvResp.Msg.Success {
		t.Fatalf("ToggleMCPServer failed: %v", err)
	}

	getResp3, _ := svc.GetMCPServer(ctx, connect.NewRequest(&v1.GetMCPServerRequest{Id: "test_server"}))
	if getResp3.Msg.Server.Enabled {
		t.Fatalf("expected server to be disabled")
	}

	// 6. DeleteMCPServer
	delResp, err := svc.DeleteMCPServer(ctx, connect.NewRequest(&v1.DeleteMCPServerRequest{Id: "test_server"}))
	if err != nil || !delResp.Msg.Success {
		t.Fatalf("DeleteMCPServer failed: %v", err)
	}

	listResp2, _ := svc.ListMCPServers(ctx, connect.NewRequest(&v1.ListMCPServersRequest{}))
	if len(listResp2.Msg.Servers) != 0 {
		t.Fatalf("expected 0 servers after delete, got %d", len(listResp2.Msg.Servers))
	}
}
