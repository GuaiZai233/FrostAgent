package instance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

func lifecycleRPC(t *testing.T, m *Manager, id, method string, body any) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := localSettingsRequest(http.MethodPost,
		"/instances/"+id+"/frostagent.v1.SettingsService/"+method, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	var result struct {
		Success bool
		Error   string
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s: HTTP %d %s", method, response.Code, response.Body.String())
	}
	if response.Code != http.StatusOK || !result.Success {
		t.Fatalf("%s: HTTP %d, error=%s", method, response.Code, result.Error)
	}
}

func lifecycleRuntime(t *testing.T, m *Manager, id string, previous *Runtime, rebuilt bool) *Runtime {
	t.Helper()
	current := m.instances[id].runtime
	if current == nil || current.Scope.Context().Err() != nil {
		t.Fatal("settings apply left no running runtime")
	}
	if (current != previous) != rebuilt {
		t.Fatalf("runtime rebuilt=%v, want %v", current != previous, rebuilt)
	}
	if rebuilt && previous.Scope.Context().Err() == nil {
		t.Fatal("replaced runtime was not stopped")
	}
	return current
}

func lifecycleStored(t *testing.T, m *Manager, id, key, want string, present bool) {
	t.Helper()
	store, err := instanceconfig.OpenDatabase(m.db, id, id == "")
	if err != nil {
		t.Fatal(err)
	}
	value, exists := store.Snapshot()[key]
	if value != want || exists != present {
		t.Fatalf("%s scope=%q: persisted=%q (present=%v), want %q (present=%v)", key, id, value, exists, want, present)
	}
}

func TestSQLLifecycleManagerApplyAndClear(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	for _, method := range []string{"UpdateEnvVar", "UpdateEnvVars"} {
		t.Run(method, func(t *testing.T) {
			m, err := NewDatabase(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			// Shared global values are fixture setup. All mutations under test
			// below pass through the manager's HTTP settings entry point.
			if err := m.global.Update("SECURITY_GATEWAY_TIMEOUT", "50s", false); err != nil {
				t.Fatal(err)
			}
			if err := m.ApplyGlobalSettings(); err != nil {
				t.Fatal(err)
			}
			first, err := m.Create("Lifecycle first")
			if err != nil {
				t.Fatal(err)
			}
			other, err := m.Create("Lifecycle other")
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{first.ID, other.ID} {
				if err := m.Enable(id, true); err != nil {
					t.Fatal(err)
				}
			}
			current, peer := m.instances[first.ID].runtime, m.instances[other.ID].runtime
			mutate := func(key, value string, clear bool) {
				t.Helper()
				rpcMethod := method
				var body any
				if method == "UpdateEnvVars" {
					if clear {
						body = map[string]any{"clearKeys": []string{key}}
					} else {
						body = map[string]any{"updates": []map[string]string{{"key": key, "value": value}}}
					}
				} else if clear {
					rpcMethod = "DeleteEnvVar"
					body = map[string]string{"key": key}
				} else {
					body = map[string]string{"key": key, "value": value}
				}
				lifecycleRPC(t, m, first.ID, rpcMethod, body)
			}
			assertTimeout := func(id string, want time.Duration) {
				t.Helper()
				classifier, ok := m.security.Watchdog.ClassifierForInstance(id).(*security.LLMClassifier)
				if !ok || classifier.Timeout() != want {
					t.Fatalf("%s: classifier=%v, want timeout %s", id, classifier, want)
				}
			}
			assertTimeout(first.ID, 50*time.Second)
			for _, clear := range []bool{false, true} {
				value := "LifecycleBot"
				if clear {
					value = ""
				}
				mutate("BOT_NAME", value, clear)
				current = lifecycleRuntime(t, m, first.ID, current, false)
				if current.Engine.Getenv("BOT_NAME") != value {
					t.Fatal("instance hot setting did not reach the live engine")
				}
				lifecycleStored(t, m, first.ID, "BOT_NAME", value, !clear)
				lifecycleStored(t, m, "", "BOT_NAME", "", false)
				if peer.Engine.Getenv("BOT_NAME") != "助手" {
					t.Fatal("instance setting leaked to peer")
				}

				value = "off"
				wantMode := security.ControlModeOff
				if clear {
					value = ""
					wantMode = security.ControlModeSimple
				}
				mutate("SECURITY_CONTROL_MODE", value, clear)
				current = lifecycleRuntime(t, m, first.ID, current, false)
				lifecycleRuntime(t, m, other.ID, peer, false)
				if m.security.Mode() != wantMode || peer.Scope.Getenv("SECURITY_CONTROL_MODE") != value {
					t.Fatal("global hot setting did not update the controller and peer scope")
				}
				lifecycleStored(t, m, "", "SECURITY_CONTROL_MODE", value, !clear)
				lifecycleStored(t, m, first.ID, "SECURITY_CONTROL_MODE", "", false)

				value = "25s"
				timeout := 25 * time.Second
				if clear {
					value = ""
					timeout = 50 * time.Second
				}
				mutate("SECURITY_GATEWAY_TIMEOUT", value, clear)
				current = lifecycleRuntime(t, m, first.ID, current, true)
				lifecycleRuntime(t, m, other.ID, peer, false)
				assertTimeout(first.ID, timeout)
				assertTimeout(other.ID, 50*time.Second)
				lifecycleStored(t, m, first.ID, "SECURITY_GATEWAY_TIMEOUT", value, !clear)
				lifecycleStored(t, m, "", "SECURITY_GATEWAY_TIMEOUT", "50s", true)

				value = "60"
				wantIterations := 60
				if clear {
					value = ""
					wantIterations = llm.DefaultMaxIterations
				}
				mutate("AGENT_MAX_ITERATIONS", value, clear)
				current = lifecycleRuntime(t, m, first.ID, current, true)
				if current.Engine.EffectiveMaxIterations() != wantIterations {
					t.Fatalf("constructor setting ignored: got %d, want %d", current.Engine.EffectiveMaxIterations(), wantIterations)
				}
				lifecycleStored(t, m, first.ID, "AGENT_MAX_ITERATIONS", value, !clear)
				lifecycleRuntime(t, m, other.ID, peer, false)
			}
		})
	}
}

func TestSQLLifecycleMixedDraftSaveClearAndNoop(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Mixed lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	current := m.instances[info.ID].runtime
	draft := map[string]any{"updates": []map[string]string{
		{"key": "BOT_NAME", "value": "MixedBot"},
		{"key": "SECURITY_CONTROL_MODE", "value": "off"},
		{"key": "AGENT_MAX_ITERATIONS", "value": "60"},
	}}
	lifecycleRPC(t, m, info.ID, "UpdateEnvVars", draft)
	current = lifecycleRuntime(t, m, info.ID, current, true)
	if current.Engine.EffectiveMaxIterations() != 60 || current.Engine.Getenv("BOT_NAME") != "MixedBot" || m.security.Mode() != security.ControlModeOff {
		t.Fatal("mixed draft did not apply all scopes and constructor settings")
	}
	lifecycleStored(t, m, info.ID, "BOT_NAME", "MixedBot", true)
	lifecycleStored(t, m, "", "SECURITY_CONTROL_MODE", "off", true)
	lifecycleRPC(t, m, info.ID, "UpdateEnvVars", draft)
	current = lifecycleRuntime(t, m, info.ID, current, false)
	lifecycleRPC(t, m, info.ID, "UpdateEnvVars", map[string]any{
		"updates":   []map[string]string{{"key": "BOT_NAME", "value": "AfterClear"}},
		"clearKeys": []string{"AGENT_MAX_ITERATIONS", "SECURITY_CONTROL_MODE"},
	})
	current = lifecycleRuntime(t, m, info.ID, current, true)
	if current.Engine.EffectiveMaxIterations() != llm.DefaultMaxIterations || current.Engine.Getenv("BOT_NAME") != "AfterClear" || m.security.Mode() != security.ControlModeSimple {
		t.Fatal("mixed clear lost hot update or failed to restore code defaults")
	}
	lifecycleStored(t, m, info.ID, "AGENT_MAX_ITERATIONS", "", false)
	lifecycleStored(t, m, "", "SECURITY_CONTROL_MODE", "", false)
}

func TestSQLLifecycleInitializedDefaults(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Lifecycle defaults")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := instanceconfig.OpenDatabase(m.db, info.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	values := reopened.Snapshot()
	seeded := 0
	for key, entry := range loadSettingsLifecycle(t) {
		value, exists := values[key]
		if entry.Seed == nil {
			if exists {
				t.Errorf("%s: initialized an unseeded default", key)
			}
			continue
		}
		seeded++
		if !exists || value != *entry.Seed {
			t.Errorf("%s: initialized=%q (present=%v), want %q", key, value, exists, *entry.Seed)
		}
	}
	if len(values) != seeded {
		t.Errorf("initialized %d settings, inventory has %d seeds", len(values), seeded)
	}
}

func TestSQLLifecycleExplicitEmptyHotSettings(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", "")
	for _, method := range []string{"UpdateEnvVar", "UpdateEnvVars"} {
		t.Run(method, func(t *testing.T) {
			m, err := NewDatabase(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			info, err := m.Create("Empty hot settings")
			if err != nil {
				t.Fatal(err)
			}
			if err := m.Enable(info.ID, true); err != nil {
				t.Fatal(err)
			}
			current := m.instances[info.ID].runtime
			for _, step := range []struct {
				key   string
				clear bool
			}{
				{"POKE_CUSTOM_TEXT", false}, // absent -> explicitly empty
				{"POKE_CUSTOM_TEXT", false}, // no-op on stored empty value
				{"POKE_CUSTOM_TEXT", true},  // explicitly empty -> absent
				{"BOT_NAME", false},         // seeded nonempty -> empty
				{"BOT_NAME", true},
				{"ADMIN_QQ_IDS", true}, // seeded empty -> absent
			} {
				rpcMethod := method
				var body any
				if method == "UpdateEnvVars" {
					if step.clear {
						body = map[string]any{"clearKeys": []string{step.key}}
					} else {
						body = map[string]any{"updates": []map[string]string{{"key": step.key, "value": ""}}}
					}
				} else if step.clear {
					rpcMethod = "DeleteEnvVar"
					body = map[string]string{"key": step.key}
				} else {
					body = map[string]string{"key": step.key, "value": ""}
				}
				lifecycleRPC(t, m, info.ID, rpcMethod, body)
				current = lifecycleRuntime(t, m, info.ID, current, false)
				lifecycleStored(t, m, info.ID, step.key, "", !step.clear)
				if current.Engine.Getenv(step.key) != "" {
					t.Fatalf("%s: live scope retained a stale value", step.key)
				}
			}
			// Presence-only changes to a global constructor key remain cold.
			for _, clear := range []bool{false, true} {
				var body any = map[string]string{"key": "ALCYONE_SERVICE_TOKEN", "value": ""}
				rpcMethod := method
				if method == "UpdateEnvVars" {
					if clear {
						body = map[string]any{"clearKeys": []string{"ALCYONE_SERVICE_TOKEN"}}
					} else {
						body = map[string]any{"updates": []map[string]string{{"key": "ALCYONE_SERVICE_TOKEN", "value": ""}}}
					}
				} else if clear {
					rpcMethod = "DeleteEnvVar"
					body = map[string]string{"key": "ALCYONE_SERVICE_TOKEN"}
				}
				lifecycleRPC(t, m, info.ID, rpcMethod, body)
				current = lifecycleRuntime(t, m, info.ID, current, true)
				lifecycleStored(t, m, "", "ALCYONE_SERVICE_TOKEN", "", !clear)
			}
		})
	}
}
