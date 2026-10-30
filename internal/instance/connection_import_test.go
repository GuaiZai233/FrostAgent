package instance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/gorilla/websocket"
)

func TestConnectionSettingsImportRejectsInvalidBeforeRuntimeStop(t *testing.T) {
	for _, adapter := range []string{"ONEBOT", "ASTRBOT"} {
		for _, enabled := range []bool{false, true} {
			t.Run(adapter+map[bool]string{false: "/disabled", true: "/enabled"}[enabled], func(t *testing.T) {
				m, err := NewDatabase(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				info, err := m.Create("Import Validation")
				if err != nil {
					t.Fatal(err)
				}
				if enabled {
					if err := m.Enable(info.ID, true); err != nil {
						t.Fatal(err)
					}
				}
				original := m.instances[info.ID].runtime
				for _, values := range []map[string]string{
					{adapter + "_WS_MODE": "forward", adapter + "_WS_URL": "http://invalid.example/?token=synthetic-import-secret", "BOT_NAME": "must-not-save"},
					{adapter + "_WS_MODE": "forward", "BOT_NAME": "must-not-save"},
				} {
					err := m.ImportSettings(info.ID, backup.Settings{FormatVersion: backup.FormatVersion, Values: values})
					if err == nil || strings.Contains(err.Error(), "synthetic-import-secret") {
						t.Fatalf("invalid import was accepted or echoed credentials: %v", err)
					}
					if m.instances[info.ID].runtime != original || (original.Scope.Context().Err() == nil) != enabled {
						t.Fatal("invalid import disturbed the existing runtime")
					}
					stored, err := instanceconfig.OpenDatabase(m.db, info.ID, false)
					if err != nil || stored.Get("BOT_NAME") == "must-not-save" || stored.Get(adapter+"_WS_MODE") != "" {
						t.Fatalf("invalid import changed persisted settings: %v", err)
					}
				}
				response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"still-manageable"}]}`)
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
					t.Fatalf("settings unavailable after rejected import: %s", response.Body.String())
				}
			})
		}
	}
}

func TestConnectionSettingsImportSwitchesModeAndRepairsDisabledInstance(t *testing.T) {
	for _, adapter := range []string{"ONEBOT", "ASTRBOT"} {
		t.Run(adapter, func(t *testing.T) {
			connected := make(chan string, 4)
			disconnected := make(chan struct{}, 4)
			upgrader := websocket.Upgrader{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				connected <- r.Header.Get("Authorization")
				for {
					if _, _, err := conn.ReadMessage(); err != nil {
						break
					}
				}
				disconnected <- struct{}{}
			}))
			defer upstream.Close()
			m, err := NewDatabase(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			info, err := m.Create("Import Mode Switch")
			if err != nil {
				t.Fatal(err)
			}
			config := m.instances[info.ID].config
			if err := config.Update(adapter+"_WS_TOKEN", "synthetic-existing-token", false); err != nil {
				t.Fatal(err)
			}
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			data := backup.Settings{FormatVersion: backup.FormatVersion, Values: map[string]string{
				adapter + "_WS_MODE": "forward", adapter + "_WS_URL": "ws" + strings.TrimPrefix(upstream.URL, "http"),
				adapter + "_WS_TOKEN": "synthetic-uploaded-token-must-be-ignored",
			}}
			if err := m.ImportSettings(info.ID, data); err != nil {
				t.Fatal(err)
			}
			select {
			case token := <-connected:
				if token != "Bearer synthetic-existing-token" {
					t.Fatal("import replaced the existing connection credential")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("valid forward import did not connect")
			}
			// Omitted non-secret fields are replaced by defaults during import.
			if err := m.ImportSettings(info.ID, backup.Settings{FormatVersion: backup.FormatVersion}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-disconnected:
			case <-time.After(5 * time.Second):
				t.Fatal("reverse import left the outbound connection active")
			}
			if err := m.Enable(info.ID, false); err != nil {
				t.Fatal(err)
			}
			config = m.instances[info.ID].config
			if err := config.Update(adapter+"_WS_MODE", "invalid", false); err != nil {
				t.Fatal(err)
			}
			if err := m.ImportSettings(info.ID, backup.Settings{FormatVersion: backup.FormatVersion}); err != nil {
				t.Fatalf("valid import could not repair disabled settings: %v", err)
			}
			if m.instances[info.ID].runtime == nil || m.instances[info.ID].runtime.Scope.Context().Err() == nil {
				t.Fatal("repair must preserve a manageable, disabled runtime")
			}
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatalf("repaired instance could not enable: %v", err)
			}
		})
	}
}
