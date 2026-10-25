package bridge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Executor is injected by the dedicated VM runtime, not by an HTTP request.
// Validate must be side-effect free. Execute must track its processes itself:
// context expiration alone is not proof that a process or GUI action stopped.
type Executor interface {
	Validate(Request) error
	Execute(context.Context, Request) (Outcome, error)
}

type Config struct {
	InstanceID         string
	StateDir           string
	AuditDir           string
	AuditRetentionDays int
	MaxConcurrent      int
}

type Service struct {
	mu            sync.Mutex
	journal       *journal
	audit         auditWriter
	executor      Executor
	instanceID    string
	maxConcurrent int
	active        int
	blocked       error
	closing       bool
	closed        bool
	wg            sync.WaitGroup
	done          chan struct{}
	closeErr      error
}

func Open(config Config, executor Executor) (*Service, error) {
	if executor == nil {
		return nil, ErrInvalid
	}
	return OpenWithFactory(config, func() (Executor, error) { return executor, nil })
}

// OpenWithFactory acquires exclusive journal ownership before opening any
// mutable machine registry. A second process must not pause the first's tasks.
func OpenWithFactory(config Config, factory func() (Executor, error)) (*Service, error) {
	if !identifier.MatchString(config.InstanceID) || factory == nil ||
		!filepath.IsAbs(config.StateDir) || !filepath.IsAbs(config.AuditDir) ||
		config.MaxConcurrent < 1 || config.MaxConcurrent > 64 {
		return nil, ErrInvalid
	}
	if config.AuditRetentionDays == 0 {
		config.AuditRetentionDays = DefaultAuditRetentionDays
	}
	if config.AuditRetentionDays < 1 || config.AuditRetentionDays > 3650 {
		return nil, ErrInvalid
	}
	for _, dir := range []string{config.StateDir, config.AuditDir} {
		if err := ensureDataDirectory(dir); err != nil {
			return nil, errors.New("cannot create computer-use data directories")
		}
	}
	stateDir, err1 := filepath.EvalSymlinks(config.StateDir)
	auditDir, err2 := filepath.EvalSymlinks(config.AuditDir)
	if err1 != nil || err2 != nil || nestedPath(stateDir, auditDir) || nestedPath(auditDir, stateDir) {
		return nil, errors.New("computer-use state and audit directories must be separate")
	}
	j, err := openJournal(stateDir, config.InstanceID)
	if err != nil {
		return nil, err
	}
	executor, err := factory()
	if err != nil || executor == nil {
		_ = j.close()
		if err == nil {
			err = ErrInvalid
		}
		return nil, err
	}
	return &Service{
		journal: j, audit: &fileAudit{dir: auditDir, retentionDays: config.AuditRetentionDays, now: time.Now},
		executor: executor, instanceID: config.InstanceID, maxConcurrent: config.MaxConcurrent, done: make(chan struct{}),
	}, nil
}

func ensureDataDirectory(dir string) error {
	var missing []string
	for path := filepath.Clean(dir); ; path = filepath.Dir(path) {
		if _, err := os.Stat(path); err == nil {
			break
		} else if !os.IsNotExist(err) || filepath.Dir(path) == path {
			return err
		}
		missing = append(missing, path)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// A new directory also needs its name persisted in its parent on Unix.
	for i := len(missing) - 1; i >= 0; i-- {
		if err := syncDataDirectory(filepath.Dir(missing[i])); err != nil {
			return err
		}
	}
	return nil
}

func nestedPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Service) Submit(ctx context.Context, request Request) (Operation, error) {
	// Own the payload before returning to a caller who may reuse its buffer.
	request.Parameters = append([]byte(nil), request.Parameters...)
	hash, err := request.fingerprint()
	if err != nil || request.InstanceID != s.instanceID {
		return Operation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Operation{}, err
	}
	if s.closed {
		return Operation{}, ErrClosed
	}
	if errors.Is(s.blocked, ErrJournal) {
		return Operation{}, s.blocked
	}
	existing, err := s.journal.get(request.OperationID)
	if err == nil {
		if existing.RequestHash != hash {
			return Operation{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		s.blocked = ErrJournal
		return Operation{}, ErrJournal
	}
	if s.closing {
		return Operation{}, ErrClosed
	}
	if s.blocked != nil {
		return Operation{}, s.blocked
	}
	// Reserve bounded control headroom for stopping GUI input even while all
	// terminal slots are occupied. Control operations still require audit.
	limit := s.maxConcurrent
	if request.Kind == "task.pause" || request.Kind == "desktop.takeover" {
		limit += 16
	}
	if s.active >= limit {
		return Operation{}, ErrCapacity
	}
	if validateRequest(s.executor, request) != nil {
		return Operation{}, ErrInvalid
	}
	now := time.Now().UTC()
	op := Operation{
		Identity: request.Identity, RequestHash: hash, Outcome: Outcome{State: Accepted},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.journal.insert(op); err != nil {
		s.blocked = ErrJournal
		return Operation{}, ErrJournal
	}
	// Both the accepted and dispatch audit writes precede all side effects.
	if err := s.audit.append(op); err != nil {
		return s.auditRejected(op)
	}
	op.State = Running
	op.UpdatedAt = maxTime(time.Now().UTC(), op.UpdatedAt)
	if err := s.journal.update(op); err != nil {
		s.blocked = ErrJournal
		return Operation{}, ErrJournal
	}
	if err := s.audit.append(op); err != nil {
		return s.auditRejected(op)
	}
	s.active++
	s.wg.Add(1)
	// Transport cancellation must not cancel accepted work. The operation's
	// own timeout is fixed in its fingerprint and independent of HTTP lifetime.
	go s.execute(request, op)
	return op, nil
}

func validateRequest(executor Executor, request Request) (err error) {
	defer func() {
		if recover() != nil {
			// Do not let net/http's panic logger print raw validation inputs.
			err = ErrInvalid
		}
	}()
	if executor.Validate(request) != nil {
		return ErrInvalid
	}
	return nil
}

func (s *Service) auditRejected(op Operation) (Operation, error) {
	s.blocked = ErrAudit
	op.Outcome = Outcome{State: Failed, Code: "audit_unavailable"}
	op.UpdatedAt = maxTime(time.Now().UTC(), op.UpdatedAt)
	if s.journal.update(op) != nil {
		s.blocked = ErrJournal
		return Operation{}, ErrJournal
	}
	return op, nil
}

func (s *Service) execute(request Request, op Operation) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(request.TimeoutMS)*time.Millisecond)
	defer cancel()
	op.Outcome = invokeExecutor(ctx, s.executor, request)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	op.UpdatedAt = maxTime(time.Now().UTC(), op.UpdatedAt)
	// Persist the completion fact even if audit is broken. A failed result
	// write blocks further work and lookups until recovery marks it unknown.
	if s.journal.update(op) != nil {
		s.blocked = ErrJournal
		return
	}
	if s.audit.append(op) != nil && s.blocked == nil {
		s.blocked = ErrAudit
	}
}

func invokeExecutor(ctx context.Context, executor Executor, request Request) (out Outcome) {
	defer func() {
		if recover() != nil {
			out = Outcome{State: Unknown, Code: "executor_panic"}
		}
	}()
	out, err := executor.Execute(ctx, request)
	if err != nil {
		return Outcome{State: Unknown, Code: "executor_error"}
	}
	if !out.State.Terminal() || !validOutcome(out) {
		return Outcome{State: Unknown, Code: "invalid_outcome"}
	}
	// The executor may reuse its result storage after returning.
	if out.ExitCode != nil {
		code := *out.ExitCode
		out.ExitCode = &code
	}
	return out
}

func (s *Service) Lookup(id string) (Operation, error) {
	if !identifier.MatchString(id) {
		return Operation{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Operation{}, ErrClosed
	}
	if errors.Is(s.blocked, ErrJournal) {
		return Operation{}, ErrJournal
	}
	op, err := s.journal.get(id)
	if errors.Is(err, ErrJournal) {
		s.blocked = ErrJournal
	}
	return op, err
}

// Shutdown stops admission but does not assert that running processes stopped.
// Even on a timeout the journal lease remains held until all executors return.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		go func() {
			s.wg.Wait()
			s.mu.Lock()
			defer s.mu.Unlock()
			s.closeErr = s.journal.close()
			if closer, ok := s.executor.(io.Closer); ok {
				s.closeErr = errors.Join(s.closeErr, closer.Close())
			}
			s.closed = true
			close(s.done)
		}()
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
