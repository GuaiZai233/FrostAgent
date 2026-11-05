package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/gorilla/websocket"
)

func TestRealAdapterInboundAuthenticationAcrossListeners(t *testing.T) {
	oldContainer, oldLoopback := instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly
	t.Cleanup(func() {
		instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = oldContainer, oldLoopback
	})
	for _, build := range []struct{ name, container, loopback string }{
		{"native", "false", "false"}, {"docker bridge", "true", "false"}, {"docker host", "true", "true"},
	} {
		t.Run(build.name, func(t *testing.T) {
			instanceconfig.ContainerManagement, instanceconfig.ManagementLoopbackOnly = build.container, build.loopback
			gateway, err := newStorageGateway(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			manager, _, _ := gateway.snapshot()
			info, err := manager.Create("Inbound Authentication")
			if err != nil {
				t.Fatal(err)
			}
			const control = "synthetic-control-token"
			const outbound = "synthetic-outbound-token"
			for key, value := range map[string]string{"MCP_CONTROL_TOKEN": control, "WS_LISTEN_ADDR": "127.0.0.1:11023"} {
				if err := manager.GlobalConfig().Update(key, value, false); err != nil {
					t.Fatal(err)
				}
			}
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// A bridge peer with a forged local Host/proxy headers is not trusted.
				r.RemoteAddr = "172.19.0.7:45678"
				r.Host = "127.0.0.1:8080"
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
				managementMux(gateway).ServeHTTP(w, r)
			}))
			defer management.Close()
			localManagement := httptest.NewServer(managementMux(gateway))
			defer localManagement.Close()
			dedicated := httptest.NewServer(instanceWebSocketHandler(gateway))
			defer dedicated.Close()
			settings := frostagentv1connect.NewSettingsServiceClient(management.Client(), management.URL+"/instances/"+info.ID)
			save := func(updates []*v1.EnvVar, clear ...string) {
				t.Helper()
				req := connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: updates, ClearKeys: clear})
				req.Header().Set("Authorization", "Bearer "+control)
				res, err := settings.UpdateEnvVars(context.Background(), req)
				if err != nil || !res.Msg.Success {
					t.Fatalf("configure adapters: %v %v", res, err)
				}
			}
			save([]*v1.EnvVar{{Key: "ONEBOT_WS_TOKEN", Value: outbound}, {Key: "ASTRBOT_WS_TOKEN", Value: outbound}})
			if err := manager.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
			assertHandshake := func(base, path, token string, want int) {
				t.Helper()
				headers := http.Header{}
				if token != "" {
					headers.Set("Authorization", "Bearer "+token)
				}
				conn, res, err := dialer.Dial("ws"+strings.TrimPrefix(base, "http")+path, headers)
				if conn != nil {
					conn.Close()
				}
				if res == nil {
					t.Fatalf("missing handshake response: %v", err)
				}
				body, _ := io.ReadAll(res.Body)
				res.Body.Close()
				if res.StatusCode != want || (err == nil) != (want == http.StatusSwitchingProtocols) {
					t.Fatalf("handshake %s: status=%d want=%d err=%v", path, res.StatusCode, want, err)
				}
				if strings.Contains(string(body)+fmt.Sprint(res.Header), "synthetic-") {
					t.Fatal("handshake disclosed a credential")
				}
			}
			managementUnset := http.StatusSwitchingProtocols
			if build.container == "true" && build.loopback != "true" {
				managementUnset = http.StatusUnauthorized
			}
			for _, adapter := range []string{"onebot", "astrbot"} {
				path := "/instances/" + info.ID + "/ws/" + adapter
				assertHandshake(dedicated.URL, path, "", http.StatusSwitchingProtocols)
				for _, base := range []string{management.URL, localManagement.URL} {
					assertHandshake(base, path, "", managementUnset)
					assertHandshake(base, path, control, managementUnset)
					assertHandshake(base, path, outbound, managementUnset)
				}
			}
			for _, generation := range []string{"initial", "rotated"} {
				save([]*v1.EnvVar{
					{Key: "ONEBOT_WS_INBOUND_TOKEN", Value: "synthetic-onebot-" + generation},
					{Key: "ASTRBOT_WS_INBOUND_TOKEN", Value: "synthetic-astrbot-" + generation},
				})
				for _, adapter := range []string{"onebot", "astrbot"} {
					path := "/instances/" + info.ID + "/ws/" + adapter
					token := "synthetic-" + adapter + "-" + generation
					other := "onebot"
					if adapter == "onebot" {
						other = "astrbot"
					}
					for _, base := range []string{dedicated.URL, management.URL, localManagement.URL} {
						for _, invalid := range []string{"", "synthetic-incorrect", control, outbound, "synthetic-" + other + "-" + generation} {
							assertHandshake(base, path, invalid, http.StatusUnauthorized)
						}
						assertHandshake(base, path+"?access_token="+token, "", http.StatusUnauthorized)
						assertHandshake(base, path, token, http.StatusSwitchingProtocols)
						if generation == "rotated" {
							assertHandshake(base, path, "synthetic-"+adapter+"-initial", http.StatusUnauthorized)
						}
					}
					assertHandshake(management.URL, path+"?mock=true", token, http.StatusUnauthorized)
					assertHandshake(management.URL, path+"?mock=true", control, http.StatusSwitchingProtocols)
					assertHandshake(dedicated.URL, path+"?mock=true", token, http.StatusForbidden)
				}
			}
			save(nil, "ONEBOT_WS_INBOUND_TOKEN", "ASTRBOT_WS_INBOUND_TOKEN")
			for _, adapter := range []string{"onebot", "astrbot"} {
				path := "/instances/" + info.ID + "/ws/" + adapter
				assertHandshake(dedicated.URL, path, "", http.StatusSwitchingProtocols)
				assertHandshake(management.URL, path, "", managementUnset)
			}
		})
	}
}
