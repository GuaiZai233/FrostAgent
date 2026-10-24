package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"FrostAgent/internal/storage"
)

const ComputerUseNotice = "仅包含项目和任务元数据；不包含桥凭据、项目环境、VM 磁盘、代码、工具链或工作会话内容。恢复后需重新绑定专用 VM，核对旧操作并由主人显式继续。"

// ComputerUse is an additive optional instance-backup member. It cannot
// represent credentials, model messages, parameters or operation content.
type ComputerUse struct {
	FormatVersion int                          `json:"format_version"`
	Projects      []storage.ComputerUseProject `json:"projects"`
	Tasks         []storage.ComputerUseTask    `json:"tasks"`
	Notice        string                       `json:"notice"`
}

func ExportComputerUse(db *storage.DB, instanceID string) (ComputerUse, error) {
	snapshot, err := db.SnapshotComputerUse(context.Background(), instanceID)
	if err != nil {
		return ComputerUse{}, err
	}
	for i := range snapshot.Projects {
		// Even allowed location variables can contain operator-specific data.
		// A VM binding must be configured again after any restore/copy.
		snapshot.Projects[i].Environment = nil
	}
	return ComputerUse{
		FormatVersion: FormatVersion, Projects: snapshot.Projects,
		Tasks: snapshot.Tasks, Notice: ComputerUseNotice,
	}, nil
}

func ValidateComputerUse(data ComputerUse) error {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return err
	}
	if len(data.Projects) > 10000 || len(data.Tasks) > 100000 {
		return errors.New("computer-use backup contains too many records")
	}
	projects := make(map[string]bool, len(data.Projects))
	for _, project := range data.Projects {
		if err := storage.ValidateComputerUseProject(project); err != nil {
			return err
		}
		if projects[project.ID] {
			return errors.New("computer-use backup contains duplicate project IDs")
		}
		projects[project.ID] = true
	}
	tasks := make(map[string]bool, len(data.Tasks))
	for _, task := range data.Tasks {
		if err := storage.ValidateComputerUseTask(task); err != nil {
			return err
		}
		if tasks[task.ID] {
			return errors.New("computer-use backup contains duplicate task IDs")
		}
		tasks[task.ID] = true
	}
	return nil
}

// ImportComputerUse imports metadata into a stopped/new instance. It detaches
// the VM and never sends a bridge request or touches any remote filesystem.
func ImportComputerUse(db *storage.DB, instanceID string, data ComputerUse) error {
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeComputerUseTx(ctx, db, tx, instanceID, data); err != nil {
		return err
	}
	return tx.Commit()
}

func writeComputerUseTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, data ComputerUse) error {
	if err := ValidateComputerUse(data); err != nil {
		return err
	}
	var present int
	if err := tx.QueryRowContext(ctx, db.Bind(`SELECT COUNT(*) FROM instances WHERE id = ? AND deleting = 0`), instanceID).Scan(&present); err != nil {
		return err
	}
	if present != 1 {
		return sql.ErrNoRows
	}
	for _, table := range []string{"computer_use_config", "computer_use_projects", "computer_use_tasks"} {
		if _, err := tx.ExecContext(ctx, db.Bind("DELETE FROM "+table+" WHERE instance_id = ?"), instanceID); err != nil {
			return err
		}
	}
	for _, project := range data.Projects {
		project.Environment = nil
		raw, err := json.Marshal(project)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO computer_use_projects(instance_id, id, metadata_json) VALUES (?, ?, ?)`),
			instanceID, project.ID, string(raw)); err != nil {
			return err
		}
	}
	for _, task := range data.Tasks {
		// The destination has a different instance/VM trust binding. Keep the
		// last effect fact as history, never as a dispatchable pending request.
		if task.Pending != nil {
			task.LastOperation = task.Pending
		}
		task.Pending, task.PauseOperation = nil, nil
		task.BridgeURL = ""
		task.State, task.Code = "paused", "restored_requires_rebind"
		task.Prepared, task.GUIStopped, task.ReconcileRequired = false, false, true
		task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		raw, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO computer_use_tasks(instance_id, id, generation, metadata_json) VALUES (?, ?, ?, ?)`),
			instanceID, task.ID, int64(task.Generation), string(raw)); err != nil {
			return fmt.Errorf("restore computer-use task metadata: %w", err)
		}
	}
	return nil
}
