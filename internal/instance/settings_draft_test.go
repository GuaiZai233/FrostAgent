package instance

import (
	"errors"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func applySettingsDraft(m *Manager, id, body string) *httptest.ResponseRecorder {
	request := localSettingsRequest(http.MethodPost, "/instances/"+id+"/frostagent.v1.SettingsService/UpdateEnvVars", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	m.ServeHTTP(response, request)
	return response
}

func TestSQLSettingsDraftReenableProactiveUsesDefaultAfterClearingLegacyProbability(t *testing.T) {
	for _, probability := range []string{"0", "-0.1", "invalid", "NaN"} {
		t.Run(probability, func(t *testing.T) {
			m, err := NewDatabase(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			info, err := m.Create("Proactive Draft")
			if err != nil {
				t.Fatal(err)
			}
			config := m.instances[info.ID].config
			if err := config.Update("PROACTIVE_REPLY_PROBABILITY", probability, false); err != nil {
				t.Fatal(err)
			}
			if err := config.Update("ENABLE_PROACTIVE_REPLY", "false", false); err != nil {
				t.Fatal(err)
			}
			runtime := m.instances[info.ID].runtime
			if proactive.GetProbability(runtime.Scope.Getenv) != 0 {
				t.Fatal("disabled proactive replies must have zero probability")
			}
			response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"ENABLE_PROACTIVE_REPLY","value":"true"}],"clearKeys":["PROACTIVE_REPLY_PROBABILITY"]}`)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
				t.Fatalf("reenable failed: %s", response.Body.String())
			}
			if _, exists := config.Snapshot()["PROACTIVE_REPLY_PROBABILITY"]; exists {
				t.Fatal("legacy probability override was retained")
			}
			if got := proactive.GetProbability(runtime.Scope.Getenv); got != proactive.MinProbability {
				t.Fatalf("reenabling with the default must take effect: got %v", got)
			}
			if m.instances[info.ID].runtime != runtime {
				t.Fatal("proactive draft unnecessarily rebuilt the runtime")
			}
		})
	}
}

func TestSQLSettingsDraftAppliesOnceWithoutRebuildingHotRuntime(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Draft")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	runtime := m.instances[info.ID].runtime
	applyCount := 0
	m.SetGlobalApplyHook(func() error { applyCount++; return m.ApplyGlobalSettings() })
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"Draft Bot"},{"key":"BOT_ALIASES","value":"Draft,Fox"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-control-token"}],"clearKeys":["MAX_CONTEXT_MESSAGES"]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatalf("save failed: %d %s", response.Code, response.Body.String())
	}
	if applyCount != 1 || m.instances[info.ID].runtime != runtime || runtime.Scope.Context().Err() != nil {
		t.Fatalf("draft applied incorrectly: applies=%d runtime changed=%v", applyCount, m.instances[info.ID].runtime != runtime)
	}
	if runtime.Scope.Getenv("BOT_NAME") != "Draft Bot" || runtime.Scope.Getenv("BOT_ALIASES") != "Draft,Fox" || m.global.Get("MCP_CONTROL_TOKEN") != "synthetic-control-token" {
		t.Fatal("batch did not apply both scopes")
	}
	if _, exists := m.instances[info.ID].config.Snapshot()["MAX_CONTEXT_MESSAGES"]; exists {
		t.Fatal("clear was not included in the same draft")
	}
}

func TestSQLSettingsDraftValidationAndWriteFailuresAreAtomic(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Draft")
	if err != nil {
		t.Fatal(err)
	}
	before := m.instances[info.ID].config.Get("BOT_NAME")
	for _, body := range []string{
		`{"updates":[{"key":"BOT_NAME","value":"bad"},{"key":"MAX_CONTEXT_MESSAGES","value":"1"}]}`,
		`{"updates":[{"key":"BOT_NAME","value":"bad"}],"clearKeys":["BOT_NAME"]}`,
		`{"updates":[{"key":"BOT_NAME","value":"bad"},{"key":"UPSTREAM_API_KEY","value":"retired"}]}`,
	} {
		response := applySettingsDraft(m, info.ID, body)
		if strings.Contains(response.Body.String(), `"success":true`) || m.instances[info.ID].config.Get("BOT_NAME") != before {
			t.Fatalf("invalid draft was partially written: %s", response.Body.String())
		}
	}
	if _, err := m.db.SQL.Exec(`CREATE TRIGGER reject_draft_token BEFORE INSERT ON settings
		WHEN NEW.key = 'MCP_CONTROL_TOKEN' BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"BOT_NAME","value":"bad"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-token"}]}`)
	if strings.Contains(response.Body.String(), `"success":true`) || m.instances[info.ID].config.Get("BOT_NAME") != before {
		t.Fatalf("write failure changed runtime cache: %s", response.Body.String())
	}
	reopened, err := instanceconfig.OpenDatabase(m.db, info.ID, false)
	if err != nil || reopened.Get("BOT_NAME") != before || m.global.Get("MCP_CONTROL_TOKEN") != "" {
		t.Fatalf("write failure persisted part of the draft: %v", err)
	}
}

func TestSQLSettingsDraftApplyFailureRestoresBothScopes(t *testing.T) {
	for _, reload := range []bool{false, true} {
		t.Run(map[bool]string{false: "hot", true: "constructor"}[reload], func(t *testing.T) {
			m, err := NewDatabase(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			info, err := m.Create("Draft")
			if err != nil {
				t.Fatal(err)
			}
			beforeName := m.instances[info.ID].config.Get("BOT_NAME")
			beforeIterations := m.instances[info.ID].config.Get("AGENT_MAX_ITERATIONS")
			applyCount := 0
			m.SetGlobalApplyHook(func() error {
				applyCount++
				if applyCount == 1 {
					return errors.New("synthetic listener failure")
				}
				return m.ApplyGlobalSettings()
			})
			body := `{"updates":[{"key":"BOT_NAME","value":"changed"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-token"}]}`
			if reload {
				body = `{"updates":[{"key":"BOT_NAME","value":"changed"},{"key":"AGENT_MAX_ITERATIONS","value":"42"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-token"}]}`
			}
			response := applySettingsDraft(m, info.ID, body)
			if response.Code == http.StatusOK || m.global.Get("MCP_CONTROL_TOKEN") != "" || m.instances[info.ID].config.Get("BOT_NAME") != beforeName ||
				m.instances[info.ID].config.Get("AGENT_MAX_ITERATIONS") != beforeIterations || applyCount != 2 {
				t.Fatalf("apply failure did not restore both scopes: %d %s calls=%d", response.Code, response.Body.String(), applyCount)
			}
		})
	}
}

func TestSQLSettingsDraftRuntimeFailureRestoresGlobalScope(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	info, err := m.Create("Draft")
	if err != nil {
		t.Fatal(err)
	}
	before := m.instances[info.ID].config.Get("AGENT_MAX_ITERATIONS")
	builder := m.buildRuntime
	m.buildRuntime = func(manager *Manager, item *managed, config *instanceconfig.Store, dir string, enabled bool) (*Runtime, error) {
		if config.Get("AGENT_MAX_ITERATIONS") == "42" {
			return nil, errors.New("synthetic runtime failure")
		}
		return builder(manager, item, config, dir, enabled)
	}
	response := applySettingsDraft(m, info.ID, `{"updates":[{"key":"AGENT_MAX_ITERATIONS","value":"42"},{"key":"MCP_CONTROL_TOKEN","value":"synthetic-token"}]}`)
	if response.Code == http.StatusOK || m.instances[info.ID].config.Get("AGENT_MAX_ITERATIONS") != before || m.global.Get("MCP_CONTROL_TOKEN") != "" {
		t.Fatalf("runtime failure left part of the draft: %d %s", response.Code, response.Body.String())
	}
}

func TestSQLSettingsDraftRollbackCannotOverwriteAnotherInstanceSave(t *testing.T) {
	m, err := NewDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Create("First")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create("Second")
	if err != nil {
		t.Fatal(err)
	}
	applying := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	calls := 0
	m.SetGlobalApplyHook(func() error {
		calls++
		if calls == 1 {
			close(applying)
			<-release
			return errors.New("synthetic listener failure")
		}
		return m.ApplyGlobalSettings()
	})
	first := make(chan *httptest.ResponseRecorder, 1)
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		first <- applySettingsDraft(m, a.ID, `{"updates":[{"key":"MCP_CONTROL_TOKEN","value":"synthetic-first"}]}`)
	}()
	select {
	case <-applying:
	case <-time.After(10 * time.Second):
		t.Fatal("first draft did not reach global application")
	}
	if m.settingsWriteMu.TryLock() {
		m.settingsWriteMu.Unlock()
		t.Fatal("draft released the global write lock before application and rollback")
	}
	go func() {
		second <- applySettingsDraft(m, b.ID, `{"updates":[{"key":"MCP_CONTROL_TOKEN","value":"synthetic-second"}]}`)
	}()
	close(release)
	select {
	case response := <-first:
		if response.Code == http.StatusOK {
			t.Fatal("failed draft reported success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first draft did not finish rollback")
	}
	select {
	case response := <-second:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"success":true`) {
			t.Fatalf("second draft failed: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second draft remained blocked")
	}
	if calls != 3 || m.global.Get("MCP_CONTROL_TOKEN") != "synthetic-second" {
		t.Fatalf("rollback lost the successful draft: applies=%d", calls)
	}
}
