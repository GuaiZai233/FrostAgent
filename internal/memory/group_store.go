package memory

import (
	"FrostAgent/internal/core"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// CanonicalGroupID strips common IM platform prefixes from a group ID.
func CanonicalGroupID(raw string) string {
	s := strings.TrimSpace(raw)
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
	return strings.TrimSpace(s)
}

func isWindowsReservedDeviceName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	switch upper {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

// SafeGroupKey converts any group identifier into an injective, Windows-safe, traversal-proof directory name.
// It uses a deterministic SHA-256 hash combined with an alphanumeric prefix to guarantee 1:1 mapping (injective)
// without collisions between distinct IDs (e.g. "abc:def" vs "abc/def" vs "abc_def").
// It also guarantees no Windows reserved device names, no path traversal (".."), and no trailing dots/spaces.
func SafeGroupKey(groupID string) string {
	canon := CanonicalGroupID(groupID)
	if canon == "" {
		return "unknown_group"
	}

	h := sha256.Sum256([]byte(canon))
	hashHex := hex.EncodeToString(h[:])[:16]

	// Build a readable alphanumeric prefix (ASCII only, up to 24 chars)
	var sb strings.Builder
	for _, r := range canon {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
		if sb.Len() >= 24 {
			break
		}
	}
	rawPrefix := strings.Trim(sb.String(), "_.")
	if rawPrefix == "" || isWindowsReservedDeviceName(rawPrefix) {
		rawPrefix = "grp"
	}

	return fmt.Sprintf("g_%s_%s", rawPrefix, hashHex)
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
	canon := CanonicalGroupID(groupID)
	if canon == "" {
		return nil, errors.New("group_id cannot be empty")
	}
	safeKey := SafeGroupKey(canon)
	groupsBase := filepath.Clean(filepath.Join(baseDir, "groups"))
	groupDir := filepath.Clean(filepath.Join(groupsBase, safeKey))

	rel, err := filepath.Rel(groupsBase, groupDir)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return nil, fmt.Errorf("group directory escapes groups boundary: %s", groupDir)
	}

	// Transparently upgrade legacy directory if it exists and new directory doesn't
	oldDir := filepath.Join(groupsBase, canon)
	if _, err := os.Stat(oldDir); err == nil {
		if _, err := os.Stat(groupDir); os.IsNotExist(err) {
			_ = os.Rename(oldDir, groupDir)
		}
	}

	if err := os.MkdirAll(groupDir, 0755); err != nil {
		return nil, fmt.Errorf("create group storage dir: %w", err)
	}

	catalogPath := filepath.Join(groupDir, "catalog.json")
	store := &GroupStore{
		groupID:     canon,
		dir:         groupDir,
		profilePath: filepath.Join(groupDir, "profile.json"),
		memoryPath:  filepath.Join(groupDir, "memory.json"),
		catalogPath: catalogPath,
		routes:      make(map[string]core.RouteContext),
		catalog:     NewCatalogStore(catalogPath),
	}

	// Verify immutable identity on disk if profile exists
	profile, err := store.loadProfileLocked()
	if err == nil {
		if profile.GroupID != "" {
			if CanonicalGroupID(profile.GroupID) != canon {
				return nil, fmt.Errorf("group profile identity mismatch: expected %s, found %s", canon, profile.GroupID)
			}
		} else {
			profile.GroupID = canon
		}
		if _, statErr := os.Stat(store.profilePath); os.IsNotExist(statErr) {
			_ = store.saveProfileLocked(profile)
		}
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
				GroupID:   s.groupID,
				Members:   make(map[string]*MemberProfile),
				UpdatedAt: time.Now(),
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
	return &profile, nil
}

// saveProfileLocked writes the group profile to disk.
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

// loadMemoryLocked reads the group memory entries from disk.
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

// saveMemoryLocked writes the group memory data to disk atomically.
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

// IncrementAccessCount bumps the access count and updates the timestamp for the specified memory IDs.
func (s *GroupStore) IncrementAccessCount(memoryIDs ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}

	targetMap := make(map[string]bool, len(memoryIDs))
	for _, id := range memoryIDs {
		if id != "" {
			targetMap[id] = true
		}
	}
	if len(targetMap) == 0 {
		return nil
	}

	now := time.Now()
	changed := false
	for i, entry := range brain.Entries {
		if targetMap[entry.ID] {
			brain.Entries[i].AccessCount++
			brain.Entries[i].UpdatedAt = now
			changed = true
		}
	}

	if !changed {
		return nil
	}
	return s.saveMemoryLocked(brain)
}

// RecordRecall increments the access count and updates the timestamp for recalled memories.
func (s *GroupStore) RecordRecall(entries []MemoryEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	return s.IncrementAccessCount(ids...)
}

// SaveMergeArchive saves a merge archive to the group store.
func (s *GroupStore) SaveMergeArchive(archive MemoryMergeArchive) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return err
	}
	brain.MergeArchives = append(brain.MergeArchives, archive)
	return s.saveMemoryLocked(brain)
}

// ListMergeArchives returns all merge archives in the group store.
func (s *GroupStore) ListMergeArchives() ([]MemoryMergeArchive, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return nil, err
	}
	return brain.MergeArchives, nil
}

// applyReflectionWithMerges applies validated merge proposals and deletes outdated entries for this group.
func (s *GroupStore) applyReflectionWithMerges(
	merges []validatedMerge,
	outdatedIDs []string,
) (reflectionApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	brain, err := s.loadMemoryLocked()
	if err != nil {
		return reflectionApplyResult{}, err
	}

	outdated := make(map[string]bool, len(outdatedIDs))
	for _, id := range outdatedIDs {
		outdated[id] = true
	}

	// A source mentioned by a merge must never be deleted as outdated in the same cycle
	for _, merge := range merges {
		for _, source := range merge.Sources {
			delete(outdated, source.ID)
		}
	}

	currentByID := make(map[string]MemoryEntry, len(brain.Entries))
	existingIDs := make(map[string]bool, len(brain.Entries))
	for _, entry := range brain.Entries {
		currentByID[entry.ID] = entry
		existingIDs[entry.ID] = true
	}

	now := time.Now()
	consumed := make(map[string]bool)
	mergedEntries := make([]MemoryEntry, 0, len(merges))
	archives := make([]MemoryMergeArchive, 0, len(merges))
	for _, merge := range merges {
		if len(merge.Sources) < 2 || len(merge.Sources) > maxMergeSources ||
			strings.TrimSpace(merge.Content) == "" || len([]rune(merge.Content)) > maxMergedContent ||
			len(merge.Tags) == 0 || len(merge.Tags) > maxMergedTags {
			continue
		}

		currentSources := make([]MemoryEntry, 0, len(merge.Sources))
		seenSources := make(map[string]bool, len(merge.Sources))
		valid := true
		for _, snapshot := range merge.Sources {
			current, ok := currentByID[snapshot.ID]
			if !ok || seenSources[current.ID] || consumed[current.ID] ||
				!sameMergeSource(current, snapshot) {
				valid = false
				break
			}
			seenSources[current.ID] = true
			currentSources = append(currentSources, current)
		}
		if !valid {
			continue
		}

		mergedID := generateID()
		for existingIDs[mergedID] {
			mergedID = generateID()
		}
		existingIDs[mergedID] = true

		mergedOwner := GroupOwnerExplicit
		if len(currentSources) > 0 {
			firstOwner := currentSources[0].Owner
			allSame := true
			for _, cs := range currentSources[1:] {
				if cs.Owner != firstOwner {
					allSame = false
					break
				}
			}
			if allSame && firstOwner != "" {
				mergedOwner = firstOwner
			}
		}

		merged := buildMergedEntry(mergedID, mergedOwner, merge, currentSources, now)
		merged.ScopeType = ScopeGroup
		merged.GroupID = s.groupID
		mergedEntries = append(mergedEntries, merged)
		archives = append(archives, MemoryMergeArchive{
			MergedID: mergedID,
			Owner:    mergedOwner,
			Sources:  currentSources,
			MergedAt: now,
		})
		for _, source := range currentSources {
			consumed[source.ID] = true
		}
	}

	remaining := make([]MemoryEntry, 0, len(brain.Entries))
	removedIDs := make([]string, 0, len(consumed)+len(outdated))
	actualOutdated := make([]string, 0, len(outdated))
	for _, entry := range brain.Entries {
		if consumed[entry.ID] {
			removedIDs = append(removedIDs, entry.ID)
			continue
		}
		if outdated[entry.ID] {
			removedIDs = append(removedIDs, entry.ID)
			actualOutdated = append(actualOutdated, entry.ID)
			continue
		}
		remaining = append(remaining, entry)
	}

	remaining = append(remaining, mergedEntries...)
	brain.Entries = remaining
	brain.MergeArchives = append(brain.MergeArchives, archives...)

	if err := s.saveMemoryLocked(brain); err != nil {
		return reflectionApplyResult{}, err
	}

	mergedSourceCount := 0
	for _, archive := range archives {
		mergedSourceCount += len(archive.Sources)
	}

	return reflectionApplyResult{
		Remaining:         remaining,
		RemovedIDs:        removedIDs,
		OutdatedIDs:       actualOutdated,
		MergedEntries:     mergedEntries,
		MergedSourceCount: mergedSourceCount,
	}, nil
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
