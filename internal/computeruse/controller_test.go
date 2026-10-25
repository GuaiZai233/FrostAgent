package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/computeruse/bridge"
	"FrostAgent/internal/core"
	"FrostAgent/internal/storage"
)

type fakeTransport struct {
	mu          sync.Mutex
	calls       []bridge.Request
	ops         map[string]bridge.Operation
	content     map[string]bridge.Content
	run         func(context.Context, bridge.Request) (bridge.Operation, error)
	readContent func(context.Context, bridge.Request)
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{ops: map[string]bridge.Operation{}, content: map[string]bridge.Content{}}
}
func (f *fakeTransport) Run(ctx context.Context, r bridge.Request) (bridge.Operation, error) {
	f.mu.Lock()
	f.calls = append(f.calls, r)
	f.mu.Unlock()
	hash, _ := r.Fingerprint()
	op := bridge.Operation{Identity: r.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Succeeded, Code: "completed"}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if f.run != nil {
		var err error
		op, err = f.run(ctx, r)
		if err != nil {
			return op, err
		}
	}
	text := "synthetic private tool output"
	switch r.Kind {
	case "task.prepare":
		data, _ := json.Marshal(map[string]any{"task_id": r.TaskID, "project_id": "synthetic-project", "generation": r.Generation, "path": "synthetic-worktree", "branch": "synthetic-branch"})
		text = string(data)
	case "task.pause":
		text = `{"gui_stopped":true,"running_operations":0}`
	case "desktop.takeover":
		text = `{"gui_stopped":true,"desktop_paused":true}`
	case "desktop.resume":
		text = `{"gui_stopped":false,"desktop_paused":false}`
	}
	f.mu.Lock()
	f.ops[r.OperationID] = op
	f.content[r.OperationID] = bridge.Content{Identity: r.Identity, RequestHash: hash, Text: text, ExpiresAt: time.Now().Add(time.Minute)}
	f.mu.Unlock()
	return op, nil
}
func (f *fakeTransport) Lookup(_ context.Context, i bridge.Identity, hash string) (bridge.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, ok := f.ops[i.OperationID]
	if !ok {
		return op, bridge.ErrNotFound
	}
	if op.Identity != i || op.RequestHash != hash {
		return op, bridge.ErrProtocol
	}
	return op, nil
}
func (f *fakeTransport) Content(ctx context.Context, r bridge.Request) (bridge.Content, error) {
	if f.readContent != nil {
		f.readContent(ctx, r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.content[r.OperationID], nil
}

type modelFunc func(context.Context, core.ChatRequest) (*core.ChatResponse, error)

func (f modelFunc) Chat(ctx context.Context, r core.ChatRequest) (*core.ChatResponse, error) {
	return f(ctx, r)
}
func fixture(t *testing.T, provider core.LLMProvider) (*Controller, *fakeTransport, *atomic.Bool) {
	t.Helper()
	root := t.TempDir()
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES('synthetic-instance','Synthetic', 'synthetic-date')`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := storage.DefaultComputerUseConfig()
	cfg.Enabled = true
	cfg.BridgeURL = "https://127.0.0.1:9443"
	cfg.Token = "synthetic-bridge-token-long-enough-for-test"
	cfg.AdapterToken = "synthetic-adapter-token-long-enough-for-test"
	if err = db.SaveComputerUseConfig(context.Background(), "synthetic-instance", cfg); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveComputerUseProject(context.Background(), "synthetic-instance", storage.ComputerUseProject{ID: "synthetic-project", Path: "synthetic-path", Baseline: "main", Shell: "synthetic-shell"}); err != nil {
		t.Fatal(err)
	}
	owner := &atomic.Bool{}
	owner.Store(true)
	c, err := New(Options{DB: db, InstanceID: "synthetic-instance", AuditDir: filepath.Join(root, "audit"), Provider: provider, OwnerAllowed: func(id string) bool { return owner.Load() && id == "synthetic-owner" }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	fake := newFakeTransport()
	c.newTransport = func(storage.ComputerUseConfig) (transport, error) { return fake, nil }
	return c, fake, owner
}
func awaitTask(t *testing.T, c *Controller, id string) storage.ComputerUseTask {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		c.mu.Lock()
		task := cloneTask(c.tasks[id])
		_, running := c.runs[id]
		c.mu.Unlock()
		if !running && task.State != "running" {
			return task
		}
		select {
		case <-deadline:
			t.Fatalf("task never stopped: %+v", task)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func TestStaleDesktopTakeoverCannotConfirmAfterResume(t *testing.T) {
	c, transport, _ := fixture(t, modelFunc(completedModel))
	entered, release := make(chan struct{}), make(chan struct{})
	transport.readContent = func(_ context.Context, r bridge.Request) {
		if r.Kind == "desktop.takeover" {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := c.desktopControl(context.Background(), c.managementActor(), true); done <- err }()
	<-entered
	_, err := c.desktopControl(context.Background(), c.managementActor(), false)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrState) {
		t.Fatalf("stale takeover falsely confirmed: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.desktopPaused || !c.desktopConfirmed {
		t.Fatal("late takeover overwrote the later unlock")
	}
}

func TestStaleProjectSnapshotCannotOverwriteTakeover(t *testing.T) {
	c, transport, _ := fixture(t, modelFunc(completedModel))
	if c.desktopConfirmed {
		t.Fatal("startup claimed to know the live desktop")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	transport.readContent = func(_ context.Context, r bridge.Request) {
		if r.Kind == "project.list" {
			transport.mu.Lock()
			content := transport.content[r.OperationID]
			content.Text = `{"projects":[],"workspaces":[],"desktop_paused":false,"desktop_generation":0}`
			transport.content[r.OperationID] = content
			transport.mu.Unlock()
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := c.syncProjects(context.Background(), c.managementActor()); done <- err }()
	<-entered
	_, err := c.desktopControl(context.Background(), c.managementActor(), true)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.desktopPaused || !c.desktopConfirmed || c.desktopGeneration != 1 {
		t.Fatal("stale snapshot overwrote newer confirmed takeover")
	}
}

func TestPausedManualControlCannotBeCompletedByLateContent(t *testing.T) {
	c, transport, _ := fixture(t, modelFunc(completedModel))
	entered, release := make(chan string, 1), make(chan struct{})
	transport.readContent = func(_ context.Context, r bridge.Request) {
		if r.Kind == "project.list" {
			entered <- r.TaskID
			<-release
		}
	}
	done := make(chan error, 1)
	go func() { _, err := c.syncProjects(context.Background(), c.managementActor()); done <- err }()
	id := <-entered
	_, err := c.pause(context.Background(), id, "synthetic-pause")
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrState) {
		t.Fatalf("late manual result advanced a paused task: %v", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tasks[id].State != "paused" || c.tasks[id].Code != "synthetic-pause" {
		t.Fatal("manual completion replaced newer paused state")
	}
}

func TestCloseCancelsAndWaitsForManualControl(t *testing.T) {
	c, transport, _ := fixture(t, modelFunc(completedModel))
	entered := make(chan struct{})
	transport.run = func(ctx context.Context, r bridge.Request) (bridge.Operation, error) {
		close(entered)
		<-ctx.Done()
		return bridge.Operation{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { _, err := c.syncProjects(context.Background(), c.managementActor()); done <- err }()
	<-entered
	c.Close()
	if err := <-done; err == nil {
		t.Fatal("cancelled control reported success")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.controls) != 0 {
		t.Fatal("manual operation survived Close")
	}
}

func completedModel(_ context.Context, r core.ChatRequest) (*core.ChatResponse, error) {
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "synthetic private model result"}}, nil
}
func TestIsolatedRunPersistsMetadataNotSourceOrOutput(t *testing.T) {
	var rounds atomic.Int32
	c, transport, _ := fixture(t, modelFunc(func(ctx context.Context, r core.ChatRequest) (*core.ChatResponse, error) {
		if !r.Sensitive {
			t.Error("privileged request may enter ordinary logs")
		}
		if len(r.Tools) != 1 || r.Tools[0].Name != "computer_execute" {
			t.Error("ordinary registry leaked")
		}
		if rounds.Add(1) == 1 {
			return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: "computer_execute", Arguments: `{"kind":"terminal.exec","parameters":{"command":"synthetic-private-command"}}`}}}}}, nil
		}
		return completedModel(ctx, r)
	}))
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic-private-instruction")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitTask(t, c, task.ID)
	if result.State != "completed" || !result.GUIStopped || !result.Prepared {
		t.Fatalf("unexpected task: %+v", result)
	}
	transport.mu.Lock()
	count := len(transport.calls)
	transport.mu.Unlock()
	if count != 4 {
		t.Fatalf("calls=%d", count)
	}
	tasks, err := c.opts.DB.ComputerUseTasks(context.Background(), c.opts.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tasks)
	for _, secret := range []string{"synthetic-private-command", "synthetic-private-instruction", "synthetic private tool output", "synthetic private model result"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("raw task content persisted")
		}
		files, _ := os.ReadDir(c.opts.AuditDir)
		for _, file := range files {
			data, _ := os.ReadFile(filepath.Join(c.opts.AuditDir, file.Name()))
			if strings.Contains(string(data), secret) {
				t.Fatal("audit leaked raw content")
			}
		}
	}
}
func TestForbiddenIngressCannotCallBridge(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(completedModel))
	for _, input := range []core.ComputerUseIngress{
		{Platform: "qq", UserID: "synthetic-owner", MessageType: "group"},
		{Platform: "qq", UserID: "synthetic-owner", MessageType: "private", Mock: true},
		{Platform: "qq", UserID: "synthetic-owner", MessageType: "private", Proactive: true},
		{Platform: "other", UserID: "synthetic-owner", MessageType: "private"},
		{Platform: "qq", UserID: "synthetic-stranger", MessageType: "private"},
	} {
		input.TransportProof = c.AuthenticateTransport("Bearer " + c.config.AdapterToken)
		input.Text = "/computer start synthetic-project synthetic"
		handled, reply := c.HandleCommand(context.Background(), input)
		if !handled || reply != "" {
			t.Fatal("forbidden ingress handled incorrectly")
		}
	}
	if len(fake.calls) != 0 {
		t.Fatal("forbidden ingress reached VM")
	}
	req := httptest.NewRequest("POST", "/api/computer-use/tasks", strings.NewReader(`{"project_id":"synthetic-project","instruction":"synthetic"}`))
	req.RemoteAddr = "192.0.2.50:1234"
	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated remote admitted")
	}
}

func TestPrivateCommandNeedsCurrentTransportProof(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(completedModel))
	input := core.ComputerUseIngress{Platform: "qq", UserID: "synthetic-owner", MessageType: "private", Text: "/computer status"}
	if _, reply := c.HandleCommand(context.Background(), input); reply != "" {
		t.Fatal("forged event owner accepted without adapter credential")
	}
	if proof := c.AuthenticateTransport("Bearer invalid"); proof != "" {
		t.Fatal("invalid handshake authenticated")
	}
	input.TransportProof = c.AuthenticateTransport("Bearer " + c.config.AdapterToken)
	if _, reply := c.HandleCommand(context.Background(), input); reply == "" {
		t.Fatal("trusted owner command rejected")
	}
	c.config.AdapterToken = "synthetic-revoked-token-long-enough-for-test"
	if _, reply := c.HandleCommand(context.Background(), input); reply != "" {
		t.Fatal("stale adapter proof accepted")
	}
	if len(fake.calls) != 0 {
		t.Fatal("status unexpectedly called bridge")
	}
}

func TestDisabledConfigurationImmediatelyStopsNewOperations(t *testing.T) {
	started := make(chan struct{})
	c, _, _ := fixture(t, modelFunc(func(ctx context.Context, r core.ChatRequest) (*core.ChatResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	c.mu.Lock()
	cfg := c.config
	c.mu.Unlock()
	cfg.Enabled = false
	if err := c.setConfig(cfg); err != nil {
		t.Fatal(err)
	}
	_ = awaitTask(t, c, task.ID)
	if _, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled feature admitted work")
	}
}
func TestOwnerRevocationPausesInFlightModel(t *testing.T) {
	started := make(chan struct{})
	c, _, owner := fixture(t, modelFunc(func(ctx context.Context, r core.ChatRequest) (*core.ChatResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	owner.Store(false)
	result := awaitTask(t, c, task.ID)
	if result.State != "paused" || result.Code != "owner_revoked" || !result.GUIStopped {
		t.Fatalf("revocation failed: %+v", result)
	}
}
func TestUnknownOperationIsNotReplacedAndRequiresAcknowledgement(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(completedModel))
	fake.run = func(ctx context.Context, r bridge.Request) (bridge.Operation, error) {
		if r.Kind == "task.prepare" {
			return bridge.Operation{}, bridge.ErrOutcomeUnknown
		}
		hash, _ := r.Fingerprint()
		return bridge.Operation{Identity: r.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Succeeded, Code: "completed"}}, nil
	}
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitTask(t, c, task.ID)
	if !result.ReconcileRequired || result.Pending.State != "unknown" {
		t.Fatalf("unknown outcome lost: %+v", result)
	}
	if _, err := c.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "continue", false); err == nil {
		t.Fatal("unknown auto-resumed")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	count := 0
	for _, r := range fake.calls {
		if r.Kind == "task.prepare" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("unknown operation was replayed")
	}
}
func TestLateFactCannotAdvancePausedTask(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(completedModel))
	started, release := make(chan struct{}), make(chan struct{})
	fake.run = func(ctx context.Context, r bridge.Request) (bridge.Operation, error) {
		if r.Kind == "task.prepare" {
			close(started)
			<-release
		}
		hash, _ := r.Fingerprint()
		return bridge.Operation{Identity: r.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Succeeded, Code: "completed"}}, nil
	}
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	paused, err := c.pause(context.Background(), task.ID, "owner_paused")
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	result := awaitTask(t, c, task.ID)
	if result.State != "paused" || result.Generation != paused.Generation || result.LastOperation.State != "succeeded" {
		t.Fatalf("late fact crossed generation or was lost: %+v", result)
	}
}
func TestRestartLoadsWithoutContactingBridge(t *testing.T) {
	c, _, _ := fixture(t, modelFunc(completedModel))
	timestamp := now()
	task := storage.ComputerUseTask{ID: "synthetic-old", ProjectID: "synthetic-project", ActorID: "synthetic-owner", ActorKind: "qq_private", BridgeURL: c.config.BridgeURL, Generation: 7, State: "running", CreatedAt: timestamp, UpdatedAt: timestamp}
	if err := c.opts.DB.SaveComputerUseTask(context.Background(), c.opts.InstanceID, task); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(c.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.newTransport = func(storage.ComputerUseConfig) (transport, error) {
		t.Error("restart contacted bridge")
		return nil, errors.New("unexpected")
	}
	state := reopened.tasks[task.ID]
	if state.State != "paused" || state.GUIStopped || !state.ReconcileRequired {
		t.Fatalf("bad recovery: %+v", state)
	}
}
func TestToolBudgetStopsBeforeNewOperation(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call", Function: core.ToolCallFunction{Name: "computer_execute", Arguments: `{"kind":"terminal.exec","parameters":{"command":"synthetic"}}`}}}}}, nil
	}))
	c.config.MaxToolCalls = 2
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	result := awaitTask(t, c, task.ID)
	if result.Code != "tool_budget" {
		t.Fatalf("budget not reported: %+v", result)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, r := range fake.calls {
		if r.Kind == "terminal.exec" {
			t.Fatal("budget allowed extra effect")
		}
	}
}
func TestDecodeRejectsIdentityAndTrailingGarbage(t *testing.T) {
	for _, raw := range []string{`{"kind":"file.write","actor_id":"fake","parameters":{}}`, `{"kind":"file.read","parameters":{}} garbage`, `{"kind":"file.read","parameters":{}} {}`} {
		var v struct {
			Kind       string         `json:"kind"`
			Parameters map[string]any `json:"parameters"`
		}
		if decode([]byte(raw), &v) == nil {
			t.Fatal("invalid tool arguments accepted")
		}
	}
}
