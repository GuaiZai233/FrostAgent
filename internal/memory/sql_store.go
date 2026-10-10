package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
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
	entryIndex := make(map[string]int, len(brain.Entries))
	for i := range brain.Entries {
		entryIndex[brain.Entries[i].ID] = i
	}
	tags, err := b.db.SQL.QueryContext(ctx, b.db.Bind(`SELECT memory_id, tag FROM memory_tags
		WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? ORDER BY memory_id, tag`),
		b.instanceID, b.scope, b.platform, b.groupID)
	if err != nil {
		return nil, err
	}
	for tags.Next() {
		var id, tag string
		if err := tags.Scan(&id, &tag); err != nil {
			tags.Close()
			return nil, err
		}
		if i, ok := entryIndex[id]; ok {
			brain.Entries[i].Tags = append(brain.Entries[i].Tags, tag)
		}
	}
	if err := tags.Err(); err != nil {
		tags.Close()
		return nil, err
	}
	tags.Close()
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
	if err := b.saveTx(ctx, tx, brain); err != nil {
		return err
	}
	return tx.Commit()
}

func (b sqlBrainStore) incrementAccessCounts(memoryIDs []string) error {
	ids := make(map[string]struct{}, len(memoryIDs))
	for _, id := range memoryIDs {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for id := range ids {
		if _, err := tx.ExecContext(ctx, b.db.Bind(`UPDATE memory_entries
			SET access_count = access_count + 1, updated_at = ?
			WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? AND id = ?`),
			now, b.instanceID, b.scope, b.platform, b.groupID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (b sqlBrainStore) appendEntries(entries []MemoryEntry) error {
	return b.writeEntries(entries, false)
}

func (b sqlBrainStore) upsertEntries(entries []MemoryEntry) error {
	return b.writeEntries(entries, true)
}

func (b sqlBrainStore) writeEntries(entries []MemoryEntry, update bool) error {
	ctx := context.Background()
	tx, err := b.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, entry := range entries {
		if entry.ID == "" {
			return fmt.Errorf("memory ID cannot be empty")
		}
		merged, err := json.Marshal(entry.MergedFrom)
		if err != nil {
			return err
		}
		if update {
			if _, err = tx.ExecContext(ctx, b.db.Bind(`DELETE FROM memory_tags
				WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ? AND memory_id = ?`),
				b.instanceID, b.scope, b.platform, b.groupID, entry.ID); err != nil {
				return err
			}
		}
		query := `INSERT INTO memory_entries (
			instance_id, scope_type, platform, group_id, id, owner, owner_type, content, summary, evidence,
			source_message_id, source_sender_id, source, visibility, created_at, updated_at, access_count, merged_from_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if update {
			query += ` ON CONFLICT (instance_id, scope_type, platform, group_id, id) DO UPDATE SET
				owner = excluded.owner, owner_type = excluded.owner_type, content = excluded.content,
				summary = excluded.summary, evidence = excluded.evidence,
				source_message_id = excluded.source_message_id, source_sender_id = excluded.source_sender_id,
				source = excluded.source, visibility = excluded.visibility,
				created_at = excluded.created_at, updated_at = excluded.updated_at,
				access_count = excluded.access_count, merged_from_json = excluded.merged_from_json`
		}
		if _, err = tx.ExecContext(ctx, b.db.Bind(query), b.instanceID, b.scope, b.platform, b.groupID,
			entry.ID, entry.Owner, entry.OwnerType, entry.Content, entry.Summary, entry.Evidence,
			entry.SourceMessageID, entry.SourceSenderID, entry.Source, entry.Visibility,
			entry.CreatedAt.UTC().Format(time.RFC3339Nano), entry.UpdatedAt.UTC().Format(time.RFC3339Nano),
			entry.AccessCount, string(merged)); err != nil {
			return err
		}
		for _, tag := range entry.Tags {
			if _, err = tx.ExecContext(ctx, b.db.Bind(`INSERT INTO memory_tags
				(instance_id, scope_type, platform, group_id, memory_id, tag) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`), b.instanceID, b.scope, b.platform, b.groupID, entry.ID, tag); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (b sqlBrainStore) saveTx(ctx context.Context, tx *sql.Tx, brain *BrainData) error {
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
	return nil
}
