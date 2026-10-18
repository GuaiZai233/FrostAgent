package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type GroupSnapshot struct {
	Platform string
	GroupID  string
	Profile  *GroupProfile
	Entries  []MemoryEntry
	Archives []MemoryMergeArchive
}

type Snapshot struct {
	PrivateEntries  []MemoryEntry
	PrivateArchives []MemoryMergeArchive
	Groups          []GroupSnapshot
}

type ImportResult struct {
	Imported int
	Skipped  int
}

type preparedGroup struct {
	store   *GroupStore
	profile *GroupProfile
	brain   *BrainData
}

// ImportSQLSnapshot restores all memory scopes in one transaction. Merge
// skips matching IDs and keeps existing profile fields on conflict.
func ImportSQLSnapshot(db *storage.DB, instanceID string, snapshot Snapshot, merge bool) (ImportResult, error) {
	result := ImportResult{}
	private := NewSQLStore(db, instanceID)
	privateBrain := &BrainData{Entries: snapshot.PrivateEntries, MergeArchives: snapshot.PrivateArchives}
	if merge {
		existing, err := private.load()
		if err != nil {
			return result, err
		}
		privateBrain = mergeBrain(existing, privateBrain, &result)
	} else if err := validateBrain(privateBrain); err != nil {
		return result, err
	} else {
		result.Imported += len(privateBrain.Entries)
	}
	manager := NewSQLGroupManager(db, instanceID, nil)
	prepared := make([]preparedGroup, 0, len(snapshot.Groups))
	seenGroups := make(map[string]bool)
	for _, group := range snapshot.Groups {
		store, err := manager.GetGroupStoreForPlatform(group.Platform, group.GroupID)
		if err != nil {
			return ImportResult{}, err
		}
		key := store.platform + "\x00" + store.groupID
		if seenGroups[key] {
			return ImportResult{}, fmt.Errorf("duplicate group %s/%s", store.platform, store.groupID)
		}
		seenGroups[key] = true
		profile := group.Profile
		if profile == nil {
			profile = &GroupProfile{GroupID: store.groupID, Members: map[string]*MemberProfile{}}
		}
		if profile.GroupID != "" && CanonicalGroupID(profile.GroupID) != store.groupID {
			return ImportResult{}, fmt.Errorf("group profile identity mismatch: %s", group.GroupID)
		}
		profile.GroupID = store.groupID
		if profile.Members == nil {
			profile.Members = map[string]*MemberProfile{}
		}
		brain := &BrainData{Entries: group.Entries, MergeArchives: group.Archives}
		for i := range brain.Entries {
			brain.Entries[i].ScopeType = ScopeGroup
			brain.Entries[i].GroupID = store.groupID
		}
		if merge {
			existing, err := store.sql.load()
			if err != nil {
				return ImportResult{}, err
			}
			brain = mergeBrain(existing, brain, &result)
			existingProfile, err := store.GetProfile()
			if err != nil {
				return ImportResult{}, err
			}
			if existingProfile.GroupName != "" {
				profile.GroupName = existingProfile.GroupName
			}
			for id, member := range existingProfile.Members {
				profile.Members[id] = member
			}
		} else if err := validateBrain(brain); err != nil {
			return ImportResult{}, err
		} else {
			result.Imported += len(brain.Entries)
		}
		prepared = append(prepared, preparedGroup{store: store, profile: profile, brain: brain})
	}
	ctx := context.Background()
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	var present int
	if err := tx.QueryRowContext(ctx, db.Bind(`SELECT COUNT(*) FROM instances WHERE id = ? AND deleting = 0`), instanceID).Scan(&present); err != nil {
		return ImportResult{}, err
	}
	if present != 1 {
		return ImportResult{}, sql.ErrNoRows
	}
	if !merge {
		for _, table := range []string{"memory_entries", "memory_merge_archives", "group_profiles", "memory_catalogs"} {
			if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM `+table+` WHERE instance_id = ?`), instanceID); err != nil {
				return ImportResult{}, err
			}
		}
	}
	if err := private.sql.saveTx(ctx, tx, privateBrain); err != nil {
		return ImportResult{}, err
	}
	if err := rebuildCatalogTx(ctx, db, tx, *private.sql, privateBrain.Entries); err != nil {
		return ImportResult{}, err
	}
	for _, group := range prepared {
		if err := group.store.saveSQLProfileTx(ctx, tx, group.profile); err != nil {
			return ImportResult{}, err
		}
		if err := group.store.sql.saveTx(ctx, tx, group.brain); err != nil {
			return ImportResult{}, err
		}
		if err := rebuildCatalogTx(ctx, db, tx, *group.store.sql, group.brain.Entries); err != nil {
			return ImportResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func validateBrain(brain *BrainData) error {
	ids := make(map[string]bool, len(brain.Entries))
	for _, entry := range brain.Entries {
		if entry.ID == "" || ids[entry.ID] {
			return fmt.Errorf("empty or duplicate memory ID %q", entry.ID)
		}
		ids[entry.ID] = true
	}
	archives := make(map[string]bool, len(brain.MergeArchives))
	for _, archive := range brain.MergeArchives {
		if archive.MergedID == "" || archives[archive.MergedID] {
			return fmt.Errorf("empty or duplicate merge archive ID %q", archive.MergedID)
		}
		archives[archive.MergedID] = true
	}
	return nil
}

func mergeBrain(existing, incoming *BrainData, result *ImportResult) *BrainData {
	entries := make(map[string]bool, len(existing.Entries))
	for _, entry := range existing.Entries {
		entries[entry.ID] = true
	}
	for _, entry := range incoming.Entries {
		if entry.ID == "" || entries[entry.ID] {
			result.Skipped++
			continue
		}
		entries[entry.ID] = true
		existing.Entries = append(existing.Entries, entry)
		result.Imported++
	}
	archives := make(map[string]bool, len(existing.MergeArchives))
	for _, archive := range existing.MergeArchives {
		archives[archive.MergedID] = true
	}
	for _, archive := range incoming.MergeArchives {
		if archive.MergedID == "" || archives[archive.MergedID] {
			continue
		}
		archives[archive.MergedID] = true
		existing.MergeArchives = append(existing.MergeArchives, archive)
	}
	return existing
}

func rebuildCatalogTx(ctx context.Context, db *storage.DB, tx *sql.Tx, scope sqlBrainStore, entries []MemoryEntry) error {
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM memory_catalogs
		WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ?`),
		scope.instanceID, scope.scope, scope.platform, scope.groupID); err != nil {
		return err
	}
	catalogs := make(map[string]*UserMemoryCatalog)
	tags := make(map[string]map[string]bool)
	for _, entry := range entries {
		owner := CanonicalOwner(entry.Owner)
		if scope.scope == ScopeGroup {
			owner = scope.groupID
		}
		catalog := catalogs[owner]
		if catalog == nil {
			catalog = &UserMemoryCatalog{Owner: owner, GeneratedAt: time.Now().UTC()}
			catalogs[owner] = catalog
			tags[owner] = map[string]bool{}
		}
		catalog.MemoryCount++
		for _, tag := range entry.Tags {
			if clean := sanitizeTopicText(tag); clean != "" {
				tags[owner][clean] = true
			}
		}
	}
	for owner, catalog := range catalogs {
		labels := make([]string, 0, len(tags[owner]))
		for label := range tags[owner] {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		if len(labels) > 24 {
			labels = labels[:24]
		}
		for _, label := range labels {
			catalog.Topics = append(catalog.Topics, MemoryTopic{Name: label})
		}
		raw, err := json.Marshal(catalog)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO memory_catalogs
			(instance_id, scope_type, platform, group_id, owner, catalog_json)
			VALUES (?, ?, ?, ?, ?, ?)`), scope.instanceID, scope.scope, scope.platform, scope.groupID,
			owner, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
