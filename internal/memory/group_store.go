package memory

import (
	"FrostAgent/internal/core"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SafeGroupKey converts any group identifier into a Windows-safe directory name.
// Strips prefixes like "group:", "qq:group:", etc., and replaces reserved filesystem
// characters (<>:"/\|?*) with underscores.
func SafeGroupKey(groupID string) string {
	s := strings.TrimSpace(groupID)
	for _, prefix := range []string{
		"aiocqhttp:group:",
		"onebot:group:",
		"qq:group:",
		"astrbot:group:",
		"group:",
	} {
		if cut, ok := strings.CutPrefix(s, prefix); ok {
			s = cut
			break
		}
	}
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			sb.WriteRune('_')
		default:
			if r < 32 {
				sb.WriteRune('_')
			} else {
				sb.WriteRune(r)
			}
		}
	}
	res := strings.TrimSpace(sb.String())
	if res == "" {
		return "unknown_group"
	}
	return res
}

// GroupStore manages independent persistent storage for a single QQ group.
// It stores group profile, group memory entries, and group topic catalog
// in an isolated directory under groups/<safe_group_key>/.
type GroupStore struct {
	groupID     string
	dir         string
	profilePath string
	memoryPath  string
	catalogPath string
	mu          sync.RWMutex
	routeMu     sync.RWMutex
	routes      map[string]core.RouteContext
	catalog     *CatalogStore
}

// NewGroupStore creates a GroupStore for the given groupID under baseDir.
func NewGroupStore(baseDir, groupID string) (*GroupStore, error) {
	safeKey := SafeGroupKey(groupID)
	groupDir := filepath.Join(baseDir, "groups", safeKey)
	if err := os.MkdirAll(groupDir, 0755); err != nil {
		return nil, fmt.Errorf("create group storage dir: %w", err)
	}

	catalogPath := filepath.Join(groupDir, "catalog.json")
	store := &GroupStore{
		groupID:     groupID,
		dir:         groupDir,
		profilePath: filepath.Join(groupDir, "profile.json"),
		memoryPath:  filepath.Join(groupDir, "memory.json"),
		catalogPath: catalogPath,
		routes:      make(map[string]core.RouteContext),
		catalog:     NewCatalogStore(catalogPath),
	}
	return store, nil
}

// GroupID returns the logical group ID.
func (s *GroupStore) GroupID() string {
	return s.groupID
}

// CatalogStore returns the isolated topic catalog store for this group.
func (s *GroupStore) CatalogStore() *CatalogStore {
	return s.catalog
}

// atomicWriteFile writes data to a temporary file in the same directory and renames it.
func (s *GroupStore) atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, "atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for atomic write: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// loadProfile reads the group profile from disk.
func (s *GroupStore) loadProfileLocked() (*GroupProfile, error) {
	data, err := os.ReadFile(s.profilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &GroupProfile{
				GroupID: s.groupID,
				Members: make(map[string]*MemberProfile),
			}, nil
		}
		return nil, fmt.Errorf("read group profile: %w", err)
	}
	var profile GroupProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parse group profile: %w", err)
	}
	if profile.Members == nil {
		profile.Members = make(map[string]*MemberProfile)
	}
	if profile.GroupID == "" {
		profile.GroupID = s.groupID
	}
	return &profile, nil
}

// saveProfile writes the group profile to disk atomically.
func (s *GroupStore) saveProfileLocked(profile *GroupProfile) error {
	profile.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal group profile: %w", err)
	}
	return s.atomicWriteFile(s.profilePath, data)
}

// GetProfile returns a copy of the current group profile.
func (s *GroupStore) GetProfile() (*GroupProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadProfileLocked()
}

// ObserveMember records or refreshes an observed speaking member in the group.
// It never overwrites existing PreferredName or Aliases when nickname changes.
func (s *GroupStore) ObserveMember(userID, nickname, card, role, source string) (*MemberProfile, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, errors.New("empty user_id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := s.loadProfileLocked()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	member, exists := profile.Members[userID]
	if !exists {
		member = &MemberProfile{
			UserID:      userID,
			Nickname:    strings.TrimSpace(nickname),
			Card:        strings.TrimSpace(card),
			Role:        NormalizeGroupRole(role),
			Source:      source,
			CreatedAt:   now,
			UpdatedAt:   now,
			LastSpokeAt: now,
		}
		profile.Members[userID] = member
	} else {
		member.LastSpokeAt = now
		member.UpdatedAt = now
		if strings.TrimSpace(nickname) != "" {
			member.Nickname = strings.TrimSpace(nickname)
		}
		if strings.TrimSpace(card) != "" {
			member.Card = strings.TrimSpace(card)
		}
		if r := NormalizeGroupRole(role); r != GroupRoleUnknown {
			member.Role = r
		}
		if source != "" && member.Source == "" {
			member.Source = source
		}
	}

	if err := s.saveProfileLocked(profile); err != nil {
		return nil, err
	}
	return member, nil
}

// UpdateGroupName updates the persistent group name if non-empty.
func (s *GroupStore) UpdateGroupName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := s.loadProfileLocked()
	if err != nil {
		return err
	}

	if profile.GroupName == name {
		return nil
	}
	profile.GroupName = name
	return s.saveProfileLocked(profile)
}

// UpdateMemberRole updates the role of a group member.
func (s *GroupStore) UpdateMemberRole(userID string, role GroupRole) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("empty user_id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := s.loadProfileLocked()
	if err != nil {
		return err
	}

	member, exists := profile.Members[userID]
	if !exists {
		member = &MemberProfile{
			UserID:    userID,
			Role:      role,
			CreatedAt: time.Now(),
		}
		profile.Members[userID] = member
	}
	member.Role = role
	member.UpdatedAt = time.Now()
	return s.saveProfileLocked(profile)
}

// UpdateMemberPreferredName updates the preferred name and aliases of a group member.
func (s *GroupStore) UpdateMemberPreferredName(userID, preferredName string, aliases []string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("empty user_id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	profile, err := s.loadProfileLocked()
	if err != nil {
		return err
	}

	member, exists := profile.Members[userID]
	if !exists {
		member = &MemberProfile{
			UserID:    userID,
			Role:      GroupRoleUnknown,
			CreatedAt: time.Now(),
		}
		profile.Members[userID] = member
	}
	member.PreferredName = strings.TrimSpace(preferredName)
	cleanAliases := make([]string, 0, len(aliases))
	seen := make(map[string]bool)
	for _, a := range aliases {
		trimmed := strings.TrimSpace(a)
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			cleanAliases = append(cleanAliases, trimmed)
		}
	}
	member.Aliases = cleanAliases
	member.UpdatedAt = time.Now()
	return s.saveProfileLocked(profile)
}

// loadMemory reads the group memory entries from disk.
func (s *GroupStore) loadMemoryLocked() (*BrainData, error) {
	data, err := os.ReadFile(s.memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &BrainData{Entries: []MemoryEntry{}}, nil
		}
		return nil, fmt.Errorf("read group memory: %w", err)
	}
	var brain BrainData
	if err := json.Unmarshal(data, &brain); err != nil {
		return nil, fmt.Errorf("parse group memory: %w", err)
	}
	for i := range brain.Entries {
		brain.Entries[i].ScopeType = ScopeGroup
		brain.Entries[i].GroupID = s.groupID
		brain.Entries[i].Owner = CanonicalOwner(brain.Entries[i].Owner)
	}
	return &brain, nil
}

// saveMemory writes the group memory data to disk atomically.
func (s *GroupStore) saveMemoryLocked(brain *BrainData) error {
	data, err := json.MarshalIndent(brain, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal group memory: %w", err)
	}
	return s.atomicWriteFile(s.memoryPath, data)
}

// SaveGroupEntriesConditionallyContext atomically persists new entries with barriers and validator checks.
func (s *GroupStore) SaveGroupEntriesConditionallyContext(
	ctx context.Context,
	entries []MemoryEntry,
	validator func() bool,
) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if ctx != nil && ctx.Err() != nil {
		return ErrConditionFailed
	}
	if validator != nil && !validator() {
		return ErrConditionFailed
	}

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier != nil {
		if !barrier.TryBeginCommit() {
			return ErrConditionFailed
		}
		defer barrier.EndCommit()
	}

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}

	now := time.Now()
	for i := range entries {
		entries[i].ScopeType = ScopeGroup
		entries[i].GroupID = s.groupID
		entries[i].UpdatedAt = now
		if entries[i].CreatedAt.IsZero() {
			entries[i].CreatedAt = now
		}
		if entries[i].Owner == "" {
			entries[i].Owner = GroupOwnerExplicit
		} else {
			entries[i].Owner = CanonicalOwner(entries[i].Owner)
		}
		brain.Entries = append(brain.Entries, entries[i])
	}

	return s.saveMemoryLocked(brain)
}

// Save writes a single memory entry to the group store.
func (s *GroupStore) Save(entry MemoryEntry) error {
	return s.SaveGroupEntriesConditionallyContext(context.Background(), []MemoryEntry{entry}, nil)
}

// Search performs a keyword search across group memories.
func (s *GroupStore) Search(query string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, err
	}

	queryLower := strings.ToLower(query)
	var results []MemoryEntry
	for _, entry := range brain.Entries {
		if matchEntry(entry, queryLower) {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

// SearchByTags searches group memories by tags with scoring.
func (s *GroupStore) SearchByTags(tags []string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, err
	}

	type scored struct {
		entry MemoryEntry
		score float64
	}

	var scoredEntries []scored
	for _, entry := range brain.Entries {
		sc := tagMatchScore(entry, tags)
		if sc > 0 {
			scoredEntries = append(scoredEntries, scored{entry: entry, score: sc})
		}
	}

	sort.SliceStable(scoredEntries, func(i, j int) bool {
		return scoredEntries[i].score > scoredEntries[j].score
	})

	results := make([]MemoryEntry, 0, len(scoredEntries))
	for _, se := range scoredEntries {
		results = append(results, se.entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

// ListByOwner returns group memories matching the specified owner.
func (s *GroupStore) ListByOwner(owner string) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, err
	}

	var results []MemoryEntry
	for _, entry := range brain.Entries {
		if OwnersMatch(entry.Owner, owner) {
			results = append(results, entry)
		}
	}
	return results, nil
}

// ListAll returns all memories in the group store.
func (s *GroupStore) ListAll() ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, err
	}
	return brain.Entries, nil
}

// Delete removes a memory entry by ID.
func (s *GroupStore) Delete(memoryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}

	for i, entry := range brain.Entries {
		if entry.ID == memoryID {
			brain.Entries = append(brain.Entries[:i], brain.Entries[i+1:]...)
			return s.saveMemoryLocked(brain)
		}
	}
	return fmt.Errorf("memory %s not found in group %s", memoryID, s.groupID)
}

// UpdateEntry updates an existing memory entry's content and tags.
func (s *GroupStore) UpdateEntry(entry MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}

	for i, e := range brain.Entries {
		if e.ID == entry.ID {
			brain.Entries[i].Content = entry.Content
			brain.Entries[i].Tags = entry.Tags
			brain.Entries[i].UpdatedAt = time.Now()
			return s.saveMemoryLocked(brain)
		}
	}
	return fmt.Errorf("memory %s not found in group %s", entry.ID, s.groupID)
}

// RecordRecall increments access count and updates access time for entries.
func (s *GroupStore) RecordRecall(entries []MemoryEntry) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}

	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.ID] = true
	}

	now := time.Now()
	for i := range brain.Entries {
		if ids[brain.Entries[i].ID] {
			brain.Entries[i].AccessCount++
			brain.Entries[i].UpdatedAt = now
		}
	}
	return s.saveMemoryLocked(brain)
}

// ApplyReflection applies reflection deletions atomically for group memory.
func (s *GroupStore) ApplyReflection(owner string, outdatedIDs []string) ([]MemoryEntry, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, nil, err
	}

	outdated := make(map[string]bool, len(outdatedIDs))
	for _, id := range outdatedIDs {
		outdated[id] = true
	}

	remaining := make([]MemoryEntry, 0, len(brain.Entries))
	var removed []string
	for _, entry := range brain.Entries {
		if outdated[entry.ID] {
			removed = append(removed, entry.ID)
		} else {
			remaining = append(remaining, entry)
		}
	}
	brain.Entries = remaining
	if err := s.saveMemoryLocked(brain); err != nil {
		return nil, nil, err
	}
	return remaining, removed, nil
}

// ExportData exports the group memory data.
func (s *GroupStore) ExportData() (ExportData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return ExportData{}, err
	}
	return ExportData{
		Version:    1,
		Entries:    brain.Entries,
		ExportedAt: time.Now(),
	}, nil
}

// ImportData imports memory data into the group store.
func (s *GroupStore) ImportData(data ExportData, overwrite bool) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return 0, 0, err
	}

	existingByID := make(map[string]int, len(brain.Entries))
	for i, e := range brain.Entries {
		existingByID[e.ID] = i
	}

	imported, skipped := 0, 0
	now := time.Now()
	for _, entry := range data.Entries {
		entry.ScopeType = ScopeGroup
		entry.GroupID = s.groupID
		if entry.Owner == "" {
			entry.Owner = GroupOwnerExplicit
		} else {
			entry.Owner = CanonicalOwner(entry.Owner)
		}
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		entry.UpdatedAt = now

		if idx, exists := existingByID[entry.ID]; exists {
			if overwrite {
				brain.Entries[idx] = entry
				imported++
			} else {
				skipped++
			}
		} else {
			brain.Entries = append(brain.Entries, entry)
			existingByID[entry.ID] = len(brain.Entries) - 1
			imported++
		}
	}

	if err := s.saveMemoryLocked(brain); err != nil {
		return 0, 0, err
	}
	return imported, skipped, nil
}

// RouteForOwner returns the remembered routing context for an owner.
func (s *GroupStore) RouteForOwner(owner string) core.RouteContext {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.routes[CanonicalOwner(owner)]
}

// RememberRoute records the routing context for an owner.
func (s *GroupStore) RememberRoute(owner string, route core.RouteContext) {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	s.routes[CanonicalOwner(owner)] = route
}
