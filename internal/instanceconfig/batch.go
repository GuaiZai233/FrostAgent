package instanceconfig

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var ErrOutcomeUnknown = errors.New("settings commit outcome is unknown")
var ErrStateUnavailable = errors.New("settings state cannot be confirmed; restore database connectivity and restart the backend before verification")

type DatabaseChange struct {
	Store      *Store
	Key, Value string
	Clear      bool
}

// ApplyDatabaseChanges commits instance and global changes in one transaction,
// then updates their synchronized runtime caches before releasing the locks.
func ApplyDatabaseChanges(ctx context.Context, changes []DatabaseChange) error {
	if len(changes) == 0 {
		return nil
	}
	if changes[0].Store == nil || changes[0].Store.db == nil {
		return fmt.Errorf("SQL settings are unavailable")
	}
	db := changes[0].Store.db
	stores := make(map[string]*Store)
	seen := make(map[string]bool)
	for _, change := range changes {
		s := change.Store
		if s == nil || s.db != db || !allowed(change.Key, s.global) || strings.ContainsRune(change.Value, 0) {
			return fmt.Errorf("invalid scoped setting change %s", change.Key)
		}
		scope := s.dbScope + "\x00" + s.dbID
		if previous := stores[scope]; previous != nil && previous != s {
			return fmt.Errorf("duplicate settings store for scope %s", s.dbScope)
		}
		stores[scope] = s
		identity := scope + "\x00" + change.Key
		if seen[identity] {
			return fmt.Errorf("duplicate setting %s", change.Key)
		}
		seen[identity] = true
	}
	ordered := make([]string, 0, len(stores))
	for scope := range stores {
		ordered = append(ordered, scope)
	}
	sort.Strings(ordered)
	for _, scope := range ordered {
		stores[scope].mu.Lock()
	}
	defer func() {
		for index := len(ordered) - 1; index >= 0; index-- {
			stores[ordered[index]].mu.Unlock()
		}
	}()
	for _, scope := range ordered {
		if stores[scope].unconfirmed {
			return ErrStateUnavailable
		}
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, change := range changes {
		s := change.Store
		if change.Clear {
			_, err = tx.ExecContext(ctx, db.Bind(`DELETE FROM settings WHERE scope = ? AND instance_id = ? AND key = ?`), s.dbScope, s.dbID, change.Key)
		} else {
			_, err = tx.ExecContext(ctx, db.Bind(`INSERT INTO settings(scope, instance_id, key, value) VALUES (?, ?, ?, ?)
				ON CONFLICT (scope, instance_id, key) DO UPDATE SET value = excluded.value`), s.dbScope, s.dbID, change.Key, change.Value)
		}
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return reconcileCommitError(stores, err)
	}
	for _, change := range changes {
		if change.Clear {
			delete(change.Store.values, change.Key)
		} else {
			change.Store.values[change.Key] = change.Value
		}
		if botLoopSetting(change.Key) {
			change.Store.botLoopRevision++
		}
	}
	return nil
}

// The transaction may have committed even if its acknowledgement was lost.
// Read every affected scope in one SQL snapshot, independently of the request's
// cancelled context, while retaining the store locks and the gateway's before
// snapshots. Only a complete readback may update caches and trigger hot apply.
func reconcileCommitError(stores map[string]*Store, commitErr error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conditions := make([]string, 0, len(stores))
	args := make([]any, 0, len(stores)*2)
	values := make(map[string]map[string]string, len(stores))
	var first *Store
	for scope, store := range stores {
		first = store
		store.unconfirmed = true
		conditions = append(conditions, "(scope = ? AND instance_id = ?)")
		args = append(args, store.dbScope, store.dbID)
		values[scope] = make(map[string]string)
	}
	err := func() error {
		rows, err := first.db.SQL.QueryContext(ctx, first.db.Bind(
			"SELECT scope, instance_id, key, value FROM settings WHERE "+strings.Join(conditions, " OR ")), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var scope, id, key, value string
			if err := rows.Scan(&scope, &id, &key, &value); err != nil {
				return err
			}
			identity := scope + "\x00" + id
			store := stores[identity]
			if store == nil || !allowed(key, store.global) {
				return errors.New("invalid settings readback")
			}
			values[identity][key] = value
		}
		return rows.Err()
	}()
	if err != nil {
		return errors.Join(ErrOutcomeUnknown, ErrStateUnavailable)
	}
	for scope, store := range stores {
		for _, key := range []string{"BOT_LOOP_ENABLED", "BOT_LOOP_USER_IDS", "BOT_LOOP_MAX_TURNS", "BOT_LOOP_MODE"} {
			if store.values[key] != values[scope][key] {
				store.botLoopRevision++
				break
			}
		}
		store.values = values[scope]
		store.unconfirmed = false
	}
	return fmt.Errorf("%w: %v", ErrOutcomeUnknown, commitErr)
}
