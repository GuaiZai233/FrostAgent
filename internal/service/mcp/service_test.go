package mcp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	mcpcore "FrostAgent/internal/mcp"
	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func createTestServerFactory(name string, toolNames []string) func(mcpcore.TransportConfig) (officialmcp.Transport, error) {
	server := officialmcp.NewServer(&officialmcp.Implementation{Name: name, Version: "1.0"}, nil)
	for _, toolName := range toolNames {
		tName := toolName
		server.AddTool(&officialmcp.Tool{
			Name:        tName,
			Description: "Echo " + tName,
			InputSchema: map[string]any{"type": "object"},
		}, func(ctx context.Context, req *officialmcp.CallToolRequest) (*officialmcp.CallToolResult, error) {
			return &officialmcp.CallToolResult{
				Content: []officialmcp.Content{&officialmcp.TextContent{Text: "echoed"}},
			}, nil
		})
	}

	return func(cfg mcpcore.TransportConfig) (officialmcp.Transport, error) {
		serverTransport, clientTransport := officialmcp.NewInMemoryTransports()
		_, err := server.Connect(context.Background(), serverTransport, nil)
		if err != nil {
			return nil, err
		}
		return clientTransport, nil
	}
}

func TestMCPServiceRPCs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcpsvc_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := mcpcore.NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	factory := createTestServerFactory("test_server", []string{"echo"})
	mgr := mcpcore.NewManagerWithFactory(store, []string{"memory"}, factory)

	svc := New(mgr)
	ctx := context.Background()

	// 1. AddMCPServer with sensitive env & header
	addResp, err := svc.AddMCPServer(ctx, connect.NewRequest(&v1.AddMCPServerRequest{
		Id:            "test_server",
		Name:          "Test Server",
		Enabled:       true,
		TransportType: "stdio",
		Command:       "echo",
		Args:          []string{"hello"},
		Env: map[string]string{
			"API_KEY":      "super_secret_key_123",
			"REGULAR_CONF": "normal_value",
		},
		Headers: map[string]string{
			"Authorization": "Bearer confidential_token",
			"X-Custom":      "regular_header",
		},
	}))
	if err != nil {
		t.Fatalf("AddMCPServer failed: %v", err)
	}
	if !addResp.Msg.Success {
		t.Fatalf("AddMCPServer returned success=false: %s", addResp.Msg.Error)
	}

	// 2. ListMCPServers (verify secret masking)
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
	if s0.Env["API_KEY"] != MaskedSecret {
		t.Fatalf("expected API_KEY to be masked as %q, got %q", MaskedSecret, s0.Env["API_KEY"])
	}
	if s0.Env["REGULAR_CONF"] != "normal_value" {
		t.Fatalf("expected REGULAR_CONF to be preserved, got %q", s0.Env["REGULAR_CONF"])
	}
	if s0.Headers["Authorization"] != MaskedSecret {
		t.Fatalf("expected Authorization header to be masked, got %q", s0.Headers["Authorization"])
	}
	if s0.Headers["X-Custom"] != "regular_header" {
		t.Fatalf("expected X-Custom header to be preserved, got %q", s0.Headers["X-Custom"])
	}

	// 3. GetMCPServer
	getResp, err := svc.GetMCPServer(ctx, connect.NewRequest(&v1.GetMCPServerRequest{Id: "test_server"}))
	if err != nil {
		t.Fatalf("GetMCPServer failed: %v", err)
	}
	if getResp.Msg.Server.Id != "test_server" {
		t.Fatalf("unexpected get server id: %s", getResp.Msg.Server.Id)
	}
	if getResp.Msg.Server.Env["API_KEY"] != MaskedSecret {
		t.Fatalf("expected masked secret in GetMCPServer")
	}

	// 4. UpdateMCPServer with placeholder "******" -> should restore original secrets!
	updateResp, err := svc.UpdateMCPServer(ctx, connect.NewRequest(&v1.UpdateMCPServerRequest{
		Id:            "test_server",
		Name:          "Test Server Updated",
		Enabled:       true,
		TransportType: "stdio",
		Command:       "echo",
		Args:          []string{"hello"},
		Env: map[string]string{
			"API_KEY":      MaskedSecret, // Masked placeholder sent by frontend
			"REGULAR_CONF": "updated_value",
		},
		Headers: map[string]string{
			"Authorization": MaskedSecret,
			"X-Custom":      "updated_header",
		},
	}))
	if err != nil || !updateResp.Msg.Success {
		t.Fatalf("UpdateMCPServer failed: %v, err=%s", err, updateResp.Msg.Error)
	}

	// Verify the underlying config retained the secret
	srvRuntime, ok := mgr.GetServer("test_server")
	if !ok {
		t.Fatalf("failed to get server runtime")
	}
	cfg := srvRuntime.Config()
	if cfg.Transport.Env["API_KEY"] != "super_secret_key_123" {
		t.Fatalf("expected restored API_KEY secret, got %q", cfg.Transport.Env["API_KEY"])
	}
	if cfg.Transport.Headers["Authorization"] != "Bearer confidential_token" {
		t.Fatalf("expected restored Authorization header, got %q", cfg.Transport.Headers["Authorization"])
	}

	// 5. ToggleMCPTool
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

	// 6. ToggleMCPServer
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

	// 7. DeleteMCPServer
	delResp, err := svc.DeleteMCPServer(ctx, connect.NewRequest(&v1.DeleteMCPServerRequest{Id: "test_server"}))
	if err != nil || !delResp.Msg.Success {
		t.Fatalf("DeleteMCPServer failed: %v", err)
	}

	listResp2, _ := svc.ListMCPServers(ctx, connect.NewRequest(&v1.ListMCPServersRequest{}))
	if len(listResp2.Msg.Servers) != 0 {
		t.Fatalf("expected 0 servers after delete, got %d", len(listResp2.Msg.Servers))
	}
}

func TestIsTrustedOrigin(t *testing.T) {
	tests := []struct {
		origin  string
		host    string
		trusted bool
	}{
		{"", "localhost:8080", true},
		{"http://localhost:8080", "localhost:8080", true},
		{"http://localhost:4200", "localhost:8080", true}, // loopback dev server
		{"http://127.0.0.1:8080", "127.0.0.1:8080", true},
		{"http://[::1]:8080", "[::1]:8080", true},
		{"https://localhost", "localhost", true},
		{"http://my-dashboard.internal:8080", "my-dashboard.internal:8080", true}, // matching host
		{"https://my-dashboard.internal", "my-dashboard.internal", true},         // matching host
		{"https://evil.example", "localhost:8080", false},                        // untrusted external origin
		{"http://attacker.com:8080", "127.0.0.1:8080", false},                   // untrusted external origin
		{"https://evil.localhost.com", "localhost:8080", false},                  // spoofed subdomain
		{"null", "localhost:8080", false},                                        // sandboxed iframe / null origin
		{"javascript:alert(1)", "localhost:8080", false},                         // invalid scheme
		{"ftp://localhost", "localhost:8080", false},                             // non-http scheme
	}

	for _, tc := range tests {
		got := IsTrustedOrigin(tc.origin, tc.host)
		if got != tc.trusted {
			t.Errorf("IsTrustedOrigin(%q, %q) = %v; want %v", tc.origin, tc.host, got, tc.trusted)
		}
	}

	// Test MCP_ALLOWED_ORIGINS
	os.Setenv("MCP_ALLOWED_ORIGINS", "https://trusted-portal.com, https://admin.internal")
	defer os.Unsetenv("MCP_ALLOWED_ORIGINS")

	if !IsTrustedOrigin("https://trusted-portal.com", "localhost:8080") {
		t.Errorf("expected allowlisted origin https://trusted-portal.com to be trusted")
	}
	if !IsTrustedOrigin("https://admin.internal", "localhost:8080") {
		t.Errorf("expected allowlisted origin https://admin.internal to be trusted")
	}
	if IsTrustedOrigin("https://not-in-list.com", "localhost:8080") {
		t.Errorf("expected unlisted origin to be untrusted")
	}
}

func TestControlPlaneSecurityBoundary(t *testing.T) {
	// 1. When no token is set: control plane access is restricted to loopback
	os.Unsetenv("MCP_CONTROL_TOKEN")
	os.Unsetenv("ADMIN_TOKEN")
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")

	// Remote address without token -> permission denied
	hRemote := make(http.Header)
	err := CheckControlPlaneAuth("192.168.1.100:45678", hRemote)
	if err == nil {
		t.Fatalf("expected permission denied for remote control plane access without token")
	}
	connectErr, ok := err.(*connect.Error)
	if !ok || connectErr.Code() != connect.CodePermissionDenied {
		t.Fatalf("expected CodePermissionDenied, got %v", err)
	}

	// Loopback addresses -> allowed
	loopbackAddrs := []string{
		"127.0.0.1:12345",
		"[::1]:12345",
		"localhost:8080",
		"",
	}
	for _, addr := range loopbackAddrs {
		if err := CheckControlPlaneAuth(addr, make(http.Header)); err != nil {
			t.Fatalf("expected loopback addr %q to be allowed, got: %v", addr, err)
		}
	}

	// Untrusted Origin header on loopback address -> MUST be rejected (CSRF / Origin bypass defense)
	hEvilOrigin := make(http.Header)
	hEvilOrigin.Set("Origin", "https://evil.example")
	hEvilOrigin.Set("Host", "localhost:8080")
	if err := CheckControlPlaneAuth("127.0.0.1:12345", hEvilOrigin); err == nil {
		t.Fatalf("expected permission denied for evil origin on loopback addr, got nil")
	} else {
		cErr, ok := err.(*connect.Error)
		if !ok || cErr.Code() != connect.CodePermissionDenied {
			t.Fatalf("expected CodePermissionDenied for evil origin, got %v", err)
		}
	}

	// Trusted Origin header on loopback address -> allowed
	hTrustedOrigin := make(http.Header)
	hTrustedOrigin.Set("Origin", "http://localhost:8080")
	hTrustedOrigin.Set("Host", "localhost:8080")
	if err := CheckControlPlaneAuth("127.0.0.1:12345", hTrustedOrigin); err != nil {
		t.Fatalf("expected trusted origin to be allowed on loopback, got: %v", err)
	}

	// With ALLOW_REMOTE_MCP_MANAGEMENT=true -> remote allowed
	os.Setenv("ALLOW_REMOTE_MCP_MANAGEMENT", "true")
	if err := CheckControlPlaneAuth("192.168.1.100:45678", make(http.Header)); err != nil {
		t.Fatalf("expected allowed with ALLOW_REMOTE_MCP_MANAGEMENT=true, got: %v", err)
	}
	os.Unsetenv("ALLOW_REMOTE_MCP_MANAGEMENT")

	// 2. When MCP_CONTROL_TOKEN is set
	os.Setenv("MCP_CONTROL_TOKEN", "super-secret-token")
	defer os.Unsetenv("MCP_CONTROL_TOKEN")

	// Remote without token -> unauthenticated
	if err := CheckControlPlaneAuth("192.168.1.100:45678", make(http.Header)); err == nil {
		t.Fatalf("expected unauthenticated when remote client lacks token")
	}

	// Remote with wrong token -> unauthenticated
	hWrongToken := make(http.Header)
	hWrongToken.Set("Authorization", "Bearer wrong")
	if err := CheckControlPlaneAuth("192.168.1.100:45678", hWrongToken); err == nil {
		t.Fatalf("expected unauthenticated with wrong token")
	}

	// Remote with valid token -> allowed
	hValidToken := make(http.Header)
	hValidToken.Set("Authorization", "Bearer super-secret-token")
	if err := CheckControlPlaneAuth("192.168.1.100:45678", hValidToken); err != nil {
		t.Fatalf("expected allowed with valid bearer token, got: %v", err)
	}

	// Local client without token -> allowed by default (preventing Web UI lockout on localhost)
	if err := CheckControlPlaneAuth("127.0.0.1:12345", make(http.Header)); err != nil {
		t.Fatalf("expected local same-origin without token to be allowed by default, got: %v", err)
	}

	// Local client with MCP_ENFORCE_LOCAL_TOKEN=true -> requires token
	os.Setenv("MCP_ENFORCE_LOCAL_TOKEN", "true")
	if err := CheckControlPlaneAuth("127.0.0.1:12345", make(http.Header)); err == nil {
		t.Fatalf("expected local client without token to be rejected when MCP_ENFORCE_LOCAL_TOKEN=true")
	}
	if err := CheckControlPlaneAuth("127.0.0.1:12345", hValidToken); err != nil {
		t.Fatalf("expected local client with valid token to be allowed when MCP_ENFORCE_LOCAL_TOKEN=true, got: %v", err)
	}
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")

	// End-to-end via service RPC with token enforcement
	os.Setenv("MCP_ENFORCE_LOCAL_TOKEN", "true")
	tmpDir, err := os.MkdirTemp("", "mcpsvc_sec_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := mcpcore.NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	mgr := mcpcore.NewManager(store, []string{"memory"})
	svc := New(mgr)
	ctx := context.Background()

	req := connect.NewRequest(&v1.AddMCPServerRequest{
		Id:            "remote_exploit",
		Name:          "Remote Exploit",
		Enabled:       false,
		TransportType: "stdio",
		Command:       "malicious_cmd",
	})
	// Without token in header -> CodeUnauthenticated
	_, err = svc.AddMCPServer(ctx, req)
	if err == nil {
		t.Fatalf("expected unauthenticated error from RPC")
	}

	// With valid Bearer token -> success
	req.Header().Set("Authorization", "Bearer super-secret-token")
	res, err := svc.AddMCPServer(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error with valid token: %v", err)
	}
	if !res.Msg.Success {
		t.Fatalf("expected success with valid token: %s", res.Msg.Error)
	}

	// ListMCPServers also protected by token
	listReq := connect.NewRequest(&v1.ListMCPServersRequest{})
	_, err = svc.ListMCPServers(ctx, listReq)
	if err == nil {
		t.Fatalf("expected unauthenticated for ListMCPServers without token")
	}
	listReq.Header().Set("Authorization", "Bearer super-secret-token")
	listRes, err := svc.ListMCPServers(ctx, listReq)
	if err != nil || len(listRes.Msg.Servers) != 1 {
		t.Fatalf("expected ListMCPServers to succeed with token, err=%v", err)
	}
	os.Unsetenv("MCP_ENFORCE_LOCAL_TOKEN")

	// 3. Verify COOKIE masking in headers and env
	cookieEnv := maskEnv(map[string]string{
		"SESSION_COOKIE": "session=xyz123",
		"NORMAL_KEY":     "plain",
	})
	if cookieEnv["SESSION_COOKIE"] != MaskedSecret {
		t.Fatalf("expected SESSION_COOKIE to be masked, got %q", cookieEnv["SESSION_COOKIE"])
	}
	cookieHeaders := maskHeaders(map[string]string{
		"Cookie": "uid=123",
	})
	if cookieHeaders["Cookie"] != MaskedSecret {
		t.Fatalf("expected Cookie header to be masked, got %q", cookieHeaders["Cookie"])
	}
}

// TestHTTP_CrossOriginCSRF_Rejected is the HTTP-layer regression test required by LamentBot:
// When an external malicious web origin (Origin: https://evil.example) attempts an MCP mutation
// via a loopback connection (e.g. browser fetch to localhost), the request MUST be rejected.
func TestHTTP_CrossOriginCSRF_Rejected(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mcp_http_sec_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store := mcpcore.NewConfigStore(filepath.Join(tmpDir, "mcp.json"))
	factory := createTestServerFactory("test_sec_server", []string{"ping"})
	mgr := mcpcore.NewManagerWithFactory(store, []string{"memory"}, factory)

	// Add an existing server to test ToggleMCPServer
	ctx := context.Background()
	_ = mgr.AddServer(ctx, mcpcore.ServerConfig{
		ID:      "test_srv",
		Name:    "Test Server",
		Enabled: true,
	})

	svc := New(mgr)
	mcpPath, mcpHandler := frostagentv1connect.NewMCPServiceHandler(svc)

	mux := http.NewServeMux()
	mux.Handle(mcpPath, mcpHandler)

	// Wrap with exact CORS middleware behavior from cmd/app/main.go
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Host") == "" && r.Host != "" {
			r.Header.Set("Host", r.Host)
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if IsTrustedOrigin(origin, r.Host) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, Connect-Protocol-Version")
				w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
			} else {
				if strings.HasPrefix(r.URL.Path, "/frostagent.v1.MCPService/") {
					http.Error(w, "Forbidden cross-origin request on MCP control plane", http.StatusForbidden)
					return
				}
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		mux.ServeHTTP(w, r)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// 1. Attack Scenario A: Browser on https://evil.example sends POST to AddMCPServer to execute stdio command
	evilAddPayload := []byte(`{
		"id": "rce_server",
		"name": "RCE",
		"enabled": true,
		"transport_type": "stdio",
		"command": "calc.exe"
	}`)
	reqAdd, err := http.NewRequest("POST", ts.URL+"/frostagent.v1.MCPService/AddMCPServer", bytes.NewReader(evilAddPayload))
	if err != nil {
		t.Fatal(err)
	}
	reqAdd.Header.Set("Content-Type", "application/json")
	reqAdd.Header.Set("Origin", "https://evil.example") // Evil website origin
	// Connection RemoteAddr will be loopback because ts is a local test server

	respAdd, err := client.Do(reqAdd)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer respAdd.Body.Close()

	if respAdd.StatusCode != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 Forbidden for cross-origin AddMCPServer, got HTTP %d", respAdd.StatusCode)
	}

	// Verify server was NOT added
	if _, exists := mgr.GetServer("rce_server"); exists {
		t.Fatalf("security violation: rce_server was created despite untrusted cross-origin request!")
	}

	// 2. Attack Scenario B: Browser on https://evil.example sends POST to ToggleMCPServer (mutation)
	evilTogglePayload := []byte(`{"id": "test_srv", "enabled": false}`)
	reqToggle, err := http.NewRequest("POST", ts.URL+"/frostagent.v1.MCPService/ToggleMCPServer", bytes.NewReader(evilTogglePayload))
	if err != nil {
		t.Fatal(err)
	}
	reqToggle.Header.Set("Content-Type", "application/json")
	reqToggle.Header.Set("Origin", "https://evil.example")

	respToggle, err := client.Do(reqToggle)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer respToggle.Body.Close()

	if respToggle.StatusCode != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 Forbidden for cross-origin ToggleMCPServer, got HTTP %d", respToggle.StatusCode)
	}

	// 3. Attack Scenario C: Browser sends CORS preflight OPTIONS from https://evil.example
	reqOptions, err := http.NewRequest("OPTIONS", ts.URL+"/frostagent.v1.MCPService/AddMCPServer", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqOptions.Header.Set("Origin", "https://evil.example")
	reqOptions.Header.Set("Access-Control-Request-Method", "POST")

	respOptions, err := client.Do(reqOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer respOptions.Body.Close()

	if respOptions.StatusCode != http.StatusForbidden {
		t.Fatalf("expected HTTP 403 Forbidden for evil OPTIONS preflight, got HTTP %d", respOptions.StatusCode)
	}

	// 4. Legitimate Scenario: Same-origin / loopback web request
	legitTogglePayload := []byte(`{"id": "test_srv", "enabled": false}`)
	reqLegit, err := http.NewRequest("POST", ts.URL+"/frostagent.v1.MCPService/ToggleMCPServer", bytes.NewReader(legitTogglePayload))
	if err != nil {
		t.Fatal(err)
	}
	reqLegit.Header.Set("Content-Type", "application/json")
	reqLegit.Header.Set("Origin", "http://localhost:8080") // Trusted local origin

	respLegit, err := client.Do(reqLegit)
	if err != nil {
		t.Fatalf("failed to send legitimate request: %v", err)
	}
	defer respLegit.Body.Close()

	if respLegit.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 OK for legitimate local origin, got HTTP %d", respLegit.StatusCode)
	}
}

// Dummy reference to avoid unused import error
var _ = http.StatusOK
