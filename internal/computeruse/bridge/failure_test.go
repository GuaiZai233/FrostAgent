package bridge

import (
	"context"
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
)

type panickingValidator struct{ testExecutor }

func (panickingValidator) Validate(Request) error { panic("synthetic-secret-validation-input") }

func TestValidationPanicIsRejectedWithoutLoggingOrExecution(t *testing.T) {
	s, _ := testService(t, panickingValidator{testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("invalid operation reached executor")
		return Outcome{}, nil
	}}})
	_, err := s.Submit(context.Background(), testRequest("validation-panic"))
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("validation panic leaked: %v", err)
	}
	if _, err := s.Lookup("validation-panic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid operation was journaled: %v", err)
	}
}

func TestCompletionJournalFailureRecoversUnknown(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s, config := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		<-release
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	req := testRequest("result-write-failed")
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	err := s.journal.db.Close()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	s.wg.Wait()
	if _, err := s.Lookup(req.OperationID); !errors.Is(err, ErrJournal) {
		t.Fatalf("lost result persistence exposed a proven outcome: %v", err)
	}
	if _, err := s.Submit(context.Background(), testRequest("later")); !errors.Is(err, ErrJournal) {
		t.Fatalf("journal fault admitted new operation: %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(config, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("operation replayed after result persistence failed")
		return Outcome{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	op, err := reopened.Submit(context.Background(), req)
	if err != nil || op.State != Unknown {
		t.Fatalf("uncertain completion incorrectly recovered: %+v %v", op, err)
	}
}

func TestCorruptJournalFailsClosed(t *testing.T) {
	s, config := testService(t, testExecutor{})
	if _, err := s.Submit(context.Background(), testRequest("corrupt")); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, s, "corrupt")
	s.mu.Lock()
	_, err := s.journal.db.Exec("UPDATE operations SET record = '{}' WHERE operation_id = 'corrupt'")
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup("corrupt"); !errors.Is(err, ErrJournal) {
		t.Fatalf("corrupt record accepted: %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(config, testExecutor{}); !errors.Is(err, ErrJournal) {
		if reopened != nil {
			_ = reopened.Shutdown(context.Background())
		}
		t.Fatalf("corrupt journal recovered as empty: %v", err)
	}
}

func TestOperationDeadlineIsNotProcessTerminationEvidence(t *testing.T) {
	deadlineReached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s, _ := testService(t, testExecutor{run: func(ctx context.Context, _ Request) (Outcome, error) {
		<-ctx.Done()
		close(deadlineReached)
		<-release
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	s.maxConcurrent = 1
	req := testRequest("deadline")
	req.TimeoutMS = 2
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deadlineReached:
	case <-time.After(3 * time.Second):
		t.Fatal("operation timeout was not applied")
	}
	op, err := s.Lookup(req.OperationID)
	if err != nil || op.State != Running {
		t.Fatalf("timeout falsely stopped the process: %+v %v", op, err)
	}
	if _, err := s.Submit(context.Background(), testRequest("capacity")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("timeout released an unconfirmed process slot: %v", err)
	}
	once.Do(func() { close(release) })
	if op := awaitOperation(t, s, req.OperationID); op.State != Succeeded {
		t.Fatalf("late real completion was discarded: %+v", op)
	}
}

func TestClientLostResponseThenAuthRevocationRemainsUnknown(t *testing.T) {
	s, _ := testService(t, testExecutor{})
	handler := testHandler(t, s)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	c := testClient(t, server)
	transport := c.http.Transport
	var dropped atomic.Bool
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		if err == nil && dropped.CompareAndSwap(false, true) {
			response.Body.Close()
			return nil, errors.New("synthetic lost response")
		}
		return response, err
	})
	req := testRequest("auth-revoked")
	_, err := c.Run(t.Context(), req)
	if !errors.Is(err, ErrOutcomeUnknown) || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("auth revocation erased previous execution uncertainty: %v", err)
	}
	if op := awaitOperation(t, s, req.OperationID); op.State != Succeeded {
		t.Fatalf("expected original submission to complete: %+v", op)
	}
}

func TestClientCancellationStopsWaitingNotExecution(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s, _ := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		close(started)
		<-release
		return Outcome{State: Succeeded, Code: "completed"}, nil
	}})
	server := httptest.NewTLSServer(testHandler(t, s))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := testClient(t, server).Run(ctx, testRequest("cancel-wait"))
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrOutcomeUnknown) {
			t.Fatalf("cancel hid uncertainty: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not cancel waiting")
	}
	if op, err := s.Lookup("cancel-wait"); err != nil || op.State != Running {
		t.Fatalf("client cancellation falsely stopped execution: %+v %v", op, err)
	}
	once.Do(func() { close(release) })
	awaitOperation(t, s, "cancel-wait")
}

func TestInvalidRequestIsNeverJournaled(t *testing.T) {
	s, _ := testService(t, testExecutor{})
	for _, mutate := range []func(*Request){
		func(r *Request) { r.OperationID = "../escape" },
		func(r *Request) { r.ActorID = "" },
		func(r *Request) { r.Generation = 0 },
		func(r *Request) { r.TimeoutMS = 0 },
		func(r *Request) { r.TimeoutMS = int64((25 * time.Hour) / time.Millisecond) },
		func(r *Request) { r.Parameters = []byte("{bad json}") },
		func(r *Request) { r.Parameters = []byte(`"` + strings.Repeat("a", MaxRequestBytes) + `"`) },
		func(r *Request) { r.Kind = "not_supported" },
	} {
		req := testRequest("invalid")
		mutate(&req)
		if _, err := s.Submit(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	if _, err := s.Lookup("invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid request created durable state: %v", err)
	}
}

func TestAuditConfigurationAndBlockedPath(t *testing.T) {
	for _, days := range []int{-1, 3651} {
		config := testConfig(t.TempDir())
		config.AuditRetentionDays = days
		if s, err := Open(config, testExecutor{}); !errors.Is(err, ErrInvalid) {
			if s != nil {
				_ = s.Shutdown(context.Background())
			}
			t.Fatalf("invalid retention accepted: %v", err)
		}
	}
	config := testConfig(t.TempDir())
	config.AuditRetentionDays = 30
	s, err := Open(config, testExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if s.audit.(*fileAudit).retentionDays != 30 {
		t.Fatal("configured retention ignored")
	}
	path := filepath.Join(config.AuditDir, "audit-"+time.Now().UTC().Format("2006-01-02")+".jsonl")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	op, err := s.Submit(context.Background(), testRequest("blocked-audit-path"))
	if err != nil || op.State != Failed {
		t.Fatalf("nonregular audit destination was accepted: %+v %v", op, err)
	}
}
