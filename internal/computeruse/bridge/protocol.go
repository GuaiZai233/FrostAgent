package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

const (
	ProtocolVersion  = 1
	MaxRequestBytes  = 256 << 10
	MaxResponseBytes = 16 << 10
)

var (
	ErrInvalid        = errors.New("invalid computer-use request")
	ErrConflict       = errors.New("computer-use operation ID already has different parameters")
	ErrNotFound       = errors.New("computer-use operation not found")
	ErrUnauthorized   = errors.New("computer-use bridge authentication failed")
	ErrCapacity       = errors.New("computer-use bridge concurrency limit reached")
	ErrClosed         = errors.New("computer-use bridge is stopping")
	ErrAudit          = errors.New("computer-use audit unavailable; new operations paused")
	ErrJournal        = errors.New("computer-use journal unavailable; execution outcome may be unknown")
	ErrOutcomeUnknown = errors.New("computer-use communication failed; execution outcome unknown")
	ErrProtocol       = errors.New("invalid computer-use bridge response")
)

type State string

const (
	Accepted  State = "accepted"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
	Unknown   State = "unknown"
)

func (s State) Terminal() bool {
	return s == Succeeded || s == Failed || s == Unknown
}

// Identity is assigned by trusted FrostAgent orchestration, never by model
// arguments. Generation fences responses from earlier task continuations.
type Identity struct {
	Version     int    `json:"version"`
	OperationID string `json:"operation_id"`
	InstanceID  string `json:"instance_id"`
	TaskID      string `json:"task_id"`
	Generation  uint64 `json:"generation"`
	ActorID     string `json:"actor_id"`
	Kind        string `json:"kind"`
}

type Request struct {
	Identity
	TimeoutMS  int64           `json:"timeout_ms"`
	Parameters json.RawMessage `json:"parameters"`
}

// Outcome contains metadata only. Raw terminal output, files, screenshots and
// executor errors must not enter the durable journal or audit via this type.
// An executor error or an invalid outcome is conservatively recorded as unknown.
type Outcome struct {
	State    State  `json:"state"`
	Code     string `json:"code"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

type Operation struct {
	Identity
	Outcome
	RequestHash string    `json:"request_hash"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var kindIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)

func (i Identity) valid() bool {
	return i.Version == ProtocolVersion && i.Generation > 0 &&
		identifier.MatchString(i.OperationID) && identifier.MatchString(i.InstanceID) &&
		identifier.MatchString(i.TaskID) && identifier.MatchString(i.ActorID) &&
		kindIdentifier.MatchString(i.Kind)
}

func (r Request) fingerprint() (string, error) {
	if !r.Identity.valid() || r.TimeoutMS < 1 || r.TimeoutMS > int64((24*time.Hour)/time.Millisecond) ||
		len(r.Parameters) == 0 || !json.Valid(r.Parameters) {
		return "", ErrInvalid
	}
	// Marshal compacts whitespace in RawMessage without coercing large numbers.
	// Object key order remains part of request identity; retries reuse this body.
	body, err := json.Marshal(r)
	if err != nil || len(body) > MaxRequestBytes {
		return "", ErrInvalid
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

func validOutcome(o Outcome) bool {
	switch o.State {
	case Accepted, Running:
		return o.Code == "" && o.ExitCode == nil
	case Succeeded:
		return o.Code == "completed" && (o.ExitCode == nil || *o.ExitCode == 0)
	case Failed:
		return o.Code == "execution_failed" || o.Code == "audit_unavailable" || o.Code == "interrupted_before_execution"
	case Unknown:
		return o.Code == "outcome_unknown" || o.Code == "executor_error" || o.Code == "executor_panic" ||
			o.Code == "invalid_outcome" || o.Code == "bridge_restart"
	}
	return false
}

func (o Operation) valid() bool {
	hash, err := hex.DecodeString(o.RequestHash)
	return o.Identity.valid() && err == nil && len(hash) == sha256.Size &&
		validOutcome(o.Outcome) && !o.CreatedAt.IsZero() && !o.UpdatedAt.Before(o.CreatedAt)
}
