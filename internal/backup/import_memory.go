package backup

import (
	"FrostAgent/internal/memory"
	"FrostAgent/internal/storage"
)

func ImportMemories(db *storage.DB, instanceID string, data Memories, merge bool) (memory.ImportResult, error) {
	if err := ValidateFormat(data.FormatVersion); err != nil {
		return memory.ImportResult{}, err
	}
	snapshot := memory.Snapshot{PrivateEntries: data.PrivateEntries, PrivateArchives: data.PrivateArchives,
		Groups: make([]memory.GroupSnapshot, 0, len(data.Groups))}
	for _, group := range data.Groups {
		snapshot.Groups = append(snapshot.Groups, memory.GroupSnapshot{Platform: group.Platform,
			GroupID: group.GroupID, Profile: group.Profile, Entries: group.Entries, Archives: group.Archives})
	}
	return memory.ImportSQLSnapshot(db, instanceID, snapshot, merge)
}
