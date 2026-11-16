package instance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
)

func TestSQLBehaviorSettingsKeepRunningRuntime(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	for _, method := range []string{"UpdateEnvVar", "UpdateEnvVars"} {
		for _, setting := range []struct {
			key    string
			values []string
		}{
			{"BOT_NAME", []string{"HotBot", "OtherBot"}},
			{"ENABLE_THINKING_STATUS", []string{"false", "true", "false"}},
			{"ENABLE_POKE", []string{"true", "false", "true"}},
			{"POKE_CUSTOM_TEXT", []string{"的耳朵", "的尾巴"}},
			{"POKE_COOLDOWN_SECONDS", []string{"15", "0", "60"}},
		} {
			t.Run(method+"/"+setting.key, func(t *testing.T) {
				m, err := NewDatabase(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer m.Close()
				info, err := m.Create("Hot Behavior")
				if err != nil {
					t.Fatal(err)
				}
				if err := m.Enable(info.ID, true); err != nil {
					t.Fatal(err)
				}
				runtime := m.instances[info.ID].runtime
				now := time.Now()
				if !runtime.Engine.PokeGate.Allow("synthetic-conversation", now, time.Minute) {
					t.Fatal("could not seed poke cooldown")
				}
				for _, value := range append(setting.values, "") {
					rpcMethod := method
					var body any
					if method == "UpdateEnvVars" {
						if value == "" {
							body = map[string]any{"clearKeys": []string{setting.key}}
						} else {
							body = map[string]any{"updates": []map[string]string{{"key": setting.key, "value": value}}}
						}
					} else if value == "" {
						rpcMethod = "DeleteEnvVar"
						body = map[string]string{"key": setting.key}
					} else {
						body = map[string]string{"key": setting.key, "value": value}
					}
					payload, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					request := localSettingsRequest(http.MethodPost,
						"/instances/"+info.ID+"/frostagent.v1.SettingsService/"+rpcMethod,
						strings.NewReader(string(payload)))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					m.ServeHTTP(response, request)
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
						t.Fatalf("%s %s=%q failed: %d %s", rpcMethod, setting.key, value, response.Code, response.Body.String())
					}
					if m.instances[info.ID].runtime != runtime {
						t.Fatalf("%s %s=%q rebuilt the runtime", rpcMethod, setting.key, value)
					}
					if err := runtime.Scope.Context().Err(); err != nil {
						t.Fatalf("%s %s=%q stopped the runtime: %v", rpcMethod, setting.key, value, err)
					}
					if got := runtime.Engine.Getenv(setting.key); got != value {
						t.Fatalf("engine sees %q, want %q", got, value)
					}
					if runtime.Engine.PokeGate.Ready("synthetic-conversation", now, time.Minute) {
						t.Fatal("settings update lost the existing poke cooldown")
					}
					reopened, err := instanceconfig.OpenDatabase(m.db, info.ID, false)
					if err != nil {
						t.Fatal(err)
					}
					got, exists := reopened.Snapshot()[setting.key]
					if got != value || exists != (value != "") {
						t.Fatalf("persisted setting = %q (exists=%v), want %q", got, exists, value)
					}
				}
			})
		}
	}
}

func TestSQLBehaviorDraftWithColdSettingRebuildsRuntime(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Mixed Behavior Draft")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	before := m.instances[info.ID].runtime
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"ENABLE_THINKING_STATUS","value":"false"},{"key":"ENABLE_POKE","value":"true"},{"key":"AGENT_MAX_ITERATIONS","value":"60"}]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("mixed draft failed: %d %s", response.Code, response.Body.String())
	}
	after := m.instances[info.ID].runtime
	if after == before || before.Scope.Context().Err() == nil {
		t.Fatal("constructor-time setting did not stop and replace the old runtime")
	}
	if after.Scope.Context().Err() != nil || after.Engine.EffectiveMaxIterations() != 60 {
		t.Fatal("replacement runtime did not start with the new iteration limit")
	}
	if after.Engine.Getenv("ENABLE_THINKING_STATUS") != "false" || after.Engine.Getenv("ENABLE_POKE") != "true" {
		t.Fatal("replacement runtime lost the hot settings in the mixed draft")
	}
}
