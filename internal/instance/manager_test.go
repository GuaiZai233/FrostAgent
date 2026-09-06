package instance

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/modelrouter"
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
	"testing"
	"time"
)

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
func rpc(t *testing.T, m *Manager, id, method, body string, includeGeneral bool) *httptest.ResponseRecorder {
	t.Helper()
	prefix := ""
	if id != "" {
		prefix = "/instances/" + id
	}
	req := httptest.NewRequest("POST", prefix+"/frostagent.v1."+method, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if includeGeneral {
		req.Header.Set("X-FrostAgent-General", "true")
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
	for _, name := range []string{".env", "model_router.json", "model_router_secrets.json"} {
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
