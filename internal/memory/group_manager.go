package memory

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
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
type GroupManager struct {
	baseDir string
	scope   *runtimescope.Scope
	mu      sync.RWMutex
	groups  map[string]*GroupStore
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
func (m *GroupManager) GetGroupStore(groupID string) (*GroupStore, error) {
	if groupID == "" {
		return nil, fmt.Errorf("group_id cannot be empty")
	}

	m.mu.RLock()
	store, exists := m.groups[groupID]
	m.mu.RUnlock()
	if exists {
		return store, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check under write lock
	if store, exists = m.groups[groupID]; exists {
		return store, nil
	}

	store, err := NewGroupStore(m.baseDir, groupID)
	if err != nil {
		if m.scope != nil {
			m.scope.Log().Warn(logs.SYSTEM, fmt.Sprintf("初始化群 [%s] 存储失败: %v", groupID, err))
		}
		return nil, err
	}
	m.groups[groupID] = store
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
		profilePath := filepath.Join(groupsDir, entry.Name(), "profile.json")
		store, err := m.GetGroupStore(entry.Name())
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
			groupID = entry.Name()
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
	safeKey := SafeGroupKey(groupID)
	groupDir := filepath.Join(m.baseDir, "groups", safeKey)

	m.mu.Lock()
	delete(m.groups, groupID)
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
