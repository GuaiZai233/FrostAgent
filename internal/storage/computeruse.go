package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

// Computer-use persistence contains metadata only. Model conversations, command
// parameters, source, screenshots and raw results must never be placed here.
// The bridge token stays in the primary database, not instance exports.
type ComputerUseConfig struct {
	Enabled            bool   `json:"enabled"`
	BridgeURL          string `json:"bridge_url"`
	Token              string `json:"token"`
	CAPEM              string `json:"ca_pem"`
	MaxConcurrent      int    `json:"max_concurrent"`
	MaxSeconds         int    `json:"max_seconds"`
	MaxModelRounds     int    `json:"max_model_rounds"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	AuditRetentionDays int    `json:"audit_retention_days"`
}

type ComputerUseProject struct {
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Remote      string            `json:"remote"`
	Baseline    string            `json:"baseline"`
	Shell       string            `json:"shell"`
	Environment map[string]string `json:"environment,omitempty"`
	Archived    bool              `json:"archived"`
}

type ComputerUseOperation struct {
	ID          string `json:"id"`
	InstanceID  string `json:"instance_id"`
	TaskID      string `json:"task_id"`
	ActorID     string `json:"actor_id"`
	Kind        string `json:"kind"`
	RequestHash string `json:"request_hash"`
	State       string `json:"state"`
	Code        string `json:"code"`
	Generation  uint64 `json:"generation"`
	ExitCode    *int   `json:"exit_code,omitempty"`
}

type ComputerUseTask struct {
	ID                string                `json:"id"`
	ProjectID         string                `json:"project_id"`
	ActorID           string                `json:"actor_id"`
	ActorKind         string                `json:"actor_kind"`
	BridgeURL         string                `json:"bridge_url"`
	State             string                `json:"state"`
	Code              string                `json:"code"`
	Workspace         string                `json:"workspace"`
	Branch            string                `json:"branch"`
	Generation        uint64                `json:"generation"`
	ModelRounds       int                   `json:"model_rounds"`
	ToolCalls         int                   `json:"tool_calls"`
	GUIStopped        bool                  `json:"gui_stopped"`
	Archived          bool                  `json:"archived"`
	Prepared          bool                  `json:"prepared"`
	ReconcileRequired bool                  `json:"reconcile_required"`
	CreatedAt         string                `json:"created_at"`
	UpdatedAt         string                `json:"updated_at"`
	Pending           *ComputerUseOperation `json:"pending,omitempty"`
	LastOperation     *ComputerUseOperation `json:"last_operation,omitempty"`
	PauseOperation    *ComputerUseOperation `json:"pause_operation,omitempty"`
}

var ErrComputerUseStaleGeneration = errors.New("computer-use task generation is stale")

func DefaultComputerUseConfig() ComputerUseConfig {
	return ComputerUseConfig{
		MaxConcurrent: 2, MaxSeconds: 1800, MaxModelRounds: 32,
		MaxToolCalls: 128, AuditRetentionDays: 7,
	}
}

func (d *DB) ComputerUseConfig(ctx context.Context, instanceID string) (ComputerUseConfig, error) {
	var raw string
	err := d.SQL.QueryRowContext(ctx, d.Bind(`SELECT config_json FROM computer_use_config WHERE instance_id = ?`), instanceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultComputerUseConfig(), nil
	}
	if err != nil {
		return ComputerUseConfig{}, err
	}
	var config ComputerUseConfig
	if err := decodeComputerUse(raw, &config); err != nil {
		return ComputerUseConfig{}, err
	}
	return config, nil
}

func (d *DB) SaveComputerUseConfig(ctx context.Context, instanceID string, config ComputerUseConfig) error {
	if config.MaxConcurrent < 1 || config.MaxConcurrent > 64 || config.MaxSeconds < 1 ||
		config.MaxSeconds > 86400 || config.MaxModelRounds < 1 || config.MaxModelRounds > 10000 ||
		config.MaxToolCalls < 1 || config.MaxToolCalls > 100000 || config.AuditRetentionDays < 1 ||
		config.AuditRetentionDays > 3650 || len(config.Token) > 256 || len(config.CAPEM) > 64<<10 ||
		len(config.BridgeURL) > 4096 {
		return errors.New("invalid computer-use configuration")
	}
	if config.BridgeURL != "" {
		u, err := url.Parse(config.BridgeURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid computer-use bridge URL")
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = d.SQL.ExecContext(ctx, d.Bind(`INSERT INTO computer_use_config(instance_id, config_json)
		VALUES (?, ?) ON CONFLICT(instance_id) DO UPDATE SET config_json = excluded.config_json`), instanceID, string(raw))
	return err
}

func (d *DB) ComputerUseProjects(ctx context.Context, instanceID string) ([]ComputerUseProject, error) {
	rows, err := d.SQL.QueryContext(ctx, d.Bind(`SELECT metadata_json FROM computer_use_projects WHERE instance_id = ? ORDER BY id`), instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ComputerUseProject, 0)
	for rows.Next() {
		var raw string
		var project ComputerUseProject
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := decodeComputerUse(raw, &project); err != nil {
			return nil, err
		}
		if err := ValidateComputerUseProject(project); err != nil {
			return nil, err
		}
		result = append(result, project)
	}
	return result, rows.Err()
}

// ValidateComputerUseProject only permits toolchain location variables, never
// arbitrary environment credentials. Paths and refs are validated by the VM.
func ValidateComputerUseProject(project ComputerUseProject) error {
	if !computerUseID(project.ID) || !computerUseText(project.Path, 4096) ||
		!computerUseText(project.Remote, 4096) || !computerUseText(project.Baseline, 1024) ||
		!computerUseText(project.Shell, 4096) || len(project.Environment) > 48 {
		return errors.New("invalid computer-use project metadata")
	}
	if u, err := url.Parse(project.Remote); err == nil {
		_, password := u.User.Password()
		if password || u.User != nil && u.Scheme != "ssh" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("project remote must not contain credentials or query parameters")
		}
	}
	seen := make(map[string]bool, len(project.Environment))
	for key, value := range project.Environment {
		canonical := strings.ToUpper(key)
		if seen[canonical] {
			return errors.New("project environment contains duplicate variable")
		}
		seen[canonical] = true
		switch canonical {
		case "PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR",
			"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
			"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMDATA", "LANG", "LC_ALL",
			"GOPATH", "GOROOT", "GOTOOLCHAIN", "JAVA_HOME", "NODE_HOME", "NVM_HOME",
			"NVM_SYMLINK", "PYTHONHOME", "PYTHONPATH", "PYENV_ROOT", "RUSTUP_HOME",
			"CARGO_HOME", "DOTNET_ROOT", "VIRTUAL_ENV":
		default:
			return errors.New("project environment contains unsupported variable")
		}
		if !computerUseText(value, 32768) {
			return errors.New("invalid project environment value")
		}
	}
	return nil
}

func (d *DB) SaveComputerUseProject(ctx context.Context, instanceID string, project ComputerUseProject) error {
	if err := ValidateComputerUseProject(project); err != nil {
		return err
	}
	raw, err := json.Marshal(project)
	if err != nil {
		return err
	}
	_, err = d.SQL.ExecContext(ctx, d.Bind(`INSERT INTO computer_use_projects(instance_id, id, metadata_json)
		VALUES (?, ?, ?) ON CONFLICT(instance_id, id) DO UPDATE SET metadata_json = excluded.metadata_json`), instanceID, project.ID, string(raw))
	return err
}

// DeleteComputerUseProject removes registration metadata only. It never calls
// the bridge or deletes any repository, worktree or toolchain.
func (d *DB) DeleteComputerUseProject(ctx context.Context, instanceID, projectID string) error {
	_, err := d.SQL.ExecContext(ctx, d.Bind(`DELETE FROM computer_use_projects WHERE instance_id = ? AND id = ?`), instanceID, projectID)
	return err
}

func (d *DB) ComputerUseTasks(ctx context.Context, instanceID string) ([]ComputerUseTask, error) {
	return d.computerUseTasks(ctx, d.SQL, instanceID)
}

type computerUseQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (d *DB) computerUseTasks(ctx context.Context, q computerUseQuerier, instanceID string) ([]ComputerUseTask, error) {
	rows, err := q.QueryContext(ctx, d.Bind(`SELECT metadata_json FROM computer_use_tasks WHERE instance_id = ? ORDER BY id`), instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ComputerUseTask, 0)
	for rows.Next() {
		var raw string
		var task ComputerUseTask
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := decodeComputerUse(raw, &task); err != nil {
			return nil, err
		}
		if err := ValidateComputerUseTask(task); err != nil {
			return nil, err
		}
		result = append(result, task)
	}
	return result, rows.Err()
}

func ValidateComputerUseTask(task ComputerUseTask) error {
	if !computerUseID(task.ID) || !computerUseID(task.ProjectID) || !computerUseID(task.ActorID) ||
		!computerUseCode(task.ActorKind) || !computerUseCode(task.State) || !computerUseCode(task.Code) ||
		!computerUseText(task.Workspace, 4096) || !computerUseText(task.Branch, 1024) ||
		!computerUseText(task.BridgeURL, 4096) ||
		task.Generation == 0 || task.Generation > math.MaxInt64 || task.ModelRounds < 0 || task.ToolCalls < 0 {
		return errors.New("invalid computer-use task metadata")
	}
	if task.BridgeURL != "" {
		u, err := url.Parse(task.BridgeURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid computer-use task bridge URL")
		}
	}
	for _, timestamp := range []string{task.CreatedAt, task.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
			return errors.New("invalid computer-use task timestamp")
		}
	}
	for _, operation := range []*ComputerUseOperation{task.Pending, task.LastOperation, task.PauseOperation} {
		if operation == nil {
			continue
		}
		if !computerUseID(operation.ID) || !computerUseID(operation.InstanceID) ||
			operation.TaskID != task.ID || !computerUseID(operation.ActorID) ||
			!computerUseCode(operation.Kind) || !computerUseCode(operation.State) ||
			!computerUseCode(operation.Code) || operation.Generation == 0 || operation.Generation > task.Generation {
			return errors.New("invalid computer-use operation metadata")
		}
		hash, err := hex.DecodeString(operation.RequestHash)
		if err != nil || len(hash) != 32 {
			return errors.New("invalid computer-use operation hash")
		}
	}
	return nil
}

// SaveComputerUseTask atomically saves state and pending operation identity.
// Callers must persist it successfully before submitting a bridge operation.
// A late response cannot overwrite a newer persisted generation.
func (d *DB) SaveComputerUseTask(ctx context.Context, instanceID string, task ComputerUseTask) error {
	if err := ValidateComputerUseTask(task); err != nil {
		return err
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	result, err := d.SQL.ExecContext(ctx, d.Bind(`INSERT INTO computer_use_tasks(instance_id, id, generation, metadata_json)
		VALUES (?, ?, ?, ?) ON CONFLICT(instance_id, id) DO UPDATE SET
		generation = excluded.generation, metadata_json = excluded.metadata_json
		WHERE computer_use_tasks.generation <= excluded.generation`), instanceID, task.ID, int64(task.Generation), string(raw))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrComputerUseStaleGeneration
	}
	return nil
}

// PauseComputerUseTasks is startup/reload recovery, never execution recovery.
// It preserves all operation identities so uncertain effects can be queried
// without resubmission. GUI stopping is unconfirmed until the bridge responds.
func (d *DB) PauseComputerUseTasks(ctx context.Context, instanceID string) error {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tasks, err := d.computerUseTasks(ctx, tx, instanceID)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		switch task.State {
		case "succeeded", "failed", "cancelled", "completed":
			continue
		}
		task.State, task.Code = "paused", "restart_requires_resume"
		task.GUIStopped, task.ReconcileRequired = false, true
		task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		raw, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, d.Bind(`UPDATE computer_use_tasks SET metadata_json = ? WHERE instance_id = ? AND id = ?`),
			string(raw), instanceID, task.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func decodeComputerUse(raw string, value any) error {
	if len(raw) > 512<<10 {
		return errors.New("computer-use metadata is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid computer-use metadata")
	}
	return nil
}

func computerUseID(value string) bool {
	return value != "" && computerUseText(value, 160)
}

func computerUseCode(value string) bool {
	if len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func computerUseText(value string, max int) bool {
	return len(value) <= max && !strings.ContainsAny(value, "\x00\r\n")
}
