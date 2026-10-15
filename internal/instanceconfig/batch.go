package instanceconfig

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

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
		return err
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
