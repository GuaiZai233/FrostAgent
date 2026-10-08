package memory

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// GroupSummary provides an overview of an observed group for management and statistics.
type GroupSummary struct {
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
	baseDir string
	scope   *runtimescope.Scope
	mu      sync.RWMutex
	groups  map[string]*GroupStore // keyed by SafeGroupKey
}

// NewGroupManager creates a new GroupManager rooted at baseDir.
func NewGroupManager(baseDir string, scope *runtimescope.Scope) *GroupManager {
	return &GroupManager{
		baseDir: baseDir,
		scope:   scope,
		groups:  make(map[string]*GroupStore),
	}
}

// GetGroupStore returns the GroupStore for groupID, loading or creating it on demand.
// GroupID is canonicalized and hashed through SafeGroupKey to guarantee that distinct
// representations of the same group share the exact same underlying GroupStore instance and mutex.
func (m *GroupManager) GetGroupStore(groupID string) (*GroupStore, error) {
	if groupID == "" {
		return nil, fmt.Errorf("group_id cannot be empty")
	}

	canon := CanonicalGroupID(groupID)
	if canon == "" {
		return nil, fmt.Errorf("canonical group_id cannot be empty")
	}

	safeKey := SafeGroupKey(canon)

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

	// Also check if groupID was passed as a safeKey directly and profile exists on disk
	candidateKey := safeKey
	groupsDir := filepath.Join(m.baseDir, "groups")
	if profileData, err := os.ReadFile(filepath.Join(groupsDir, groupID, "profile.json")); err == nil {
		var p GroupProfile
		if err := json.Unmarshal(profileData, &p); err == nil && p.GroupID != "" {
			canon = CanonicalGroupID(p.GroupID)
			candidateKey = SafeGroupKey(canon)
			if store, exists = m.groups[candidateKey]; exists {
				return store, nil
			}
		}
	}

	store, err := NewGroupStore(m.baseDir, canon)
	if err != nil {
		if m.scope != nil {
			m.scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("初始化群 [%s] 存储失败: %v", canon, err))
		}
		return nil, err
	}
	m.groups[candidateKey] = store
	return store, nil
}

// ListGroups scans the groups directory and returns summaries of all stored groups.
func (m *GroupManager) ListGroups() ([]GroupSummary, error) {
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
			resolvedGroupID = dirName
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
	canon := CanonicalGroupID(groupID)
	safeKey := SafeGroupKey(canon)
	groupDir := filepath.Join(m.baseDir, "groups", safeKey)

	m.mu.Lock()
	delete(m.groups, safeKey)
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
