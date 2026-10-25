package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testExecutor struct {
	run func(context.Context, Request) (Outcome, error)
}

func (e testExecutor) Validate(r Request) error {
	if r.Kind != "terminal.exec" {
		return errors.New("unsupported synthetic operation")
	}
	return nil
}

func (e testExecutor) Execute(ctx context.Context, r Request) (Outcome, error) {
	if e.run != nil {
		return e.run(ctx, r)
	}
	return Outcome{State: Succeeded, Code: "completed"}, nil
}

func testRequest(id string) Request {
	return Request{
		Identity: Identity{Version: 1, OperationID: id, InstanceID: "synthetic-instance",
			TaskID: "synthetic-task", Generation: 1, ActorID: "synthetic-owner", Kind: "terminal.exec"},
		TimeoutMS: 5000, Parameters: json.RawMessage(`{"command":"synthetic-secret-command"}`),
	}
}

func testConfig(root string) Config {
	return Config{InstanceID: "synthetic-instance", StateDir: filepath.Join(root, "state"),
		AuditDir: filepath.Join(root, "audit"), MaxConcurrent: 4}
}

func testService(t *testing.T, executor Executor) (*Service, Config) {
	t.Helper()
	config := testConfig(t.TempDir())
	s, err := Open(config, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, config
}

func awaitOperation(t *testing.T, s *Service, id string) Operation {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		op, err := s.Lookup(id)
		if err != nil {
			t.Fatal(err)
		}
		if op.State.Terminal() {
			return op
		}
		select {
		case <-deadline:
			t.Fatal("operation never reached a terminal state")
		case <-tick.C:
		}
	}
}

func TestConcurrentDuplicateRequestsExecuteOnce(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		calls.Add(1)
		<-release
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	defer close(release)
	req := testRequest("same-operation")
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			op, err := s.Submit(context.Background(), req)
			if err != nil || op.State != Running {
				t.Errorf("duplicate submit: state=%s err=%v", op.State, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("executor invoked %d times", calls.Load())
	}
}

func TestParameterAndIdentityConflicts(t *testing.T) {
	s, _ := testService(t, testExecutor{})
	req := testRequest("operation")
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	finished := awaitOperation(t, s, req.OperationID)
	for name, mutate := range map[string]func(*Request){
		"parameters": func(r *Request) { r.Parameters = json.RawMessage(`{"command":"other"}`) },
		"task":       func(r *Request) { r.TaskID = "other-task" },
		"generation": func(r *Request) { r.Generation++ },
		"actor":      func(r *Request) { r.ActorID = "other-owner" },
		"timeout":    func(r *Request) { r.TimeoutMS++ },
		"kind":       func(r *Request) { r.Kind = "desktop.input" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			if _, err := s.Submit(context.Background(), changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
	req.Parameters = json.RawMessage(" { \"command\": \"synthetic-secret-command\" } ")
	replay, err := s.Submit(context.Background(), req)
	if err != nil || replay.RequestHash != finished.RequestHash || replay.State != Succeeded {
		t.Fatalf("whitespace-only retry: %+v %v", replay, err)
	}
	req.InstanceID = "another-instance"
	if _, err := s.Submit(context.Background(), req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-instance submit: %v", err)
	}
}

func TestPersistenceLeaseAndInstanceBinding(t *testing.T) {
	s, config := testService(t, testExecutor{})
	if other, err := Open(config, testExecutor{}); err == nil {
		_ = other.Shutdown(context.Background())
		t.Fatal("two services acquired the same journal")
	}
	req := testRequest("persisted")
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	before := awaitOperation(t, s, req.OperationID)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(config, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("completed operation was re-executed")
		return Outcome{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Submit(context.Background(), req)
	if err != nil || after.RequestHash != before.RequestHash || after.State != Succeeded {
		t.Fatalf("replay after restart: %+v %v", after, err)
	}
	if err := reopened.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	config.InstanceID = "different-instance"
	if wrong, err := Open(config, testExecutor{}); err == nil {
		_ = wrong.Shutdown(context.Background())
		t.Fatal("journal rebound to another instance")
	}
}

func TestRestartDoesNotResumeAcceptedOrRunning(t *testing.T) {
	config := testConfig(t.TempDir())
	if err := os.MkdirAll(config.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := openJournal(config.StateDir, config.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{Accepted, Running} {
		req := testRequest(string(state))
		hash, _ := req.fingerprint()
		now := time.Now().UTC()
		if err := j.insert(Operation{Identity: req.Identity, Outcome: Outcome{State: state},
			RequestHash: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("recovered operation was executed")
		return Outcome{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	for _, fixture := range []struct {
		id    string
		state State
		code  string
	}{
		{"accepted", Failed, "interrupted_before_execution"},
		{"running", Unknown, "bridge_restart"},
	} {
		op, err := s.Submit(context.Background(), testRequest(fixture.id))
		if err != nil || op.State != fixture.state || op.Code != fixture.code {
			t.Fatalf("recovered state: %+v %v", op, err)
		}
	}
}

// An actual child process dies after its synthetic side effect, before the
// bridge can write completion. This also verifies OS lease crash release.
func TestCrashAfterSideEffectDoesNotReplay(t *testing.T) {
	if os.Getenv("FROSTAGENT_BRIDGE_CRASH_HELPER") == "1" {
		root := os.Getenv("FROSTAGENT_BRIDGE_CRASH_ROOT")
		s, err := Open(testConfig(root), testExecutor{run: func(context.Context, Request) (Outcome, error) {
			if err := os.WriteFile(filepath.Join(root, "effect"), []byte("performed-once"), 0600); err != nil {
				os.Exit(78)
			}
			os.Exit(77)
			return Outcome{}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Submit(context.Background(), testRequest("crashed")); err != nil {
			t.Fatal(err)
		}
		_ = s.Shutdown(context.Background())
		t.Fatal("crash helper unexpectedly returned")
	}
	root := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestCrashAfterSideEffectDoesNotReplay$")
	cmd.Env = append(os.Environ(), "FROSTAGENT_BRIDGE_CRASH_HELPER=1", "FROSTAGENT_BRIDGE_CRASH_ROOT="+root)
	output, err := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 77 {
		t.Fatalf("expected crash after side effect: %v %s", err, output)
	}
	effect, err := os.ReadFile(filepath.Join(root, "effect"))
	if err != nil || string(effect) != "performed-once" {
		t.Fatalf("missing synthetic side effect: %q %v", effect, err)
	}
	s, err := Open(testConfig(root), testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("uncertain operation was blindly replayed")
		return Outcome{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	op, err := s.Submit(context.Background(), testRequest("crashed"))
	if err != nil || op.State != Unknown || op.Code != "bridge_restart" {
		t.Fatalf("crash recovery: %+v %v", op, err)
	}
}

type failingAudit struct{ calls, failAt int }

func (a *failingAudit) append(Operation) error {
	a.calls++
	if a.calls == a.failAt {
		return errors.New("synthetic sensitive filesystem error")
	}
	return nil
}

func TestAuditFailureBeforeDispatchBlocksNewWork(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
				t.Error("executed without durable audit")
				return Outcome{}, nil
			}})
			s.audit = &failingAudit{failAt: failAt}
			req := testRequest("audit-rejected")
			op, err := s.Submit(context.Background(), req)
			if err != nil || op.State != Failed || op.Code != "audit_unavailable" {
				t.Fatalf("audit rejection not recorded: %+v %v", op, err)
			}
			if _, err := s.Submit(context.Background(), testRequest("later")); !errors.Is(err, ErrAudit) {
				t.Fatalf("audit fault did not pause admission: %v", err)
			}
			op, err = s.Submit(context.Background(), req)
			if err != nil || op.State != Failed {
				t.Fatalf("rejected operation replay: %+v %v", op, err)
			}
		})
	}
}

func TestCompletionSurvivesAuditFailure(t *testing.T) {
	s, _ := testService(t, testExecutor{})
	s.audit = &failingAudit{failAt: 3}
	if _, err := s.Submit(context.Background(), testRequest("completed")); err != nil {
		t.Fatal(err)
	}
	op := awaitOperation(t, s, "completed")
	if op.State != Succeeded {
		t.Fatalf("lost completion fact: %+v", op)
	}
	if _, err := s.Submit(context.Background(), testRequest("later")); !errors.Is(err, ErrAudit) {
		t.Fatalf("completion audit failure did not pause admission: %v", err)
	}
}

func TestExecutorFailureMetadata(t *testing.T) {
	for name, executor := range map[string]testExecutor{
		"executor_error": {run: func(context.Context, Request) (Outcome, error) {
			return Outcome{}, errors.New("synthetic-secret-error")
		}},
		"executor_panic": {run: func(context.Context, Request) (Outcome, error) { panic("synthetic-secret-panic") }},
		"invalid_outcome": {run: func(context.Context, Request) (Outcome, error) {
			return Outcome{State: Succeeded, Code: "synthetic-secret-output"}, nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := testService(t, executor)
			if _, err := s.Submit(context.Background(), testRequest("failure")); err != nil {
				t.Fatal(err)
			}
			op := awaitOperation(t, s, "failure")
			if op.State != Unknown || op.Code != name {
				t.Fatalf("unproven failure incorrectly classified: %+v", op)
			}
			body, _ := json.Marshal(op)
			if strings.Contains(string(body), "synthetic-secret") {
				t.Fatal("raw executor content leaked into metadata")
			}
		})
	}
}

func TestConcurrencyAndShutdownKeepLeaseUntilExecutorReturns(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s, config := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		<-release
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	s.maxConcurrent = 1
	if _, err := s.Submit(context.Background(), testRequest("running")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(context.Background(), testRequest("capacity")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("concurrency not limited: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown falsely claimed completion: %v", err)
	}
	op, err := s.Lookup("running")
	if err != nil || op.State != Running {
		t.Fatalf("shutdown falsely stopped operation: %+v %v", op, err)
	}
	if other, err := Open(config, testExecutor{}); err == nil {
		_ = other.Shutdown(context.Background())
		t.Fatal("lease released while executor still active")
	}
	if _, err := s.Submit(context.Background(), testRequest("closed")); !errors.Is(err, ErrClosed) {
		t.Fatalf("shutdown admitted new operation: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRequestCancellationDoesNotCancelAcceptedOperation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s, _ := testService(t, testExecutor{run: func(ctx context.Context, _ Request) (Outcome, error) {
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Error("transport cancellation reached the executor")
		}
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.Submit(ctx, testRequest("detached")); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	close(release)
	if op := awaitOperation(t, s, "detached"); op.State != Succeeded {
		t.Fatalf("unexpected completion: %+v", op)
	}
}
