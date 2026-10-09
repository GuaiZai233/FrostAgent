package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// sqlBrainStore keeps one memory scope in normalized SQL rows. The existing
// Store and GroupStore locks guard each read/modify/write cycle.
type sqlBrainStore struct {
	db         *storage.DB
	instanceID string
	scope      ScopeType
	platform   string
	groupID    string
}

func (b sqlBrainStore) load() (*BrainData, error) {
	ctx := context.Background()
	rows, err := b.db.SQL.QueryContext(ctx, b.db.Bind(`SELECT id, owner, owner_type, content, summary, evidence,
		source_message_id, source_sender_id, source, visibility, created_at, updated_at,
		access_count, merged_from_json FROM memory_entries
		WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? ORDER BY created_at, id`),
		b.instanceID, b.scope, b.platform, b.groupID)
	if err != nil {
		return nil, err
	}
	brain := &BrainData{Entries: []MemoryEntry{}}
	for rows.Next() {
		var entry MemoryEntry
		var ownerType, source, visibility, createdAt, updatedAt, merged string
		if err := rows.Scan(&entry.ID, &entry.Owner, &ownerType, &entry.Content, &entry.Summary,
			&entry.Evidence, &entry.SourceMessageID, &entry.SourceSenderID, &source, &visibility,
			&createdAt, &updatedAt, &entry.AccessCount, &merged); err != nil {
			rows.Close()
			return nil, err
		}
		entry.ScopeType, entry.GroupID = b.scope, b.groupID
		entry.OwnerType, entry.Source, entry.Visibility = OwnerType(ownerType), Source(source), Visibility(visibility)
		entry.Owner = CanonicalOwner(entry.Owner)
		if entry.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("memory %s created_at: %w", entry.ID, err)
		}
		if entry.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("memory %s updated_at: %w", entry.ID, err)
		}
		if err = json.Unmarshal([]byte(merged), &entry.MergedFrom); err != nil {
			rows.Close()
			return nil, err
		}
		brain.Entries = append(brain.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range brain.Entries {
		entry := &brain.Entries[i]
		tags, err := b.db.SQL.QueryContext(ctx, b.db.Bind(`SELECT tag FROM memory_tags
			WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? AND memory_id = ? ORDER BY tag`),
			b.instanceID, b.scope, b.platform, b.groupID, entry.ID)
		if err != nil {
			return nil, err
		}
		for tags.Next() {
			var tag string
			if err := tags.Scan(&tag); err != nil {
				tags.Close()
				return nil, err
			}
			entry.Tags = append(entry.Tags, tag)
		}
		if err := tags.Err(); err != nil {
			tags.Close()
			return nil, err
		}
		tags.Close()
	}
	archives, err := b.db.SQL.QueryContext(ctx, b.db.Bind(`SELECT merged_id, owner, merged_at, sources_json
		FROM memory_merge_archives WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? ORDER BY merged_at, merged_id`),
		b.instanceID, b.scope, b.platform, b.groupID)
	if err != nil {
		return nil, err
	}
	defer archives.Close()
	for archives.Next() {
		var archive MemoryMergeArchive
		var mergedAt, sources string
		if err := archives.Scan(&archive.MergedID, &archive.Owner, &mergedAt, &sources); err != nil {
			return nil, err
		}
		if archive.MergedAt, err = time.Parse(time.RFC3339Nano, mergedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(sources), &archive.Sources); err != nil {
			return nil, err
		}
		brain.MergeArchives = append(brain.MergeArchives, archive)
	}
	return brain, archives.Err()
}

func (b sqlBrainStore) save(brain *BrainData) error {
	ctx := context.Background()
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := []any{b.instanceID, b.scope, b.platform, b.groupID}
	for _, table := range []string{"memory_entries", "memory_merge_archives"} {
		if _, err := tx.ExecContext(ctx, b.db.Bind(`DELETE FROM `+table+` WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ?`), args...); err != nil {
			return err
		}
	}
	for _, entry := range brain.Entries {
		if entry.ID == "" {
			return fmt.Errorf("memory ID cannot be empty")
		}
		merged, err := json.Marshal(entry.MergedFrom)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO memory_entries (
			instance_id, scope_type, platform, group_id, id, owner, owner_type, content, summary, evidence,
			source_message_id, source_sender_id, source, visibility, created_at, updated_at, access_count, merged_from_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			b.instanceID, b.scope, b.platform, b.groupID, entry.ID, entry.Owner, entry.OwnerType,
			entry.Content, entry.Summary, entry.Evidence, entry.SourceMessageID, entry.SourceSenderID,
			entry.Source, entry.Visibility, entry.CreatedAt.UTC().Format(time.RFC3339Nano),
			entry.UpdatedAt.UTC().Format(time.RFC3339Nano), entry.AccessCount, string(merged)); err != nil {
			return err
		}
		for _, tag := range entry.Tags {
			if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO memory_tags
				(instance_id, scope_type, platform, group_id, memory_id, tag) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`), b.instanceID, b.scope, b.platform, b.groupID, entry.ID, tag); err != nil {
				return err
			}
		}
	}
	for _, archive := range brain.MergeArchives {
		sources, err := json.Marshal(archive.Sources)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO memory_merge_archives
			(instance_id, scope_type, platform, group_id, merged_id, owner, merged_at, sources_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), b.instanceID, b.scope, b.platform, b.groupID,
			archive.MergedID, archive.Owner, archive.MergedAt.UTC().Format(time.RFC3339Nano), string(sources)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
