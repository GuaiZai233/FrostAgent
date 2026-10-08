package memory

import (
	"FrostAgent/internal/logs"
	"FrostAgent/internal/runtimescope"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extractLegacyGroupID extracts a canonical group ID if the entry belongs to a group,
// or returns an empty string if the entry belongs to a private user.
func extractLegacyGroupID(entry MemoryEntry) string {
	if entry.ScopeType == ScopeGroup && entry.GroupID != "" {
		return CanonicalGroupID(entry.GroupID)
	}
	if entry.GroupID != "" {
		return CanonicalGroupID(entry.GroupID)
	}
	if entry.OwnerType == OwnerGroup {
		canon := CanonicalGroupID(entry.Owner)
		if canon != "" {
			return canon
		}
	}
	for _, prefix := range []string{
		"aiocqhttp:group:",
		"onebot:group:",
		"qq:group:",
		"astrbot:group:",
		"group:",
	} {
		if cut, ok := strings.CutPrefix(entry.Owner, prefix); ok {
			canon := CanonicalGroupID(cut)
			if canon != "" {
				return canon
			}
		}
	}
	return ""
}

func extractLegacyArchiveGroupID(archive MemoryMergeArchive) string {
	for _, prefix := range []string{
		"aiocqhttp:group:",
		"onebot:group:",
		"qq:group:",
		"astrbot:group:",
		"group:",
	} {
		if cut, ok := strings.CutPrefix(archive.Owner, prefix); ok {
			canon := CanonicalGroupID(cut)
			if canon != "" {
				return canon
			}
		}
	}
	if len(archive.Sources) > 0 {
		return extractLegacyGroupID(archive.Sources[0])
	}
	return ""
}

// MigrateLegacyGroupMemories migrates pre-existing group memories stored in brain.json
// into their respective isolated GroupStore files (groups/<safe_key>/memory.json).
// The migration is fully idempotent and creates a timestamped backup of brain.json before modifying anything.
func MigrateLegacyGroupMemories(
	baseDir string,
	store *Store,
	groupManager *GroupManager,
	scope *runtimescope.Scope,
) error {
	if store == nil || groupManager == nil {
		return nil
	}

	brainPath := filepath.Join(baseDir, "brain.json")
	rawData, err := os.ReadFile(brainPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read brain.json: %w", err)
	}

	var brain BrainData
	if err := json.Unmarshal(rawData, &brain); err != nil {
		return fmt.Errorf("unmarshal brain.json: %w", err)
	}

	var privateEntries []MemoryEntry
	groupEntries := make(map[string][]MemoryEntry)
	for _, entry := range brain.Entries {
		groupID := extractLegacyGroupID(entry)
		if groupID != "" {
			migrated := entry
			migrated.ScopeType = ScopeGroup
			migrated.GroupID = groupID
			migrated.OwnerType = OwnerGroup
			if strings.HasPrefix(migrated.Owner, "group:") ||
				strings.HasPrefix(migrated.Owner, "qq:group:") ||
				strings.HasPrefix(migrated.Owner, "aiocqhttp:group:") ||
				strings.HasPrefix(migrated.Owner, "onebot:group:") ||
				strings.HasPrefix(migrated.Owner, "astrbot:group:") ||
				migrated.Owner == groupID ||
				migrated.Owner == "" {
				migrated.Owner = GroupOwnerExplicit
			} else {
				migrated.Owner = CanonicalOwner(migrated.Owner)
			}
			groupEntries[groupID] = append(groupEntries[groupID], migrated)
		} else {
			privateEntries = append(privateEntries, entry)
		}
	}

	var privateArchives []MemoryMergeArchive
	groupArchives := make(map[string][]MemoryMergeArchive)
	for _, archive := range brain.MergeArchives {
		groupID := extractLegacyArchiveGroupID(archive)
		if groupID != "" {
			migrated := archive
			migrated.Owner = GroupOwnerExplicit
			groupArchives[groupID] = append(groupArchives[groupID], migrated)
		} else {
			privateArchives = append(privateArchives, archive)
		}
	}

	if len(groupEntries) == 0 && len(groupArchives) == 0 {
		// Nothing to migrate
		return nil
	}

	// Create timestamped backup of brain.json before altering
	backupPath := filepath.Join(baseDir, fmt.Sprintf("brain.json.bak.%d", time.Now().Unix()))
	if err := os.WriteFile(backupPath, rawData, 0600); err != nil {
		return fmt.Errorf("create brain.json backup before migration: %w", err)
	}

	totalMigratedEntries := 0
	for groupID, entries := range groupEntries {
		gStore, err := groupManager.GetGroupStore(groupID)
		if err != nil {
			return fmt.Errorf("get group store for %s during migration: %w", groupID, err)
		}

		existing, err := gStore.ListAll()
		if err != nil {
			return fmt.Errorf("list existing memories for group %s: %w", groupID, err)
		}
		seenIDs := make(map[string]bool, len(existing))
		for _, e := range existing {
			seenIDs[e.ID] = true
		}

		var toAdd []MemoryEntry
		for _, e := range entries {
			if !seenIDs[e.ID] {
				seenIDs[e.ID] = true
				toAdd = append(toAdd, e)
			}
		}

		if len(toAdd) > 0 {
			if err := gStore.SaveGroupEntriesConditionallyContext(context.Background(), toAdd, nil); err != nil {
				return fmt.Errorf("save migrated memories to group %s: %w", groupID, err)
			}
			totalMigratedEntries += len(toAdd)
		}
	}

	for groupID, archives := range groupArchives {
		gStore, err := groupManager.GetGroupStore(groupID)
		if err != nil {
			continue
		}
		existingArchives, _ := gStore.ListMergeArchives()
		seenMerged := make(map[string]bool, len(existingArchives))
		for _, a := range existingArchives {
			seenMerged[a.MergedID] = true
		}
		for _, a := range archives {
			if !seenMerged[a.MergedID] {
				seenMerged[a.MergedID] = true
				_ = gStore.SaveMergeArchive(a)
			}
		}
	}

	// Atomically write updated brain.json containing only private memories
	updatedBrain := BrainData{
		Entries:       privateEntries,
		MergeArchives: privateArchives,
	}
	cleanData, err := json.MarshalIndent(updatedBrain, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal updated brain.json: %w", err)
	}

	tmpFile, err := os.CreateTemp(baseDir, "brain-migration-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for brain.json migration: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(cleanData); err != nil {
		return fmt.Errorf("write temp migrated brain.json: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp migrated brain.json: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp migrated brain.json: %w", err)
	}

	if err := os.Rename(tmpName, brainPath); err != nil {
		return fmt.Errorf("rename migrated brain.json: %w", err)
	}

	if scope != nil {
		scope.Log().InfoWithConsoleSummary(
			logs.SYSTEM,
			fmt.Sprintf("✓ 已成功迁移 %d 条旧群聊记忆至 %d 个独立群存储，原数据已备份至 %s",
				totalMigratedEntries, len(groupEntries), filepath.Base(backupPath)),
			fmt.Sprintf("✓ 已成功迁移 %d 条旧群聊记忆至独立存储", totalMigratedEntries),
		)
	}

	return nil
}
