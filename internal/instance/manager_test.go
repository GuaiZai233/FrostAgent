package instance

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/mcp"
	"FrostAgent/internal/modelrouter"
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoppedRuntimeRejectsNewInstanceLogStreams(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "stream-stop")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	item := m.instances[info.ID]
	item.op.Lock()
	defer item.op.Unlock()
	if err := m.stop(info.ID, item); err != nil {
		t.Fatal(err)
	}
	if item.runtime == nil || item.runtime.Scope.Context().Err() == nil {
		t.Fatal("stop did not retain a cancelled runtime for the deletion window")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.LogService/StreamLogs",
		strings.NewReader("{}"),
	).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("new log stream entered cancelled runtime: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestInstanceLogStreamTracksRuntimeCancellationAfterAdmission(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "stream-race")
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	item := m.instances[info.ID]
	item.op.Lock()
	defer item.op.Unlock()

	body := newGatedRequestBody("\x00\x00\x00\x00\x02{}")
	released := false
	defer func() {
		if !released {
			close(body.release)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.LogService/StreamLogs",
		nil,
	).WithContext(ctx)
	req.Body = body
	req.ContentLength = int64(body.reader.Len())
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, req)
		response <- w
	}()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("log stream request did not reach body decoding")
	}

	if err := m.stop(info.ID, item); err != nil {
		t.Fatal(err)
	}
	close(body.release)
	released = true
	select {
	case <-response:
	case <-time.After(time.Second):
		cancel()
		<-response
		t.Fatal("log stream survived captured runtime cancellation")
	}
}

func TestOverviewKeepsEffectiveWebSocketAddressUntilControlPlaneRestart(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "address-freeze")
	assertAddress := func(manager *Manager, want string) {
		t.Helper()
		w := rpc(t, manager, info.ID, "BotStatusService/GetOverview", "{}", false)
		if w.Code != http.StatusOK {
			t.Fatalf("overview status = %d, body=%s", w.Code, w.Body.String())
		}
		var response struct {
			WSListenAddr string `json:"wsListenAddr"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.WSListenAddr != want {
			t.Fatalf("overview WS address = %q, want %q", response.WSListenAddr, want)
		}
	}

	assertAddress(m, "127.0.0.1:1234")
	if err := m.global.Update("WS_LISTEN_ADDR", ":4321", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	assertAddress(m, "127.0.0.1:1234")

	m.Close()
	next, err := New(
		m.root,
		m.global,
		filepath.Join(filepath.Dir(m.root), "dialogue-restarted.yml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	assertAddress(next, ":4321")
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	g, err := instanceconfig.Open(filepath.Join(dir, ".env"), true)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(filepath.Join(dir, "data"), g, filepath.Join(dir, "dialogue.yml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func create(t *testing.T, m *Manager, name string) Info {
	t.Helper()
	i, err := m.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestSandboxIsNamespacedPerInstanceAndRegisteredFromGlobalConfig(t *testing.T) {
	var mu sync.Mutex
	var userUUIDs []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "synthetic-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/shell/exec":
			mu.Lock()
			userUUIDs = append(userUUIDs, r.URL.Query().Get("user_uuid"))
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"stdout":"ok","stderr":"","exit_code":0,"timed_out":false,"duration_ms":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	dir := t.TempDir()
	globalPath := filepath.Join(dir, ".env")
	raw := strings.Join([]string{
		"SANDBOX_ENABLED=true",
		"SANDBOX_BASE_URL=" + gateway.URL,
		"SANDBOX_AUTH_TOKEN=synthetic-token",
		"SANDBOX_SESSION_NAMESPACE=frostagent-test",
		"",
	}, "\n")
	if err := os.WriteFile(globalPath, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	global, err := instanceconfig.Open(globalPath, true)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(filepath.Join(dir, "data"), global, filepath.Join(dir, "dialogue.yml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	a := create(t, m, "sandbox-a")
	b := create(t, m, "sandbox-b")
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "shared-session"})
	for _, id := range []string{a.ID, b.ID, a.ID} {
		tool, ok := m.instances[id].runtime.Engine.ToolRegistry["execute_command"]
		if !ok {
			t.Fatalf("instance %s does not expose execute_command", id)
		}
		executor, ok := tool.(interface {
			ExecuteContext(context.Context, string) (string, error)
		})
		if !ok {
			t.Fatalf("instance %s execute_command is not context-aware", id)
		}
		if _, err := executor.ExecuteContext(ctx, `{"command":"printf ok"}`); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := append([]string(nil), userUUIDs...)
	mu.Unlock()
	if len(got) != 3 || got[0] == "" || got[0] == got[1] || got[0] != got[2] {
		t.Fatalf("sandbox UUID isolation/stability failed: %v", got)
	}

	disabled := testManager(t)
	disabledInfo := create(t, disabled, "sandbox-disabled")
	if _, ok := disabled.instances[disabledInfo.ID].runtime.Engine.ToolRegistry["execute_command"]; ok {
		t.Fatal("execute_command exposed while SANDBOX_ENABLED is false")
	}
}

func TestMCPIsIsolatedPerInstanceAndHasNoRootEndpoint(t *testing.T) {
	m := testManager(t)
	req := httptest.NewRequest(
		http.MethodPost,
		"/frostagent.v1.MCPService/ListMCPServers",
		strings.NewReader("{}"),
	)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("root MCP endpoint status = %d, want 404", w.Code)
	}

	a := create(t, m, "mcp-a")
	b := create(t, m, "mcp-b")
	if m.instances[a.ID].mcp == m.instances[b.ID].mcp {
		t.Fatal("instances share an MCP manager")
	}
	for _, info := range []Info{a, b} {
		runtime := m.instances[info.ID].runtime
		if runtime == nil || runtime.Engine.MCPManager != m.instances[info.ID].mcp {
			t.Fatalf("instance %s runtime does not use its own MCP manager", info.ID)
		}
	}

	req = httptest.NewRequest(
		http.MethodPost,
		"/instances/"+a.ID+"/frostagent.v1.MCPService/AddMCPServer",
		strings.NewReader(`{"id":"only-a","name":"Only A","enabled":false,"transportType":"stdio","command":"synthetic"}`),
	)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	m.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add MCP server status = %d: %s", w.Code, w.Body.String())
	}
	if _, ok := m.instances[a.ID].mcp.GetServer("only-a"); !ok {
		t.Fatal("instance A did not retain its MCP server")
	}
	if _, ok := m.instances[b.ID].mcp.GetServer("only-a"); ok {
		t.Fatal("instance B observed instance A MCP server")
	}
	if _, err := os.Stat(filepath.Join(m.dir(a.ID), "mcp_servers.json")); err != nil {
		t.Fatalf("instance MCP config was not persisted in its directory: %v", err)
	}
	m.Close()
	restarted, err := New(m.root, m.global, m.sharedPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if _, ok := restarted.instances[a.ID].mcp.GetServer("only-a"); !ok {
		t.Fatal("instance A MCP configuration was not restored after restart")
	}
	if _, ok := restarted.instances[b.ID].mcp.GetServer("only-a"); ok {
		t.Fatal("instance B loaded instance A MCP configuration after restart")
	}
}

func TestInstanceMCPAuthUsesControlPlaneStartupSnapshot(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "mcp-auth")
	if err := m.global.Update("MCP_CONTROL_TOKEN", "synthetic-control-token", false); err != nil {
		t.Fatal(err)
	}
	if err := m.global.Update("MCP_ENFORCE_LOCAL_TOKEN", "true", false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.MCPService/ListMCPServers",
		strings.NewReader("{}"),
	)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("MCP auth changed before Control Plane restart: %d %s", w.Code, w.Body.String())
	}
}

func rpc(t *testing.T, m *Manager, id, method, body string, includeGeneral bool) *httptest.ResponseRecorder {
	t.Helper()
	prefix := ""
	if id != "" {
		prefix = "/instances/" + id
	}
	req := httptest.NewRequest("POST", prefix+"/frostagent.v1."+method, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if includeGeneral {
		req.Header.Set("X-FrostAgent-Log-Source", "control-plane")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, req)
	return w
}
func TestEmptyControlPlaneAndNameSequence(t *testing.T) {
	m := testManager(t)
	list, next := m.List()
	if len(list) != 0 || next != 1 {
		t.Fatal(list, next)
	}
	first := create(t, m, "")
	if first.Name != "实例1" || first.Enabled || !idPattern.MatchString(first.ID) {
		t.Fatal(first)
	}
	if _, err := m.Create("实例1"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := m.Rename(first.ID, " renamed "); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create("RENAMED"); err == nil {
		t.Fatal("case insensitive duplicate accepted")
	}
	if err := m.Delete(first.ID, true); err != nil {
		t.Fatal(err)
	}
	list, _ = m.List()
	if len(list) != 0 {
		t.Fatal(list)
	}
	second := create(t, m, "")
	if second.Name != "实例2" || second.ID == first.ID {
		t.Fatal(second)
	}
	w := rpc(t, m, "", "LogService/ListLogs", "{}", false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestSettingsMemoryLogsAndMediaIsolation(t *testing.T) {
	m := testManager(t)
	a := create(t, m, "a")
	b := create(t, m, "b")
	w := rpc(t, m, a.ID, "SettingsService/UpdateEnvVar", `{"key":"BOT_NAME","value":"only-a"}`, false)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	if m.instances[a.ID].config.Get("BOT_NAME") != "only-a" || m.instances[b.ID].config.Get("BOT_NAME") == "only-a" {
		t.Fatal("settings leaked")
	}
	w = rpc(t, m, a.ID, "MemoryService/AddMemory", `{"owner":"test_owner","content":"instance-a-only","visibility":"private"}`, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"success":false`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = rpc(t, m, b.ID, "MemoryService/ListMemories", "{}", false)
	if strings.Contains(w.Body.String(), "instance-a-only") {
		t.Fatal("memory leaked")
	}
	full := strings.Repeat("隔离", 220)
	m.instances[a.ID].logger.LLMRequest(full)
	m.instances[b.ID].logger.Info(logs.SYSTEM, "only-b-log")
	w = rpc(t, m, a.ID, "LogService/ListLogs", "{}", false)
	if !strings.Contains(w.Body.String(), full) || strings.Contains(w.Body.String(), "only-b-log") {
		t.Fatal("log isolation or full content broken", w.Body.String())
	}
	w = rpc(t, m, a.ID, "LogService/ListLogs", "{}", true)
	if strings.Contains(w.Body.String(), "only-b-log") {
		t.Fatal("General leaked peer logs")
	}
	w = rpc(t, m, "", "LogService/ListLogs", "{}", false)
	if strings.Contains(w.Body.String(), full) {
		t.Fatal("instance logs reached General")
	}
	for _, path := range []string{"/instances/" + b.ID + "/api/sticker/missing/image", "/instances/" + b.ID + "/api/log-images/missing"} {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
	}
}
func configureRouter(t *testing.T, m *Manager, id, url string) {
	t.Helper()
	r := m.instances[id].runtime.Engine.ModelRouter
	cfg := r.Draft()
	eid := "endpoint_test_" + id
	cfg.Endpoints = []modelrouter.Endpoint{{ID: eid, DisplayName: "mock", BaseURL: url, Enabled: true, APIKeySource: modelrouter.APIKeyStorageManual, APIKeyRef: "endpoint/" + eid}}
	cfg.Models = []modelrouter.Model{{ID: "model_mock", DisplayName: "mock", EndpointID: eid, UpstreamModel: "mock", Enabled: true}}
	cfg.GlobalBindings[modelrouter.WorkloadDialogue] = modelrouter.Binding{Mode: modelrouter.BindingModel, ModelID: "model_mock"}
	if err := r.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetDraftEndpointSecret(eid, "synthetic-test-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Publish(); err != nil {
		t.Fatal(err)
	}
}
func TestCopyPublishedOnlyAndDeleteOptions(t *testing.T) {
	m := testManager(t)
	src := create(t, m, "source")
	dst := create(t, m, "target")
	configureRouter(t, m, src.ID, "http://127.0.0.1:1")
	source := m.instances[src.ID]
	target := m.instances[dst.ID]
	if err := target.mcp.AddServer(context.Background(), mcp.ServerConfig{
		ID:      "target-only",
		Name:    "Target Only",
		Enabled: false,
		Transport: mcp.TransportConfig{
			Type:    mcp.TransportStdio,
			Command: "synthetic",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := source.config.Replace("# copied raw settings\nBOT_NAME=source\nBILLING_ENABLED=false\n"); err != nil {
		t.Fatal(err)
	}
	// The source draft is deliberately different from the published configuration.
	draft := source.runtime.Engine.ModelRouter.Draft()
	draft.Endpoints[0].DisplayName = "unpublished"
	if err := source.runtime.Engine.ModelRouter.SaveDraft(draft); err != nil {
		t.Fatal(err)
	}
	brain := []byte(`{"entries":[]}`)
	brainPath := filepath.Join(m.dir(dst.ID), "brain.json")
	if err := os.WriteFile(brainPath, brain, 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(dst.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy(dst.ID, src.ID); err == nil {
		t.Fatal("active target overwritten")
	}
	if err := m.Enable(dst.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy(dst.ID, src.ID); err != nil {
		t.Fatal(err)
	}
	copied := target.runtime.Engine.ModelRouter.Active()
	original := source.runtime.Engine.ModelRouter.Active()
	if copied.Endpoints[0].ID == original.Endpoints[0].ID || copied.Endpoints[0].DisplayName == "unpublished" {
		t.Fatal("copied draft or shared endpoint ID")
	}
	if copied.Models[0].EndpointID != copied.Endpoints[0].ID {
		t.Fatal("model reference not remapped")
	}
	raw, err := os.ReadFile(filepath.Join(m.dir(dst.ID), "model_router_secrets.json"))
	if err != nil || !strings.Contains(string(raw), "synthetic-test-secret") {
		t.Fatal("manual secret not copied", err)
	}
	saved, _ := os.ReadFile(brainPath)
	if string(saved) != string(brain) {
		t.Fatal("memory changed")
	}
	if _, ok := target.mcp.GetServer("target-only"); !ok {
		t.Fatal("quick config overwrote target MCP configuration")
	}
	rawEnv, _ := target.config.Raw()
	if !strings.HasPrefix(rawEnv, "# copied raw settings") {
		t.Fatal("raw env changed")
	}
	if err := m.Delete(dst.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(brainPath); err != nil {
		t.Fatal("retained memory missing", err)
	}
	for _, name := range []string{".env", "model_router.json", "model_router_secrets.json", "mcp_servers.json"} {
		if _, err := os.Stat(filepath.Join(m.dir(dst.ID), name)); !os.IsNotExist(err) {
			t.Fatal("configuration retained", name, err)
		}
	}
	if err := m.Delete(src.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.dir(src.ID)); !os.IsNotExist(err) {
		t.Fatal("all data not deleted")
	}
}
func TestLifecycleBusyBackendAndAutostart(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "busy")
	i := m.instances[info.ID]
	i.op.Lock()
	if err := m.Enable(info.ID, true); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	w := rpc(t, m, info.ID, "SettingsService/UpdateEnvVar", `{"key":"BOT_NAME","value":"bad"}`, false)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	i.op.Unlock()
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	list, _ := m.List()
	if !list[0].Enabled {
		t.Fatal("zero connections must still be green")
	}
	if w = rpc(t, m, info.ID, "SettingsService/UpdateEnvVar", `{"key":"ENABLE_ONEBOT_ADAPTER","value":"false"}`, false); w.Code != 200 {
		t.Fatal(w.Body)
	}
	list, _ = m.List()
	if !list[0].RestartRequired {
		t.Fatal("restart warning missing")
	}
	m.Close()
	next, err := New(m.root, m.global, m.sharedPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	list, _ = next.List()
	if !list[0].Enabled {
		t.Fatal("enabled state not restored")
	}
}

func TestInstanceRestartReloadsOnlyItsEnvFromDisk(t *testing.T) {
	m := testManager(t)
	a := create(t, m, "a")
	b := create(t, m, "b")
	if err := m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(b.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.dir(a.ID), ".env"), []byte("BOT_NAME=disk-a\nENABLE_ONEBOT_ADAPTER=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := m.instances[a.ID].runtime.Scope.Getenv("BOT_NAME"); got != "disk-a" {
		t.Fatalf("A used stale BOT_NAME %q", got)
	}
	if got := m.instances[a.ID].runtime.Scope.Getenv("ENABLE_ONEBOT_ADAPTER"); got != "false" {
		t.Fatalf("A used stale restart setting %q", got)
	}
	if got := m.instances[b.ID].runtime.Scope.Getenv("BOT_NAME"); got == "disk-a" {
		t.Fatal("A's disk reload leaked into B")
	}
}

func TestEnableRejectsMalformedFreshEnvWithoutStaleValues(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "malformed")
	if err := m.instances[info.ID].config.Replace("BOT_NAME=old-value\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.dir(info.ID), ".env"), []byte("BOT_NAME=\"unterminated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(info.ID, true); err == nil {
		t.Fatal("malformed fresh .env was enabled")
	}
	items, _ := m.List()
	if items[0].Enabled || items[0].Error == "" {
		t.Fatalf("malformed instance state = %+v", items[0])
	}
	managed := m.instances[info.ID]
	if managed.config == nil || managed.config.Error() == nil {
		t.Fatal("fresh parse error was not retained for repair")
	}
	if got := managed.config.Get("BOT_NAME"); got != "" {
		t.Fatalf("stale BOT_NAME survived malformed reload: %q", got)
	}
	if managed.runtime != nil && managed.runtime.Scope.Context().Err() == nil {
		t.Fatal("malformed instance kept an active runtime")
	}
}

func stageTestCopy(t *testing.T, m *Manager, target, source string) copyTransaction {
	t.Helper()
	raw, err := m.instances[source].config.Raw()
	if err != nil {
		t.Fatal(err)
	}
	cfg, secrets, copied, err := m.instances[source].runtime.Engine.ModelRouter.CopyPublished(func([]modelrouter.Endpoint) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	removed, err := m.instances[target].runtime.Engine.ModelRouter.DeleteCredentials()
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newCopyTransaction(target, copied, removed)
	if err != nil {
		t.Fatal(err)
	}
	dir := m.dir(target)
	if _, err = writeCopyTransaction(dir, transaction); err != nil {
		t.Fatal(err)
	}
	stageDir := filepath.Join(dir, transaction.Stage)
	if err = os.Mkdir(stageDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{".env": []byte(raw), "model_router.json": cfg, "model_router_secrets.json": secrets} {
		if _, err = writeCopyFile(filepath.Join(stageDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = modelrouter.StageCredentialPromotions(transaction.Credentials, copied); err != nil {
		t.Fatal(err)
	}
	return transaction
}

func TestCopyTransactionRecoveryNeverExposesMixedConfiguration(t *testing.T) {
	t.Run("preparing rolls back to old configuration", func(t *testing.T) {
		m := testManager(t)
		source := create(t, m, "source")
		target := create(t, m, "target")
		configureRouter(t, m, source.ID, "http://127.0.0.1:1")
		if err := m.instances[source.ID].config.Replace("BOT_NAME=new-generation\n"); err != nil {
			t.Fatal(err)
		}
		if err := m.instances[target.ID].config.Replace("BOT_NAME=old-generation\n"); err != nil {
			t.Fatal(err)
		}
		transaction := stageTestCopy(t, m, target.ID, source.ID)
		m.Close()
		next, err := New(m.root, m.global, m.sharedPathForTest())
		if err != nil {
			t.Fatal(err)
		}
		defer next.Close()
		if got := next.instances[target.ID].config.Get("BOT_NAME"); got != "old-generation" {
			t.Fatalf("preparing transaction exposed %q", got)
		}
		if len(next.instances[target.ID].runtime.Engine.ModelRouter.Active().Endpoints) != 0 {
			t.Fatal("preparing transaction exposed the new router")
		}
		for _, path := range []string{transactionPath(next.dir(target.ID)), filepath.Join(next.dir(target.ID), transaction.Stage)} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("preparing residue remains at %s: %v", path, err)
			}
		}
	})

	t.Run("committing completes the new configuration", func(t *testing.T) {
		m := testManager(t)
		source := create(t, m, "source")
		target := create(t, m, "target")
		configureRouter(t, m, source.ID, "http://127.0.0.1:1")
		if err := m.instances[source.ID].config.Replace("BOT_NAME=new-generation\n"); err != nil {
			t.Fatal(err)
		}
		if err := m.instances[target.ID].config.Replace("BOT_NAME=old-generation\n"); err != nil {
			t.Fatal(err)
		}
		transaction := stageTestCopy(t, m, target.ID, source.ID)
		transaction.Phase = copyCommitting
		if _, err := writeCopyTransaction(m.dir(target.ID), transaction); err != nil {
			t.Fatal(err)
		}
		// Simulate a crash after only the first of three target files was replaced.
		stagedEnv, err := os.ReadFile(filepath.Join(m.dir(target.ID), transaction.Stage, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		if err = instanceconfig.WriteAtomic(filepath.Join(m.dir(target.ID), ".env"), stagedEnv, 0600); err != nil {
			t.Fatal(err)
		}
		m.Close()
		next, err := New(m.root, m.global, m.sharedPathForTest())
		if err != nil {
			t.Fatal(err)
		}
		defer next.Close()
		if got := next.instances[target.ID].config.Get("BOT_NAME"); got != "new-generation" {
			t.Fatalf("committing transaction did not expose the new env: %q", got)
		}
		active := next.instances[target.ID].runtime.Engine.ModelRouter.Active()
		if len(active.Endpoints) != 1 || len(active.Models) != 1 || active.Models[0].EndpointID != active.Endpoints[0].ID {
			t.Fatal("committing transaction exposed a mixed model configuration")
		}
		for _, path := range []string{transactionPath(next.dir(target.ID)), filepath.Join(next.dir(target.ID), transaction.Stage)} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("committing residue remains at %s: %v", path, err)
			}
		}
	})
}

func TestCopyKeepsRecoveryMaterialAfterCommittedJournalSyncFailure(t *testing.T) {
	m := testManager(t)
	source := create(t, m, "source")
	target := create(t, m, "target")
	if err := m.instances[source.ID].config.Replace("BOT_NAME=new-generation\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.instances[target.ID].config.Replace("BOT_NAME=old-generation\n"); err != nil {
		t.Fatal(err)
	}

	originalWrite := writeCopyFile
	injected := false
	writeCopyFile = func(path string, data []byte, mode os.FileMode) (bool, error) {
		if !injected && filepath.Base(path) == copyTransactionFile && strings.Contains(string(data), `"phase": "committing"`) {
			if err := instanceconfig.WriteAtomic(path, data, mode); err != nil {
				return false, err
			}
			injected = true
			return true, errors.New("synthetic directory sync failure")
		}
		return originalWrite(path, data, mode)
	}
	err := m.Copy(target.ID, source.ID)
	writeCopyFile = originalWrite
	if err == nil || !injected {
		t.Fatalf("copy error=%v injected=%v", err, injected)
	}
	transaction, readErr := readCopyTransaction(m.dir(target.ID), target.ID)
	if readErr != nil || transaction == nil || transaction.Phase != copyCommitting {
		t.Fatalf("committed recovery journal missing: transaction=%+v err=%v", transaction, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(m.dir(target.ID), transaction.Stage)); statErr != nil {
		t.Fatalf("copy recovery stage was removed: %v", statErr)
	}
	if m.instances[target.ID].runtime != nil {
		t.Fatal("target runtime exposed files while commit durability was uncertain")
	}
	oldEnv, readErr := os.ReadFile(filepath.Join(m.dir(target.ID), ".env"))
	if readErr != nil || !strings.Contains(string(oldEnv), "BOT_NAME=old-generation") {
		t.Fatalf("target files changed before durable commit: %q err=%v", oldEnv, readErr)
	}

	if err = m.Enable(target.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := m.instances[target.ID].config.Get("BOT_NAME"); got != "new-generation" {
		t.Fatalf("recovery did not complete new configuration: %q", got)
	}
	if _, statErr := os.Stat(transactionPath(m.dir(target.ID))); !os.IsNotExist(statErr) {
		t.Fatalf("recovered journal remains: %v", statErr)
	}
}

func TestCreateFailureRemovesOwnedDirectory(t *testing.T) {
	m := testManager(t)
	if err := os.Mkdir(filepath.Join(m.root, "instances.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create("must-fail"); err == nil {
		t.Fatal("create unexpectedly succeeded")
	}
	paths, err := filepath.Glob(filepath.Join(m.root, "instance_*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("failed creation left orphan paths: %v", paths)
	}
}
func (m *Manager) sharedPathForTest() string {
	return filepath.Join(filepath.Dir(m.root), "dialogue.yml")
}
func TestWebsocketSourceIsolationAndStop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"mock reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	m := testManager(t)
	a := create(t, m, "source")
	b := create(t, m, "peer")
	configureRouter(t, m, a.ID, upstream.URL)
	server := httptest.NewServer(m)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/instances/" + a.ID + "/ws/onebot"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil || resp.StatusCode != 503 {
		t.Fatal("stopped handshake", err)
	}
	if err = m.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = m.Enable(b.ID, true); err != nil {
		t.Fatal(err)
	}
	c1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err = c1.WriteJSON(map[string]any{"post_type": "message", "message_type": "private", "self_id": 2, "user_id": 1, "message_id": 1, "message": []map[string]any{{"type": "text", "data": map[string]string{"text": "unique-instance-source"}}}, "sender": map[string]any{"user_id": 1, "nickname": "mock"}}); err != nil {
		t.Fatal(err)
	}
	_ = c1.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := c1.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "mock reply") {
		t.Fatal(string(raw))
	}
	var action map[string]any
	if err = json.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	_ = c1.WriteJSON(map[string]any{"status": "ok", "retcode": 0, "echo": action["echo"], "data": map[string]int{"message_id": 2}})
	_ = c2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err = c2.ReadMessage(); err == nil {
		t.Fatal("reply broadcast to second source")
	}
	if m.instances[b.ID].runtime.Engine.SessionManager.Count() != 0 {
		t.Fatal("session leaked")
	}
	done := make(chan error, 1)
	go func() { done <- m.Enable(a.ID, false) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not finish")
	}
	if m.instances[a.ID].runtime.Engine.SessionManager.Count() != 0 || len(m.instances[a.ID].logger.Snapshot()) != 0 {
		t.Fatal("transient state survived stop")
	}
	if err = m.Delete(a.ID, true); err != nil {
		t.Fatal(err)
	}
	_, resp, err = websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil || resp.StatusCode != 404 {
		t.Fatal("deleted handshake", err)
	}
}

func TestCopyRollbackOnReloadFailure(t *testing.T) {
	m := testManager(t)
	src := create(t, m, "source")
	dst := create(t, m, "target")
	configureRouter(t, m, src.ID, "http://127.0.0.1:1")
	original, _ := m.instances[dst.ID].config.Raw()
	path := filepath.Join(m.dir(dst.ID), "group_summaries.json")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Copy(dst.ID, src.ID); err == nil {
		t.Fatal("copy succeeded despite invalid target storage")
	}
	raw, _ := os.ReadFile(filepath.Join(m.dir(dst.ID), ".env"))
	if string(raw) != original {
		t.Fatal("settings not rolled back")
	}
	if len(m.instances[dst.ID].runtime.Engine.ModelRouter.Active().Endpoints) != 0 {
		t.Fatal("live router changed after rollback")
	}
	if _, err := os.Stat(filepath.Join(m.dir(dst.ID), "model_router.json")); !os.IsNotExist(err) {
		t.Fatal("new model config was not rolled back", err)
	}
}
func TestDeleteCancelsInFlightLLMWithoutResurrection(t *testing.T) {
	entered := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer upstream.Close()
	m := testManager(t)
	info := create(t, m, "in-flight")
	configureRouter(t, m, info.ID, upstream.URL)
	if err := m.Enable(info.ID, true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m)
	defer server.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/instances/"+info.ID+"/ws/onebot", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.WriteJSON(map[string]any{"post_type": "message", "message_type": "private", "user_id": 1, "self_id": 2, "message_id": 1, "message": []map[string]any{{"type": "text", "data": map[string]string{"text": "wait-until-cancelled"}}}})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("LLM not reached")
	}
	done := make(chan error, 1)
	go func() { done <- m.Delete(info.ID, true) }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delete blocked on LLM")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream was not cancelled")
	}
	if _, err = os.Stat(m.dir(info.ID)); !os.IsNotExist(err) {
		t.Fatal("instance directory recreated", err)
	}
}
func TestMalformedInstanceDoesNotStopControlPlane(t *testing.T) {
	m := testManager(t)
	bad := create(t, m, "bad")
	good := create(t, m, "good")
	if err := os.WriteFile(filepath.Join(m.dir(bad.ID), ".env"), []byte("NOT VALID = \"unterminated"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Close()
	next, err := New(m.root, m.global, m.sharedPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err = next.Enable(good.ID, true); err != nil {
		t.Fatal("healthy instance blocked", err)
	}
	if err = next.Enable(bad.ID, true); err == nil {
		t.Fatal("bad instance enabled")
	}
	w := rpc(t, next, bad.ID, "SettingsService/UpdateRawEnvFile", `{"content":"BOT_NAME=repaired\n"}`, false)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = next.Enable(bad.ID, true); err != nil {
		t.Fatal("repaired instance failed", err)
	}
}

func TestWindowsCredentialCloneAndCleanupWithoutRuntime(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Credential Manager")
	}
	m := testManager(t)
	src := create(t, m, "source")
	dst := create(t, m, "target")
	r := m.instances[src.ID].runtime.Engine.ModelRouter
	endpointID := "endpoint_credential_" + src.ID
	target := modelrouter.CredentialTarget(endpointID)
	cfg := r.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: endpointID, DisplayName: "synthetic", BaseURL: "http://127.0.0.1:1", Enabled: true, APIKeySource: modelrouter.APIKeyStorageWindowsCredentialManager, APIKeyRef: target}}
	if err := r.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetDraftEndpointSecret(endpointID, "synthetic-credential-test-value"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Publish(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = modelrouter.ApplyCredentials([]modelrouter.CredentialChange{{Target: target}}) })
	if err := m.Copy(dst.ID, src.ID); err != nil {
		t.Fatal(err)
	}
	cloned := m.instances[dst.ID].runtime.Engine.ModelRouter.Active().Endpoints[0]
	t.Cleanup(func() {
		_, _ = modelrouter.ApplyCredentials([]modelrouter.CredentialChange{{Target: cloned.APIKeyRef}})
	})
	if cloned.ID == endpointID || cloned.APIKeyRef != modelrouter.CredentialTarget(cloned.ID) {
		t.Fatal("credential reference not remapped")
	}
	// Compare without exposing any credential value in test output.
	secrets, err := modelrouter.CredentialExists(cloned.ID)
	if err != nil || !secrets {
		t.Fatal("cloned credential missing", err)
	}
	if err = os.WriteFile(filepath.Join(m.dir(src.ID), "group_summaries.json"), []byte("invalid-json"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Close()
	restarted, err := New(m.root, m.global, m.sharedPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.instances[src.ID].runtime != nil {
		t.Fatal("expected failed runtime")
	}
	if err = restarted.Delete(src.ID, true); err != nil {
		t.Fatal(err)
	}
	exists, err := modelrouter.CredentialExists(endpointID)
	if err != nil || exists {
		t.Fatal("credential leaked without runtime", err)
	}
	if err = restarted.Delete(dst.ID, false); err != nil {
		t.Fatal(err)
	}
	exists, err = modelrouter.CredentialExists(cloned.ID)
	if err != nil || exists {
		t.Fatal("cloned credential leaked", err)
	}
}

func TestWindowsCredentialCopyTransactionRecovery(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Credential Manager")
	}
	m := testManager(t)
	source := create(t, m, "source")
	target := create(t, m, "target")
	router := m.instances[source.ID].runtime.Engine.ModelRouter
	endpointID := "endpoint_recovery_" + source.ID
	sourceTarget := modelrouter.CredentialTarget(endpointID)
	cfg := router.Draft()
	cfg.Endpoints = []modelrouter.Endpoint{{ID: endpointID, DisplayName: "synthetic", BaseURL: "http://127.0.0.1:1", Enabled: true, APIKeySource: modelrouter.APIKeyStorageWindowsCredentialManager, APIKeyRef: sourceTarget}}
	if err := router.SaveDraft(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := router.SetDraftEndpointSecret(endpointID, "synthetic-recovery-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Publish(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = modelrouter.ApplyCredentials([]modelrouter.CredentialChange{{Target: sourceTarget}}) })

	transaction := stageTestCopy(t, m, target.ID, source.ID)
	transaction.Phase = copyCommitting
	if _, err := writeCopyTransaction(m.dir(target.ID), transaction); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(transactionPath(m.dir(target.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "synthetic-recovery-secret") {
		t.Fatal("copy transaction manifest persisted credential material")
	}
	m.Close()
	next, err := New(m.root, m.global, m.sharedPathForTest())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	active := next.instances[target.ID].runtime.Engine.ModelRouter.Active()
	if len(active.Endpoints) != 1 {
		t.Fatal("recovered endpoint missing")
	}
	cloned := active.Endpoints[0]
	t.Cleanup(func() {
		_, _ = modelrouter.ApplyCredentials([]modelrouter.CredentialChange{{Target: cloned.APIKeyRef}})
	})
	exists, err := modelrouter.CredentialExists(cloned.ID)
	if err != nil || !exists {
		t.Fatal("recovered credential missing", err)
	}
	if _, err := os.Stat(transactionPath(next.dir(target.ID))); !os.IsNotExist(err) {
		t.Fatal("recovered credential transaction was not cleaned", err)
	}
}

func tombstoneInstanceForTest(t *testing.T, m *Manager, id string) {
	t.Helper()
	if err := m.update(id, func(info *Info) {
		info.Enabled = false
		info.Deleting = true
	}); err != nil {
		t.Fatal(err)
	}
	item := m.instances[id]
	item.mu.Lock()
	runtime := item.runtime
	item.runtime = nil
	item.mu.Unlock()
	if runtime != nil {
		runtime.Stop()
	}
}

func TestDeletingTombstoneRejectsLifecycleAndCopyOperations(t *testing.T) {
	t.Run("target", func(t *testing.T) {
		m := testManager(t)
		source := create(t, m, "source")
		target := create(t, m, "target")
		tombstoneInstanceForTest(t, m, target.ID)
		if err := os.RemoveAll(m.dir(target.ID)); err != nil {
			t.Fatal(err)
		}

		for name, err := range map[string]error{
			"rename":  m.Rename(target.ID, "revived"),
			"enable":  m.Enable(target.ID, true),
			"disable": m.Enable(target.ID, false),
			"copy":    m.Copy(target.ID, source.ID),
		} {
			if !errors.Is(err, ErrDeleting) {
				t.Fatalf("%s error = %v, want ErrDeleting", name, err)
			}
		}
		if m.instances[target.ID].runtime != nil {
			t.Fatal("deleting target runtime was recreated")
		}
		if _, err := os.Stat(m.dir(target.ID)); !os.IsNotExist(err) {
			t.Fatalf("deleting target directory was recreated: %v", err)
		}
		if err := m.Delete(target.ID, true); err != nil {
			t.Fatalf("retry delete was rejected: %v", err)
		}
	})

	t.Run("source", func(t *testing.T) {
		m := testManager(t)
		source := create(t, m, "source")
		target := create(t, m, "target")
		before, err := m.instances[target.ID].config.Raw()
		if err != nil {
			t.Fatal(err)
		}
		tombstoneInstanceForTest(t, m, source.ID)
		if err = os.RemoveAll(m.dir(source.ID)); err != nil {
			t.Fatal(err)
		}

		if err = m.Copy(target.ID, source.ID); !errors.Is(err, ErrDeleting) {
			t.Fatalf("copy source error = %v, want ErrDeleting", err)
		}
		if m.instances[source.ID].runtime != nil {
			t.Fatal("deleting source runtime was recreated")
		}
		after, err := m.instances[target.ID].config.Raw()
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatal("target settings changed after rejecting deleting source")
		}
		if _, err = os.Stat(m.dir(source.ID)); !os.IsNotExist(err) {
			t.Fatalf("deleting source directory was recreated: %v", err)
		}
	})
}

func TestControlPlaneLogsRemainAvailableForDeletingInstance(t *testing.T) {
	logs.General.Clear()
	t.Cleanup(logs.General.Clear)
	m := testManager(t)
	info := create(t, m, "deleting")
	tombstoneInstanceForTest(t, m, info.ID)
	logs.General.Info(logs.HTTP, "delete-failure-diagnostic")

	w := rpc(t, m, "", "LogService/ListLogs", "{}", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "delete-failure-diagnostic") {
		t.Fatalf("root Control Plane logs unavailable: code=%d body=%s", w.Code, w.Body.String())
	}
	w = rpc(t, m, "", "LogService/ClearLogs", "{}", true)
	if w.Code != http.StatusOK || len(logs.General.Snapshot()) != 0 {
		t.Fatalf("root Control Plane clear unavailable: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCloseEndsControlPlaneAndInstanceLogStreams(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "streaming")
	_, generalStream := logs.General.Subscribe(nil)
	_, instanceStream := m.instances[info.ID].logger.Subscribe(nil)
	handlerEntered := make(chan struct{})
	handlerDone := make(chan struct{})
	m.general = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(handlerEntered)
		<-r.Context().Done()
		close(handlerDone)
	})
	requestDone := make(chan struct{})
	go func() {
		m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/frostagent.v1.LogService/StreamLogs", strings.NewReader("{}")))
		close(requestDone)
	}()
	select {
	case <-handlerEntered:
	case <-time.After(time.Second):
		t.Fatal("Control Plane stream handler did not start")
	}

	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager close did not complete promptly")
	}
	for name, completed := range map[string]<-chan struct{}{
		"Control Plane handler": handlerDone,
		"Control Plane request": requestDone,
	} {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatalf("%s was not cancelled", name)
		}
	}
	for name, stream := range map[string]<-chan logs.LogEntry{
		"Control Plane": generalStream,
		"instance":      instanceStream,
	} {
		select {
		case _, ok := <-stream:
			if ok {
				t.Fatalf("%s log stream remained open", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s log stream was not ended", name)
		}
	}
}

type gatedRequestBody struct {
	reader  *strings.Reader
	entered chan struct{}
	release chan struct{}
}

func newGatedRequestBody(content string) *gatedRequestBody {
	return &gatedRequestBody{
		reader:  strings.NewReader(content),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *gatedRequestBody) Read(p []byte) (int, error) {
	select {
	case <-b.entered:
	default:
		close(b.entered)
	}
	<-b.release
	return b.reader.Read(p)
}

func (*gatedRequestBody) Close() error { return nil }

func TestCloseRejectsInstanceMCPMutationStillReadingBody(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "mcp-close-gate")
	body := newGatedRequestBody(`{"id":"late","name":"Late","enabled":false,"transportType":"stdio","command":"synthetic"}`)
	req := httptest.NewRequest(
		http.MethodPost,
		"/instances/"+info.ID+"/frostagent.v1.MCPService/AddMCPServer",
		nil,
	)
	req.Body = body
	req.ContentLength = int64(body.reader.Len())
	req.Header.Set("Content-Type", "application/json")
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, req)
		response <- w
	}()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("MCP request did not begin reading its body")
	}

	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		err := m.instances[info.ID].mcp.SetRuntimeActive(false)
		if errors.Is(err, mcp.ErrManagerClosed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("instance MCP manager did not close before request admission resumed")
		}
		time.Sleep(time.Millisecond)
	}

	close(body.release)
	select {
	case <-response:
	case <-time.After(time.Second):
		t.Fatal("MCP request did not finish after body release")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Control Plane close did not finish")
	}
	if _, ok := m.instances[info.ID].mcp.GetServer("late"); ok {
		t.Fatal("MCP request mutated a closed instance manager")
	}
	if _, err := os.Stat(filepath.Join(m.dir(info.ID), "mcp_servers.json")); !os.IsNotExist(err) {
		t.Fatalf("closed MCP request created a config file: %v", err)
	}
}

func TestCloseRejectsLifecycleRequestsStillReadingBodies(t *testing.T) {
	t.Run("enable", func(t *testing.T) {
		m := testManager(t)
		info := create(t, m, "stays-stopped")
		originalRuntime := m.instances[info.ID].runtime
		body := newGatedRequestBody(`{"enabled":true}`)
		req := httptest.NewRequest(http.MethodPost, "/api/instances/"+info.ID+"/enable", nil)
		req.Body = body
		req.ContentLength = int64(body.reader.Len())
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := httptest.NewRecorder()
			m.ServeHTTP(w, req)
			response <- w
		}()
		select {
		case <-body.entered:
		case <-time.After(time.Second):
			t.Fatal("enable request did not begin reading its body")
		}
		m.Close()
		close(body.release)
		select {
		case w := <-response:
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("enable after close status = %d, body=%s", w.Code, w.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("enable request did not finish after body release")
		}
		managed := m.instances[info.ID]
		if managed.runtime != originalRuntime || managed.runtime.Scope.Context().Err() == nil {
			t.Fatal("in-flight enable revived an instance after close")
		}
		items, _ := m.List()
		if len(items) != 1 || items[0].Enabled {
			t.Fatalf("instance enabled after close: %+v", items)
		}
	})

	t.Run("create", func(t *testing.T) {
		m := testManager(t)
		body := newGatedRequestBody(`{"name":"late-instance"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/instances", nil)
		req.Body = body
		req.ContentLength = int64(body.reader.Len())
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			w := httptest.NewRecorder()
			m.ServeHTTP(w, req)
			response <- w
		}()
		select {
		case <-body.entered:
		case <-time.After(time.Second):
			t.Fatal("create request did not begin reading its body")
		}
		m.Close()
		close(body.release)
		select {
		case w := <-response:
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("create after close status = %d, body=%s", w.Code, w.Body.String())
			}
		case <-time.After(time.Second):
			t.Fatal("create request did not finish after body release")
		}
		items, _ := m.List()
		if len(items) != 0 {
			t.Fatalf("instance created after close: %+v", items)
		}
		paths, err := filepath.Glob(filepath.Join(m.root, "instance_*"))
		if err != nil || len(paths) != 0 {
			t.Fatalf("instance directory created after close: paths=%v err=%v", paths, err)
		}
	})
}

func TestLifecycleRejectsNonCanonicalIdentifiers(t *testing.T) {
	m := testManager(t)
	info := create(t, m, "safe")
	for _, id := range []string{"../" + info.ID, info.ID + "/../../outside", info.ID + "G", info.ID + "\x00"} {
		if !errors.Is(m.Rename(id, "bad"), os.ErrNotExist) {
			t.Fatal("invalid rename ID accepted")
		}
		if !errors.Is(m.Enable(id, true), os.ErrNotExist) {
			t.Fatal("invalid enable ID accepted")
		}
		if !errors.Is(m.Delete(id, true), os.ErrNotExist) {
			t.Fatal("invalid delete ID accepted")
		}
		if !errors.Is(m.Copy(info.ID, id), os.ErrNotExist) {
			t.Fatal("invalid copy source accepted")
		}
		if !errors.Is(m.Copy(id, info.ID), os.ErrNotExist) {
			t.Fatal("invalid copy target accepted")
		}
	}
	if _, err := os.Stat(m.dir(info.ID)); err != nil {
		t.Fatal("valid instance affected", err)
	}
}
