package bridge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// journal is private to one Service. The OS lease is held until every executor
// has returned, including while Shutdown's caller has already timed out.
type journal struct {
	db    *sql.DB
	lease *os.File
}

func openJournal(dir, instanceID string) (*journal, error) {
	file, err := os.OpenFile(filepath.Join(dir, "bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrJournal
	}
	if err := lockJournal(file); err != nil {
		file.Close()
		return nil, errors.New("computer-use journal already owned or cannot be locked")
	}
	j := &journal{lease: file}
	// Create with restrictive Unix permissions; Windows deployment must also
	// restrict the data directory's ACL to the dedicated bridge account.
	dbPath := filepath.Join(dir, "operations.db")
	created, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		j.close()
		return nil, ErrJournal
	}
	created.Close()
	j.db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		j.close()
		return nil, ErrJournal
	}
	j.db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 1000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		`CREATE TABLE IF NOT EXISTS bridge_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL, instance_id TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS operations (operation_id TEXT PRIMARY KEY, record TEXT NOT NULL)`,
	} {
		if _, err = j.db.Exec(statement); err != nil {
			j.close()
			return nil, ErrJournal
		}
	}
	if _, err = j.db.Exec("INSERT OR IGNORE INTO bridge_meta (id, version, instance_id) VALUES (1, 1, ?)", instanceID); err != nil {
		j.close()
		return nil, ErrJournal
	}
	var version int
	var boundInstance string
	if err = j.db.QueryRow("SELECT version, instance_id FROM bridge_meta WHERE id = 1").Scan(&version, &boundInstance); err != nil ||
		version != 1 || boundInstance != instanceID {
		j.close()
		return nil, errors.New("computer-use journal version or instance binding mismatch")
	}
	if err := j.recover(instanceID); err != nil {
		j.close()
		return nil, err
	}
	return j, nil
}

func (j *journal) recover(instanceID string) error {
	tx, err := j.db.Begin()
	if err != nil {
		return ErrJournal
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT operation_id, record FROM operations")
	if err != nil {
		return ErrJournal
	}
	var updates []Operation
	for rows.Next() {
		var id, data string
		var op Operation
		if err := rows.Scan(&id, &data); err != nil {
			rows.Close()
			return ErrJournal
		}
		if json.Unmarshal([]byte(data), &op) != nil || !op.valid() || op.OperationID != id || op.InstanceID != instanceID {
			rows.Close()
			return ErrJournal
		}
		switch op.State {
		case Accepted:
			op.Outcome = Outcome{State: Failed, Code: "interrupted_before_execution"}
		case Running:
			// The old process may have performed side effects before losing its
			// completion write. Never resubmit these operations to an executor.
			op.Outcome = Outcome{State: Unknown, Code: "bridge_restart"}
		default:
			continue
		}
		op.UpdatedAt = maxTime(time.Now().UTC(), op.CreatedAt)
		updates = append(updates, op)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrJournal
	}
	for _, op := range updates {
		data, _ := json.Marshal(op)
		if _, err := tx.Exec("UPDATE operations SET record = ? WHERE operation_id = ?", string(data), op.OperationID); err != nil {
			return ErrJournal
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrJournal
	}
	return nil
}

func (j *journal) get(id string) (Operation, error) {
	var data string
	if err := j.db.QueryRow("SELECT record FROM operations WHERE operation_id = ?", id).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Operation{}, ErrNotFound
		}
		return Operation{}, ErrJournal
	}
	var op Operation
	if err := json.Unmarshal([]byte(data), &op); err != nil || !op.valid() || op.OperationID != id {
		return Operation{}, ErrJournal
	}
	return op, nil
}

func (j *journal) insert(op Operation) error {
	data, _ := json.Marshal(op)
	if _, err := j.db.Exec("INSERT INTO operations (operation_id, record) VALUES (?, ?)", op.OperationID, string(data)); err != nil {
		return ErrJournal
	}
	return nil
}

func (j *journal) update(op Operation) error {
	data, _ := json.Marshal(op)
	result, err := j.db.Exec("UPDATE operations SET record = ? WHERE operation_id = ?", string(data), op.OperationID)
	if err != nil {
		return ErrJournal
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrJournal
	}
	return nil
}

func (j *journal) close() error {
	var err error
	if j.db != nil {
		err = j.db.Close()
	}
	// Closing releases the OS lease, even after a process crash; never unlink
	// the lock file, which could allow another process to lock a different inode.
	return errors.Join(err, j.lease.Close())
}

func maxTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
