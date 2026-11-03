package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/gorilla/websocket"
)

func TestAdapterListenerRejectsMockBeforeDispatch(t *testing.T) {
	for _, adapter := range []string{"onebot", "astrbot"} {
		for _, attempt := range []struct{ name, query, header string }{
			{"query", "?mock=true", ""},
			{"encoded query", "?m%6fck=tr%75e", ""},
			{"header", "", "true"},
			{"header with false query", "?mock=false", "true"},
		} {
			t.Run(adapter+"/"+attempt.name, func(t *testing.T) {
				called := false
				handler := instanceWebSocketHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				}))
				request := httptest.NewRequest(http.MethodGet, "/instances/synthetic-instance/ws/"+adapter+attempt.query, nil)
				request.RemoteAddr = "198.51.100.10:12345"
				request.Header.Set("X-Mock-Adapter", attempt.header)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusForbidden || called {
					t.Fatalf("mock reached adapter dispatch: status=%d called=%t", response.Code, called)
				}
			})
		}
	}
}

func TestAdapterListenerMockIsolationPreservesDirectChat(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	for _, config := range []struct {
		mode, enabled string
		status        int
	}{
		{"reverse", "true", http.StatusSwitchingProtocols},
		{"reverse", "false", http.StatusServiceUnavailable},
		{"forward", "true", http.StatusConflict},
		{"forward", "false", http.StatusServiceUnavailable},
	} {
		t.Run(config.mode+"/enabled="+config.enabled, func(t *testing.T) {
			gateway, err := newStorageGateway(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			manager, _, _ := gateway.snapshot()
			info, err := manager.Create("Listener Isolation")
			if err != nil {
				t.Fatal(err)
			}
			management := httptest.NewServer(managementMux(gateway))
			defer management.Close()
			settings := frostagentv1connect.NewSettingsServiceClient(management.Client(), management.URL+"/instances/"+info.ID)
			var updates []*v1.EnvVar
			for _, prefix := range []string{"ONEBOT", "ASTRBOT"} {
				updates = append(updates,
					&v1.EnvVar{Key: prefix + "_WS_ENABLED", Value: config.enabled},
					&v1.EnvVar{Key: prefix + "_WS_MODE", Value: config.mode},
					&v1.EnvVar{Key: prefix + "_WS_URL", Value: "ws" + strings.TrimPrefix(upstream.URL, "http")},
				)
			}
			response, err := settings.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: updates}))
			if err != nil || !response.Msg.Success {
				t.Fatalf("configure adapters: %v %v", response, err)
			}
			if err := manager.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			adapterMux := http.NewServeMux()
			adapterMux.Handle("/instances/", instanceWebSocketHandler(gateway))
			adapterListener := httptest.NewServer(adapterMux)
			defer adapterListener.Close()
			dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
			assertHandshake := func(base, path string, header http.Header, want int) {
				t.Helper()
				connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(base, "http")+path, header)
				if connection != nil {
					connection.Close()
				}
				if response == nil {
					t.Fatalf("missing handshake response: %v", err)
				}
				response.Body.Close()
				if response.StatusCode != want || (err == nil) != (want == http.StatusSwitchingProtocols) {
					t.Fatalf("%s%s: status=%d want=%d err=%v", base, path, response.StatusCode, want, err)
				}
			}
			for _, adapter := range []string{"onebot", "astrbot"} {
				path := "/instances/" + info.ID + "/ws/" + adapter
				assertHandshake(adapterListener.URL, path, nil, config.status)
				for _, attempt := range []struct{ query, header string }{
					{"?mock=true", ""}, {"", "true"},
				} {
					headers := http.Header{}
					headers.Set("X-Mock-Adapter", attempt.header)
					// Non-browser peers can omit Origin; the listener must still reject them.
					assertHandshake(adapterListener.URL, path+attempt.query, headers, http.StatusForbidden)
					headers.Set("Origin", management.URL)
					assertHandshake(management.URL, path+attempt.query, headers, http.StatusSwitchingProtocols)
				}
			}
		})
	}
}
