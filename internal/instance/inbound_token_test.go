package instance

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/gorilla/websocket"
)

func TestInboundCredentialsRotatePersistAndStayInstanceScoped(t *testing.T) {
	for _, adapter := range []string{"onebot", "astrbot"} {
		t.Run(adapter, func(t *testing.T) {
			root := t.TempDir()
			m, err := NewDatabase(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { m.Close() }()
			info, err := m.Create("Inbound Credential Lifecycle")
			if err != nil {
				t.Fatal(err)
			}
			other, err := m.Create("Unrelated Instance")
			if err != nil {
				t.Fatal(err)
			}
			key := strings.ToUpper(adapter) + "_WS_INBOUND_TOKEN"
			save := func(value string) {
				t.Helper()
				body, _ := json.Marshal(map[string]any{"updates": []map[string]string{{"key": key, "value": value}}})
				res := applySettingsDraft(m, info.ID, string(body))
				if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"success":true`) {
					t.Fatal("failed to save inbound credential")
				}
				if m.instances[other.ID].config.Get(key) != "" || m.global.Get(key) != "" {
					t.Fatal("inbound credential escaped instance scope")
				}
			}
			save("synthetic-inbound-old")
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(m)
			defer server.Close()
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + info.ID + "/ws/" + adapter
			conn, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer synthetic-inbound-old"}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			save("synthetic-inbound-new")
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						t.Fatal("credential rotation did not close the old socket")
					}
					break
				}
			}
			// Non-secret settings imports must neither replace nor erase credentials.
			if err := m.ImportSettings(info.ID, backup.Settings{FormatVersion: backup.FormatVersion, Values: map[string]string{key: "synthetic-untrusted-import\n"}}); err != nil {
				t.Fatal(err)
			}
			if m.instances[info.ID].config.Get(key) != "synthetic-inbound-new" {
				t.Fatal("non-secret import replaced inbound credential")
			}
			if strings.Contains(fmt.Sprint(m.instances[info.ID].logger.Snapshot()), "synthetic-inbound-") {
				t.Fatal("inbound credential leaked into instance logs")
			}
			server.Close()
			m.Close()
			m, err = NewDatabase(root)
			if err != nil {
				t.Fatal(err)
			}
			if m.instances[info.ID].config.Get(key) != "synthetic-inbound-new" {
				t.Fatal("inbound credential lost after reopening SQL")
			}
			server = httptest.NewServer(m)
			defer server.Close()
			endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + info.ID + "/ws/" + adapter
			for _, token := range []string{"synthetic-inbound-old", "synthetic-inbound-new"} {
				conn, res, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer " + token}})
				if conn != nil {
					conn.Close()
				}
				if res != nil {
					res.Body.Close()
				}
				if token == "synthetic-inbound-new" && err != nil || token == "synthetic-inbound-old" && (res == nil || res.StatusCode != http.StatusUnauthorized) {
					t.Fatal("reopened runtime did not enforce the persisted credential")
				}
			}
		})
	}
}
