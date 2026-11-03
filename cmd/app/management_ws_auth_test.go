package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
)

func TestManagementMockAuthenticationBoundary(t *testing.T) {
	const token = "synthetic-management-token"
	protocol := "frostagent.direct-chat, frostagent.control-token." + base64.RawURLEncoding.EncodeToString([]byte(token))
	for _, adapter := range []string{"onebot", "astrbot"} {
		for _, mockFlag := range []string{"query", "header"} {
			for _, tc := range []struct {
				name, remote, host, origin, authorization, protocols string
				enforceLocal, container, noToken, forwarded          bool
				want                                                 int
			}{
				{name: "remote without Origin", want: 401},
				{name: "forged same Origin", origin: "https://panel.example", want: 401},
				{name: "incorrect bearer", authorization: "Bearer incorrect", want: 401},
				{name: "valid bearer", authorization: "Bearer " + token, want: 204},
				{name: "browser protocol", protocols: protocol, want: 204},
				{name: "incorrect browser token", protocols: "frostagent.direct-chat, frostagent.control-token.aW5jb3JyZWN0", want: 401},
				{name: "malformed browser token", protocols: "frostagent.direct-chat, frostagent.control-token.%%%", want: 401},
				{name: "empty browser token", protocols: "frostagent.direct-chat, frostagent.control-token.", want: 401},
				{name: "duplicate browser token", protocols: protocol + ", frostagent.control-token.aW5jb3JyZWN0", want: 401},
				{name: "bearer cannot be overridden", authorization: "Bearer incorrect", protocols: protocol, want: 401},
				{name: "no configured token", protocols: protocol, noToken: true, want: 401},
				{name: "trusted local", remote: "127.0.0.1:12345", host: "localhost:8080", want: 204},
				{name: "enforced local", remote: "127.0.0.1:12345", host: "localhost:8080", enforceLocal: true, want: 401},
				{name: "authenticated local", remote: "127.0.0.1:12345", host: "localhost:8080", enforceLocal: true, protocols: protocol, want: 204},
				{name: "local reverse proxy", remote: "127.0.0.1:12345", want: 401},
				{name: "forwarded local host", remote: "127.0.0.1:12345", host: "localhost:8080", forwarded: true, want: 401},
				{name: "trusted container connector", remote: "127.0.0.1:12345", container: true, want: 204},
				{name: "container remote peer", container: true, want: 401},
				{name: "container enforced token", remote: "127.0.0.1:12345", container: true, enforceLocal: true, want: 401},
				{name: "authenticated connector", remote: "127.0.0.1:12345", container: true, enforceLocal: true, protocols: protocol, want: 204},
			} {
				t.Run(adapter+"/"+mockFlag+"/"+tc.name, func(t *testing.T) {
					previous := instanceconfig.ManagementLoopbackOnly
					instanceconfig.ManagementLoopbackOnly = "false"
					if tc.container {
						instanceconfig.ManagementLoopbackOnly = "true"
					}
					t.Cleanup(func() { instanceconfig.ManagementLoopbackOnly = previous })
					values := map[string]string{"MCP_CONTROL_TOKEN": token, "ALLOW_REMOTE_MCP_MANAGEMENT": "true"}
					if tc.noToken {
						values["MCP_CONTROL_TOKEN"] = ""
					}
					if tc.enforceLocal {
						values["MCP_ENFORCE_LOCAL_TOKEN"] = "true"
					}
					called := false
					handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						called = true
						w.WriteHeader(http.StatusNoContent)
					}), func(key string) string { return values[key] })
					path := "/instances/synthetic-instance/ws/" + adapter
					if mockFlag == "query" {
						path += "?mock=true"
					}
					request := httptest.NewRequest(http.MethodGet, path, nil)
					request.RemoteAddr, request.Host = "198.51.100.10:12345", "panel.example"
					if tc.remote != "" {
						request.RemoteAddr = tc.remote
					}
					if tc.host != "" {
						request.Host = tc.host
					}
					if mockFlag == "header" {
						request.Header.Set("X-Mock-Adapter", "true")
					}
					request.Header.Set("Origin", tc.origin)
					request.Header.Set("Authorization", tc.authorization)
					request.Header.Set("Sec-WebSocket-Protocol", tc.protocols)
					if tc.forwarded {
						request.Header.Set("X-Forwarded-For", "198.51.100.20")
					}
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != tc.want || called != (tc.want == 204) {
						t.Fatalf("status=%d called=%t, want=%d", response.Code, called, tc.want)
					}
					if strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), base64.RawURLEncoding.EncodeToString([]byte(token))) {
						t.Fatal("authentication failure disclosed control credentials")
					}
				})
			}
		}
	}
}

func TestManagementMockTokenRotationAndAdminFallback(t *testing.T) {
	values := map[string]string{"MCP_CONTROL_TOKEN": "synthetic-old-token"}
	handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), func(key string) string { return values[key] })
	request := httptest.NewRequest(http.MethodGet, "/instances/synthetic-instance/ws/astrbot?mock=true", nil)
	request.RemoteAddr = "198.51.100.10:12345"
	setProtocol := func(token string) {
		request.Header.Set("Sec-WebSocket-Protocol", "frostagent.direct-chat, frostagent.control-token."+base64.RawURLEncoding.EncodeToString([]byte(token)))
	}
	assertStatus := func(want int) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("status=%d, want=%d", response.Code, want)
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatal("credential parsing mutated the incoming request")
		}
	}
	setProtocol("synthetic-old-token")
	assertStatus(http.StatusNoContent)
	values["MCP_CONTROL_TOKEN"] = "synthetic-new-token"
	assertStatus(http.StatusUnauthorized)
	setProtocol("synthetic-new-token")
	assertStatus(http.StatusNoContent)
	delete(values, "MCP_CONTROL_TOKEN")
	values["ADMIN_TOKEN"] = "synthetic-admin-token"
	assertStatus(http.StatusUnauthorized)
	setProtocol("synthetic-admin-token")
	assertStatus(http.StatusNoContent)
}

func TestDirectChatCredentialParsingIsLimitedToMockWebSockets(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/instances/synthetic-instance/ws/onebot", 204},
		{"/instances/synthetic-instance/ws/astrbot", 204},
		{"/instances/synthetic-instance/api/v1/messages/send?mock=true", 204},
		{"/api/v1/messages/send?mock=true", 204},
		{"/api/instances?mock=true", 401},
	} {
		t.Run(tc.path, func(t *testing.T) {
			handler := managementAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}), func(string) string { return "synthetic-control-token" })
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.RemoteAddr = "198.51.100.10:12345"
			request.Header.Set("Sec-WebSocket-Protocol", "frostagent.direct-chat, frostagent.control-token."+base64.RawURLEncoding.EncodeToString([]byte("synthetic-control-token")))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status=%d, want=%d", response.Code, tc.want)
			}
		})
	}
}
