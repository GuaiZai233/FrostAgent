package memory

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GroupSummary provides an overview of an observed group for management and statistics.
type GroupSummary struct {
	Platform    string    `json:"platform,omitempty"`
	GroupID     string    `json:"group_id"`
	GroupName   string    `json:"group_name"`
	MemberCount int       `json:"member_count"`
	MemoryCount int       `json:"memory_count"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GroupManager coordinates all QQ group memory stores and profiles within an instance.
// It synchronizes all access by canonical group ID and safe key, ensuring that aliases
// (e.g. "123456" vs "group:123456" vs "qq:group:123456") resolve to the exact same
// GroupStore pointer and synchronization lock.
type GroupManager struct {
	baseDir    string
	db         *storage.DB
	instanceID string
	scope      *runtimescope.Scope
	mu         sync.RWMutex
	groups     map[string]*GroupStore // keyed by SafeGroupKey
}

// NewGroupManager creates a new GroupManager rooted at baseDir.
func NewGroupManager(baseDir string, scope *runtimescope.Scope) *GroupManager {
	return &GroupManager{
		baseDir: baseDir,
		scope:   scope,
		groups:  make(map[string]*GroupStore),
	}
}

func NewSQLGroupManager(db *storage.DB, instanceID string, scope *runtimescope.Scope) *GroupManager {
	return &GroupManager{db: db, instanceID: instanceID, scope: scope, groups: make(map[string]*GroupStore)}
}

// CarrySQLStoresFrom preserves per-group locks shared with live sessions when a
// runtime is rebuilt over the same instance database.
func (m *GroupManager) CarrySQLStoresFrom(old *GroupManager) {
	if m == nil || old == nil || m == old || m.db == nil || m.db != old.db || m.instanceID != old.instanceID {
		return
	}
	old.mu.RLock()
	defer old.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, store := range old.groups {
		m.groups[key] = store
	}
}

// GetGroupStore returns the GroupStore for groupID, loading or creating it on demand.
// GroupID is canonicalized and hashed through SafeGroupKey to guarantee that distinct
// representations of the same group share the exact same underlying GroupStore instance and mutex.
func (m *GroupManager) GetGroupStore(groupID string) (*GroupStore, error) {
	return m.GetGroupStoreForPlatform("qq", groupID)
}

func (m *GroupManager) GetGroupStoreForPlatform(platform, groupID string) (*GroupStore, error) {
	if groupID == "" {
		return nil, fmt.Errorf("group_id cannot be empty")
	}

	platform = CanonicalPlatform(platform)
	canon := CanonicalGroupID(strings.TrimPrefix(groupID, platform+":group:"))
	if canon == "" {
		return nil, fmt.Errorf("canonical group_id cannot be empty")
	}

	safeKey := platform + ":" + SafeGroupKey(canon)

	m.mu.RLock()
	store, exists := m.groups[safeKey]
	m.mu.RUnlock()
	if exists {
		return store, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check under write lock
	if store, exists = m.groups[safeKey]; exists {
		return store, nil
	}

	var err error
	if m.db != nil {
		store, err = newSQLGroupStore(m.db, m.instanceID, platform, canon)
	} else {
		store, err = NewGroupStore(m.baseDir, canon)
	}
	if err != nil {
		if m.scope != nil {
			m.scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("初始化群 [%s] 存储失败: %v", canon, err))
		}
		return nil, err
	}
	m.groups[safeKey] = store
	return store, nil
}

// ListGroups scans the groups directory and returns summaries of all stored groups.
func (m *GroupManager) ListGroups() ([]GroupSummary, error) {
	if m.db != nil {
		rows, err := m.db.SQL.QueryContext(context.Background(), m.db.Bind(`SELECT platform, group_id FROM group_profiles WHERE instance_id = ?
			UNION SELECT platform, group_id FROM memory_entries WHERE instance_id = ? AND scope_type = 'group'
			UNION SELECT platform, group_id FROM memory_merge_archives WHERE instance_id = ? AND scope_type = 'group'`),
			m.instanceID, m.instanceID, m.instanceID)
		if err != nil {
			return nil, err
		}
		type groupKey struct{ platform, id string }
		var keys []groupKey
		for rows.Next() {
			var key groupKey
			if err := rows.Scan(&key.platform, &key.id); err != nil {
				rows.Close()
				return nil, err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		result := make([]GroupSummary, 0, len(keys))
		for _, key := range keys {
			store, err := m.GetGroupStoreForPlatform(key.platform, key.id)
			if err != nil {
				return nil, err
			}
			profile, err := store.GetProfile()
			if err != nil {
				return nil, err
			}
			memories, err := store.ListAll()
			if err != nil {
				return nil, err
			}
			result = append(result, GroupSummary{Platform: key.platform, GroupID: key.id,
				GroupName: profile.GroupName, MemberCount: len(profile.Members),
				MemoryCount: len(memories), UpdatedAt: profile.UpdatedAt})
		}
		return result, nil
	}
	groupsDir := filepath.Join(m.baseDir, "groups")
	entries, err := os.ReadDir(groupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read groups directory: %w", err)
	}

	var result []GroupSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()
		profilePath := filepath.Join(groupsDir, dirName, "profile.json")

		var resolvedGroupID string
		if data, err := os.ReadFile(profilePath); err == nil {
			var p GroupProfile
			if err := json.Unmarshal(data, &p); err == nil && p.GroupID != "" {
				resolvedGroupID = p.GroupID
			}
		}

		if resolvedGroupID == "" {
			// Check memory.json
			memPath := filepath.Join(groupsDir, dirName, "memory.json")
			if memData, err := os.ReadFile(memPath); err == nil {
				var brain BrainData
				if err := json.Unmarshal(memData, &brain); err == nil && len(brain.Entries) > 0 {
					resolvedGroupID = brain.Entries[0].GroupID
				}
			}
		}

		if resolvedGroupID == "" {
			continue
		}

		store, err := m.GetGroupStore(resolvedGroupID)
		if err != nil {
			continue
		}

		profile, err := store.GetProfile()
		if err != nil {
			continue
		}

		memories, _ := store.ListAll()
		groupID := profile.GroupID
		if groupID == "" {
			groupID = resolvedGroupID
		}

		updatedAt := profile.UpdatedAt
		if fi, err := os.Stat(profilePath); err == nil && updatedAt.IsZero() {
			updatedAt = fi.ModTime()
		}

		result = append(result, GroupSummary{
			GroupID:     groupID,
			GroupName:   profile.GroupName,
			MemberCount: len(profile.Members),
			MemoryCount: len(memories),
			UpdatedAt:   updatedAt,
		})
	}
	return result, nil
}

// DeleteGroup removes persistent storage and cached store for a group.
func (m *GroupManager) DeleteGroup(groupID string) error {
	return m.DeleteGroupForPlatform("qq", groupID)
}

func (m *GroupManager) DeleteGroupForPlatform(platform, groupID string) error {
	canon := CanonicalGroupID(groupID)
	if canon == "" {
		return fmt.Errorf("canonical group_id cannot be empty")
	}
	platform = strings.ToLower(strings.TrimSpace(platform))
	safeKey := platform + ":" + SafeGroupKey(canon)
	if m.db != nil {
		ctx := context.Background()
		tx, err := m.db.SQL.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, statement := range []string{
			`DELETE FROM memory_entries WHERE instance_id = ? AND scope_type = 'group' AND platform = ? AND group_id = ?`,
			`DELETE FROM memory_merge_archives WHERE instance_id = ? AND scope_type = 'group' AND platform = ? AND group_id = ?`,
			`DELETE FROM memory_catalogs WHERE instance_id = ? AND scope_type = 'group' AND platform = ? AND group_id = ?`,
			`DELETE FROM group_profiles WHERE instance_id = ? AND platform = ? AND group_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, m.db.Bind(statement), m.instanceID, platform, canon); err != nil {
				return err
			}
		}
		summaryID := canon
		if platform == "qq" {
			summaryID = "group:" + canon
		}
		if _, err := tx.ExecContext(ctx, m.db.Bind(`DELETE FROM group_summaries
			WHERE instance_id = ? AND platform = ? AND group_id = ?`), m.instanceID, platform, summaryID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		m.mu.Lock()
		delete(m.groups, safeKey)
		m.mu.Unlock()
		return nil
	}
	cacheKey := safeKey
	safeKey = SafeGroupKey(canon)
	groupsBase := filepath.Clean(filepath.Join(m.baseDir, "groups"))
	groupDir := filepath.Clean(filepath.Join(groupsBase, safeKey))

	rel, err := filepath.Rel(groupsBase, groupDir)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return fmt.Errorf("group directory escapes groups boundary: %s", groupDir)
	}

	m.mu.Lock()
	delete(m.groups, cacheKey)
	m.mu.Unlock()

	if err := os.RemoveAll(groupDir); err != nil {
		if m.scope != nil {
			m.scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("删除群 [%s] 目录失败: %v", groupID, err))
		}
		return fmt.Errorf("remove group dir %s: %w", groupDir, err)
	}
	return nil
}

// TotalMemoriesCount returns the aggregate number of memories across all groups.
func (m *GroupManager) TotalMemoriesCount() int {
	groups, err := m.ListGroups()
	if err != nil {
		return 0
	}
	total := 0
	for _, g := range groups {
		total += g.MemoryCount
	}
	return total
}
