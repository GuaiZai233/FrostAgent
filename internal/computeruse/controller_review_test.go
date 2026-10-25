package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/computeruse/bridge"
	"github.com/GuaiZai233/FrostAgent/internal/computeruse/machine"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestLateTransportFailureCannotEraseReconciledCompletion(t *testing.T) {
	for _, pause := range []bool{false, true} {
		for _, state := range []bridge.State{bridge.Succeeded, bridge.Failed} {
			t.Run(map[bool]string{false: "perform", true: "pause"}[pause]+"/"+string(state), func(t *testing.T) {
				c, fake, _ := fixture(t, modelFunc(completedModel))
				task := reviewTask(t, c)
				entered, release := make(chan bridge.Request, 1), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				fake.run = func(_ context.Context, req bridge.Request) (bridge.Operation, error) {
					entered <- req
					<-release
					return bridge.Operation{}, bridge.ErrOutcomeUnknown
				}
				done := make(chan struct{})
				go func() {
					defer close(done)
					if pause {
						_, _ = c.pause(context.Background(), task.ID, "owner_paused")
					} else {
						_, _ = c.perform(context.Background(), task.ID, 1, "terminal.exec", map[string]string{"command": "synthetic"})
					}
				}()
				req := <-entered
				hash, _ := req.Fingerprint()
				code := "completed"
				if state == bridge.Failed {
					code = "execution_failed"
				}
				fake.mu.Lock()
				fake.ops[req.OperationID] = bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: state, Code: code}}
				fake.mu.Unlock()
				if _, err := c.reconcile(context.Background(), task.ID); err != nil {
					t.Fatal(err)
				}
				close(release)
				<-done
				c.mu.Lock()
				got := cloneTask(task)
				c.mu.Unlock()
				operation := got.Pending
				if pause {
					operation = got.PauseOperation
				}
				if operation.State != string(state) || operation.Code != code {
					t.Fatalf("late transport failure erased known completion: %+v", operation)
				}
				if pause && got.GUIStopped != (state == bridge.Succeeded) {
					t.Fatal("reconciled GUI stop fact lost")
				}
				if !pause && got.LastOperation.State != string(state) {
					t.Fatal("known last operation lost")
				}
			})
		}
	}
}

func TestMissingKnownFactDoesNotStrandUnsubmittedPause(t *testing.T) {
	c, _, _ := fixture(t, modelFunc(completedModel))
	task := reviewTask(t, c)
	task.State = "paused"
	op := reviewOperation(c, task, "terminal.exec", bridge.Succeeded)
	op.Code = "completed"
	task.Pending, task.LastOperation = operationMeta(op), operationMeta(op)
	stop := reviewOperation(c, task, "task.pause", bridge.Accepted)
	stop.OperationID = "synthetic-unsubmitted-stop"
	task.PauseOperation = operationMeta(stop)
	_, _ = c.reconcile(context.Background(), task.ID)
	if task.Pending.State != "succeeded" || task.PauseOperation.State != "unknown" || !task.ReconcileRequired {
		t.Fatalf("missing known fact prevented pause reconciliation: %+v", task)
	}
}

func TestControlNamedProjectRemainsVisibleAndResumable(t *testing.T) {
	c, fake, _ := fixture(t, modelFunc(completedModel))
	project := storage.ComputerUseProject{ID: "control", Path: "synthetic-path", Baseline: "main", Shell: "synthetic-shell"}
	if err := c.opts.DB.SaveComputerUseProject(context.Background(), c.opts.InstanceID, project); err != nil {
		t.Fatal(err)
	}
	task, err := c.start(actor{"qq_private", "synthetic-owner"}, "control", "synthetic independent instruction")
	if err != nil {
		t.Fatal(err)
	}
	_ = awaitTask(t, c, task.ID)
	input := core.ComputerUseIngress{Platform: "qq", UserID: "synthetic-owner", MessageType: "private",
		TransportProof: c.AuthenticateTransport("Bearer " + c.config.AdapterToken), Text: "/computer status"}
	if _, reply := c.HandleCommand(context.Background(), input); !strings.Contains(reply, task.ID) {
		t.Fatalf("legitimate project hidden from status: %s", reply)
	}
	if _, err := c.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "another independent instruction", false); err != nil {
		t.Fatalf("legitimate project could not continue: %v", err)
	}
	_ = awaitTask(t, c, task.ID)
	restarted, err := New(c.opts)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.newTransport = func(storage.ComputerUseConfig) (transport, error) { return fake, nil }
	if restarted.desktopGeneration != 0 {
		t.Fatal("work task generation mistaken for global desktop generation")
	}
	if _, err := restarted.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "continue after explicit inspection", true); err != nil {
		t.Fatalf("legitimate project could not continue after restart: %v", err)
	}
	_ = awaitTask(t, restarted, task.ID)
}

func TestAdministrativeTaskStartsWithItsRealPendingOperation(t *testing.T) {
	for _, kind := range []string{"project.list", "project.register", "project.archive", "desktop.takeover", "desktop.resume"} {
		t.Run(kind, func(t *testing.T) {
			c, fake, _ := fixture(t, modelFunc(completedModel))
			// Catch an intermediate persisted task with no classification even if
			// the next SQL write would normally replace it before transport.
			_, err := c.opts.DB.SQL.Exec(`CREATE TEMP TRIGGER synthetic_control_has_operation
				BEFORE INSERT ON computer_use_tasks
				WHEN json_extract(NEW.metadata_json, '$.pending.kind') IS NULL
				BEGIN SELECT RAISE(ABORT, 'missing first operation'); END`)
			if err != nil {
				t.Fatal(err)
			}
			var dispatched bridge.Request
			fake.run = func(_ context.Context, req bridge.Request) (bridge.Operation, error) {
				dispatched = req
				tasks, err := c.opts.DB.ComputerUseTasks(context.Background(), c.opts.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				hash, _ := req.Fingerprint()
				if len(tasks) != 1 || tasks[0].Pending == nil || tasks[0].Pending.ID != req.OperationID ||
					tasks[0].Pending.Kind != kind || tasks[0].Pending.RequestHash != hash || tasks[0].Pending.State != "accepted" {
					t.Fatalf("administrative identity not saved before dispatch: %+v", tasks)
				}
				return bridge.Operation{}, bridge.ErrOutcomeUnknown
			}
			if _, err := c.manual(context.Background(), c.managementActor(), kind, machine.Params{}); !errors.Is(err, bridge.ErrOutcomeUnknown) {
				t.Fatalf("manual did not reach the one real request: %v", err)
			}
			if dispatched.OperationID == "" || len(fake.calls) != 1 {
				t.Fatal("administrative request was not singular")
			}
			restarted, err := New(c.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			restarted.newTransport = func(storage.ComputerUseConfig) (transport, error) { return fake, nil }
			record := restarted.tasks[dispatched.TaskID]
			if record == nil || !isControlTask(record) || record.Pending.ID != dispatched.OperationID {
				t.Fatalf("administrative identity lost after restart: %+v", record)
			}
			if _, err := restarted.resume(actor{"qq_private", "synthetic-owner"}, record.ID, "do not turn control into work", true); !errors.Is(err, ErrState) {
				t.Fatalf("administrative task resumed as model work: %v", err)
			}
			wantGeneration := uint64(0)
			if strings.HasPrefix(kind, "desktop.") {
				wantGeneration = dispatched.Generation
			}
			if restarted.desktopGeneration != wantGeneration {
				t.Fatalf("wrong restored desktop generation %d", restarted.desktopGeneration)
			}
		})
	}
}

func TestControlClassificationUsesOnlyAdministrativeOperationKinds(t *testing.T) {
	for _, kind := range []string{"", "task.prepare", "task.instructions", "terminal.exec", "desktop.acquire", "desktop.release", "desktop.screenshot", "desktop.input", "maintenance.acquire"} {
		task := &storage.ComputerUseTask{ProjectID: "control"}
		if kind != "" {
			task.Pending = &storage.ComputerUseOperation{Kind: kind}
		}
		if isControlTask(task) || isDesktopControlTask(task) {
			t.Fatalf("work kind classified as control: %q", kind)
		}
	}
	for _, kind := range []string{"project.list", "project.register", "project.archive", "desktop.takeover", "desktop.resume"} {
		// The old format already persisted real operation kinds. No new schema
		// or task-ID convention is needed to recognize those existing records.
		task := &storage.ComputerUseTask{ProjectID: "control", LastOperation: &storage.ComputerUseOperation{Kind: kind}}
		if !isControlTask(task) {
			t.Fatalf("old administrative record not recognized: %s", kind)
		}
	}
}

type reviewTransport struct {
	transport
	lookup  func(context.Context, bridge.Identity, string) (bridge.Operation, error)
	content func(context.Context, bridge.Request) (bridge.Content, error)
}

func (r reviewTransport) Lookup(ctx context.Context, id bridge.Identity, hash string) (bridge.Operation, error) {
	if r.lookup != nil {
		return r.lookup(ctx, id, hash)
	}
	return r.transport.Lookup(ctx, id, hash)
}
func (r reviewTransport) Content(ctx context.Context, req bridge.Request) (bridge.Content, error) {
	if r.content != nil {
		return r.content(ctx, req)
	}
	return r.transport.Content(ctx, req)
}

func reviewTask(t *testing.T, c *Controller) *storage.ComputerUseTask {
	t.Helper()
	task := &storage.ComputerUseTask{ID: "synthetic-review-task", ProjectID: "synthetic-project",
		ActorID: "synthetic-owner", ActorKind: "qq_private", BridgeURL: c.config.BridgeURL,
		State: "running", Generation: 1, CreatedAt: now(), UpdatedAt: now()}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tasks[task.ID] = task
	if err := c.save(task); err != nil {
		t.Fatal(err)
	}
	return task
}

func reviewOperation(c *Controller, task *storage.ComputerUseTask, kind string, state bridge.State) bridge.Operation {
	req := bridge.Request{Identity: bridge.Identity{Version: 1, OperationID: "synthetic-unsubmitted-operation",
		InstanceID: c.opts.InstanceID, TaskID: task.ID, ActorID: task.ActorID, Generation: task.Generation, Kind: kind},
		TimeoutMS: 1000, Parameters: json.RawMessage("{}")}
	hash, _ := req.Fingerprint()
	return bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: state}}
}

func TestMissingPendingAfterRestartNeedsAcknowledgementWithoutReplay(t *testing.T) {
	for _, slot := range []string{"pending", "pause"} {
		for _, state := range []bridge.State{bridge.Accepted, bridge.Running} {
			t.Run(slot+"/"+string(state), func(t *testing.T) {
				c, fake, _ := fixture(t, modelFunc(completedModel))
				task := reviewTask(t, c)
				kind := "terminal.exec"
				if slot == "pause" {
					kind = "task.pause"
				}
				op := reviewOperation(c, task, kind, state)
				if slot == "pause" {
					task.PauseOperation = operationMeta(op)
				} else {
					task.Pending = operationMeta(op)
				}
				if err := c.save(task); err != nil {
					t.Fatal(err)
				}
				restarted, err := New(c.opts)
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close()
				restarted.newTransport = func(storage.ComputerUseConfig) (transport, error) { return fake, nil }
				reconciled, err := restarted.reconcile(context.Background(), task.ID)
				if err != nil {
					t.Fatalf("missing operation could not be reconciled: %v", err)
				}
				got := reconciled.Pending
				if slot == "pause" {
					got = reconciled.PauseOperation
				}
				if got == nil || got.State != "unknown" || got.ID != op.OperationID || got.RequestHash != op.RequestHash ||
					!reconciled.ReconcileRequired || reconciled.GUIStopped || reconciled.State != "paused" {
					t.Fatalf("missing operation lost identity or became executable: %+v", reconciled)
				}
				if len(fake.calls) != 0 {
					t.Fatal("reconcile replayed a missing operation")
				}
				if _, err := restarted.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "inspect existing effects only", false); err == nil {
					t.Fatal("resumed without explicit uncertainty acknowledgement")
				}
				if _, err := restarted.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "new independent instruction after inspection", true); err != nil {
					t.Fatalf("explicit recovery remained blocked: %v", err)
				}
				_ = awaitTask(t, restarted, task.ID)
				fake.mu.Lock()
				defer fake.mu.Unlock()
				for _, req := range fake.calls {
					if req.OperationID == op.OperationID || req.Kind == "terminal.exec" {
						t.Fatal("old side effect replayed")
					}
				}
			})
		}
	}
}

func TestReconcilePreservesNewerTerminalFact(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "not-found", false: "running"}[missing], func(t *testing.T) {
			c, fake, _ := fixture(t, modelFunc(completedModel))
			task := reviewTask(t, c)
			task.State = "paused"
			op := reviewOperation(c, task, "terminal.exec", bridge.Accepted)
			task.Pending = operationMeta(op)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			client := reviewTransport{transport: fake, lookup: func(context.Context, bridge.Identity, string) (bridge.Operation, error) {
				close(entered)
				<-release
				if missing {
					return bridge.Operation{}, bridge.ErrNotFound
				}
				result := op
				result.State = bridge.Running
				return result, nil
			}}
			c.newTransport = func(storage.ComputerUseConfig) (transport, error) { return client, nil }
			done := make(chan struct{})
			go func() { defer close(done); _, _ = c.reconcile(context.Background(), task.ID) }()
			<-entered
			c.mu.Lock()
			result := op
			result.State, result.Code = bridge.Succeeded, "completed"
			task.Pending, task.LastOperation = operationMeta(result), operationMeta(result)
			if err := c.save(task); err != nil {
				t.Fatal(err)
			}
			c.mu.Unlock()
			close(release)
			<-done
			if task.Pending.State != "succeeded" || task.LastOperation.State != "succeeded" {
				t.Fatalf("late lookup regressed terminal fact: %+v", task.Pending)
			}
		})
	}
}

func TestMissingLookupDoesNotReleaseInFlightOperation(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "perform", true: "pause"}[pause], func(t *testing.T) {
			c, fake, _ := fixture(t, modelFunc(completedModel))
			task := reviewTask(t, c)
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			fake.run = func(_ context.Context, req bridge.Request) (bridge.Operation, error) {
				close(entered)
				<-release
				hash, _ := req.Fingerprint()
				return bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Succeeded, Code: "completed"}}, nil
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if pause {
					_, _ = c.pause(context.Background(), task.ID, "owner_paused")
				} else {
					_, _ = c.perform(context.Background(), task.ID, 1, "terminal.exec", map[string]string{"command": "synthetic"})
				}
			}()
			<-entered
			_, _ = c.reconcile(context.Background(), task.ID)
			c.mu.Lock()
			pendingOp := task.Pending
			if pause {
				pendingOp = task.PauseOperation
			}
			state := pendingOp.State
			c.mu.Unlock()
			if state != "accepted" {
				t.Fatalf("live dispatch relabeled %s", state)
			}
			if _, err := c.resume(actor{"qq_private", "synthetic-owner"}, task.ID, "inspect", true); !errors.Is(err, ErrBusy) {
				t.Fatalf("live dispatch escaped pending guard: %v", err)
			}
			close(release)
			<-done
		})
	}
}

func TestFailedOperationExposesOnlyItsTransientDiagnostic(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "expired"}[expired], func(t *testing.T) {
			var modelCalls, contentCalls atomic.Int32
			c, fake, _ := fixture(t, modelFunc(func(context.Context, core.ChatRequest) (*core.ChatResponse, error) {
				modelCalls.Add(1)
				return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "synthetic-call",
					Function: core.ToolCallFunction{Name: "computer_execute", Arguments: `{"kind":"terminal.exec","parameters":{"command":"synthetic failing command"}}`}}}}}, nil
			}))
			fake.run = func(_ context.Context, req bridge.Request) (bridge.Operation, error) {
				hash, _ := req.Fingerprint()
				op := bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Succeeded, Code: "completed"}}
				if req.Kind == "terminal.exec" {
					exit := 7
					op.Outcome = bridge.Outcome{State: bridge.Failed, Code: "execution_failed", ExitCode: &exit}
				}
				return op, nil
			}
			client := reviewTransport{transport: fake, content: func(ctx context.Context, req bridge.Request) (bridge.Content, error) {
				if req.Kind != "terminal.exec" {
					return fake.Content(ctx, req)
				}
				contentCalls.Add(1)
				if expired {
					return bridge.Content{}, bridge.ErrContentExpired
				}
				value, _ := fake.Content(ctx, req)
				value.Text = "synthetic compiler diagnostic"
				return value, nil
			}}
			c.newTransport = func(storage.ComputerUseConfig) (transport, error) { return client, nil }
			task, err := c.start(actor{"qq_private", "synthetic-owner"}, "synthetic-project", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			result := awaitTask(t, c, task.ID)
			if result.State != "paused" || result.LastOperation.State != "failed" || modelCalls.Load() != 1 || contentCalls.Load() != 1 {
				t.Fatalf("failure did not stop with one diagnostic read: task=%+v model=%d content=%d", result, modelCalls.Load(), contentCalls.Load())
			}
			request := httptest.NewRequest(http.MethodGet, "/api/computer-use/tasks/"+task.ID+"/content", nil)
			request.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, request)
			if expired {
				if rec.Code != http.StatusGone {
					t.Fatalf("expired diagnostic reused stale output: %d %s", rec.Code, rec.Body.String())
				}
			} else {
				var value bridge.Content
				if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &value) != nil || value.OperationID != result.LastOperation.ID || value.Text != "synthetic compiler diagnostic" {
					t.Fatalf("failed diagnostic not visible: %d %s", rec.Code, rec.Body.String())
				}
			}
			var stored string
			if err := c.opts.DB.SQL.QueryRow("SELECT metadata_json FROM computer_use_tasks WHERE id = ?", task.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, "synthetic compiler diagnostic") {
				t.Fatal("raw diagnostic persisted")
			}
		})
	}
}

func TestDisabledConfigCanClearBridgeTokenWithoutDiscardingEndpoint(t *testing.T) {
	c, _, _ := fixture(t, modelFunc(completedModel))
	c.newTransport = makeTransport
	requestConfig := map[string]any{"enabled": false, "bridge_url": c.config.BridgeURL, "token": "", "max_concurrent": 2, "max_seconds": 1800, "max_model_rounds": 32, "max_tool_calls": 128, "audit_retention_days": 7}
	update := func() *httptest.ResponseRecorder {
		raw, _ := json.Marshal(requestConfig)
		req := httptest.NewRequest(http.MethodPut, "/api/computer-use/config", strings.NewReader(string(raw)))
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		c.ServeHTTP(rec, req)
		return rec
	}
	if rec := update(); rec.Code != http.StatusOK {
		t.Fatalf("disabled clear rejected: %d %s", rec.Code, rec.Body.String())
	}
	stored, err := c.opts.DB.ComputerUseConfig(context.Background(), c.opts.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Enabled || stored.Token != "" || stored.BridgeURL != "https://127.0.0.1:9443" {
		t.Fatalf("secret not cleared independently: %+v", stored)
	}
	requestConfig["enabled"] = true
	if rec := update(); rec.Code == http.StatusOK {
		t.Fatal("enabled without credentials")
	}
	requestConfig["enabled"] = false
	for _, endpoint := range []string{"https://example.invalid", "http://127.0.0.1:9443", "https://127.0.0.1:9443/path"} {
		requestConfig["bridge_url"] = endpoint
		if rec := update(); rec.Code == http.StatusOK {
			t.Fatalf("invalid disabled endpoint accepted: %s", endpoint)
		}
	}
	requestConfig["bridge_url"] = ""
	requestConfig["ca_pem"] = "invalid synthetic CA"
	if rec := update(); rec.Code == http.StatusOK {
		t.Fatal("invalid CA accepted with empty endpoint")
	}
}
