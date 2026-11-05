package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
)

func TestDockerPublishedManagementRequiresToken(t *testing.T) {
	previous := instanceconfig.ContainerManagement
	instanceconfig.ContainerManagement = "true"
	t.Cleanup(func() { instanceconfig.ContainerManagement = previous })
	gateway, err := newStorageGateway(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	manager, _, _ := gateway.snapshot()
	const token = "synthetic-docker-control-token-with-32-bytes"
	if err := initializeDockerControlToken(manager.GlobalConfig(), func(string) string { return token }); err != nil {
		t.Fatal(err)
	}
	info, err := manager.Create("Synthetic Docker instance")
	if err != nil {
		t.Fatal(err)
	}
	handler := corsMiddleware(managementMux(gateway), manager.ControlPlaneGetenv())
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/instances", ""},
		{http.MethodGet, "/api/storage", ""},
		{http.MethodGet, "/api/instances/global/backup/settings", ""},
		{http.MethodGet, "/api/instances/global/extra-env", ""},
		{http.MethodPost, "/instances/" + info.ID + "/frostagent.v1.SettingsService/ListEnvVars", `{}`},
		{http.MethodPost, "/instances/" + info.ID + "/frostagent.v1.MCPService/ListMCPServers", `{}`},
	} {
		for _, peer := range []string{"172.20.0.1:12345", "192.168.65.1:12345", "[fd00::1]:12345", "198.51.100.10:12345"} {
			for _, credentials := range []string{"", "Bearer incorrect", "Bearer " + token} {
				request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				request.RemoteAddr, request.Host = peer, "localhost:8080"
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", credentials)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				want := http.StatusUnauthorized
				if credentials == "Bearer "+token {
					want = http.StatusOK
				}
				if response.Code != want {
					t.Fatalf("%s peer=%s authorized=%t: %d %s", tc.path, peer, want == 200, response.Code, response.Body.String())
				}
			}
		}
	}
}

func TestDockerBridgeCannotClaimLoopbackConnector(t *testing.T) {
	previous := instanceconfig.ContainerManagement
	instanceconfig.ContainerManagement = "true"
	t.Cleanup(func() { instanceconfig.ContainerManagement = previous })
	values := map[string]string{"MCP_CONTROL_TOKEN": "synthetic-control-token"}
	handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), func(key string) string { return values[key] })
	for _, peer := range []string{"127.0.0.1:12345", "172.20.0.1:12345"} {
		for _, enforce := range []bool{false, true} {
			values["MCP_ENFORCE_LOCAL_TOKEN"] = "false"
			if enforce {
				values["MCP_ENFORCE_LOCAL_TOKEN"] = "true"
			}
			request := httptest.NewRequest(http.MethodGet, "/api/instances", nil)
			request.RemoteAddr, request.Host = peer, "panel.example"
			request.Header.Set("X-Forwarded-For", "127.0.0.1")
			request.Header.Set("Cf-Access-Authenticated-User-Email", "synthetic@example.invalid")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			want := http.StatusUnauthorized
			if isLoopbackHost(peer) && !enforce {
				want = http.StatusNoContent
			}
			if response.Code != want {
				t.Fatalf("peer=%s enforce=%t: %d, want=%d", peer, enforce, response.Code, want)
			}
		}
	}
}
