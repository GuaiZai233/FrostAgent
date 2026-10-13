package envinject

import (
	"FrostAgent/internal/storage"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const scope = "extra_env"

var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type appliedEntry struct {
	value    string
	injected bool
}

type Snapshot map[string]appliedEntry

var applied = struct {
	sync.RWMutex
	keys Snapshot
}{keys: Snapshot{}}

type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// List reads additional process environment variables from SQL. These values
// are applied only while FrostAgent starts, never when they are edited.
func List(ctx context.Context, db *storage.DB) ([]Entry, error) {
	rows, err := db.SQL.QueryContext(ctx, db.Bind(`SELECT key, value FROM settings WHERE scope = ? AND instance_id = ?`), scope, "")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.Key, &entry.Value); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return entries, nil
}

func Validate(key, value string) error {
	if len(key) > 128 || !keyPattern.MatchString(key) {
		return fmt.Errorf("环境变量名只能包含英文字母、数字和下划线，并以字母或下划线开头")
	}
	if len(value) > 16<<10 || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("环境变量值不得超过 16 KiB，也不能包含换行或 NUL")
	}
	return nil
}

func Put(ctx context.Context, db *storage.DB, key, value string) error {
	if err := Validate(key, value); err != nil {
		return err
	}
	_, err := db.SQL.ExecContext(ctx, db.Bind(`INSERT INTO settings(scope, instance_id, key, value)
		VALUES (?, ?, ?, ?) ON CONFLICT (scope, instance_id, key) DO UPDATE SET value = excluded.value`), scope, "", key, value)
	return err
}

func Delete(ctx context.Context, db *storage.DB, key string) error {
	if err := Validate(key, ""); err != nil {
		return err
	}
	_, err := db.SQL.ExecContext(ctx, db.Bind(`DELETE FROM settings WHERE scope = ? AND instance_id = ? AND key = ?`), scope, "", key)
	return err
}

// Replace atomically restores the persisted injection set. The active process
// environment is unchanged until the next FrostAgent start.
func Replace(ctx context.Context, db *storage.DB, values map[string]string) error {
	for key, value := range values {
		if err := Validate(key, value); err != nil {
			return fmt.Errorf("invalid extra environment entry %q: %w", key, err)
		}
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM settings WHERE scope = ? AND instance_id = ?`), scope, ""); err != nil {
		return err
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO settings(scope, instance_id, key, value) VALUES (?, ?, ?, ?)`),
			scope, "", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApplyAtStartup leaves variables supplied by the hosting environment intact.
// Values edited during this process lifetime are intentionally not applied.
func ApplyAtStartup(ctx context.Context, db *storage.DB) error {
	Suspend()
	entries, err := List(ctx, db)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := Validate(entry.Key, entry.Value); err != nil {
			return fmt.Errorf("invalid extra environment entry %q: %w", entry.Key, err)
		}
		if _, exists := os.LookupEnv(entry.Key); exists {
			applied.Lock()
			applied.keys[entry.Key] = appliedEntry{value: os.Getenv(entry.Key)}
			applied.Unlock()
			continue
		}
		if err := os.Setenv(entry.Key, entry.Value); err != nil {
			return fmt.Errorf("inject environment entry %q: %w", entry.Key, err)
		}
		applied.Lock()
		applied.keys[entry.Key] = appliedEntry{value: entry.Value, injected: true}
		applied.Unlock()
	}
	return nil
}

// Suspend prevents settings in a newly selected database from reading values
// injected from the previous database. Hosting environment values stay intact.
func Suspend() Snapshot {
	applied.Lock()
	defer applied.Unlock()
	previous := applied.keys
	applied.keys = Snapshot{}
	for key, entry := range previous {
		if entry.injected && os.Getenv(key) == entry.value {
			_ = os.Unsetenv(key)
		}
	}
	return previous
}

// Restore reactivates the original startup injection after a failed switch.
func Restore(previous Snapshot) error {
	applied.Lock()
	defer applied.Unlock()
	for key, entry := range previous {
		if entry.injected {
			if _, exists := os.LookupEnv(key); !exists {
				if err := os.Setenv(key, entry.value); err != nil {
					return fmt.Errorf("restore environment entry %q: %w", key, err)
				}
			}
		}
	}
	applied.keys = previous
	return nil
}

// LookupApplied exposes only explicitly injected keys to an instance's
// environment-backed integrations. Other host variables never become settings.
func LookupApplied(key string) (string, bool) {
	applied.RLock()
	_, allowed := applied.keys[key]
	applied.RUnlock()
	if !allowed {
		return "", false
	}
	return os.LookupEnv(key)
}
