package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLegacyMigrationKeepsRetiredNamespacesInBrain(t *testing.T) {
	for _, platform := range []string{"aiocqhttp", "astrbot"} {
		t.Run(platform, func(t *testing.T) {
			dir := t.TempDir()
			brainPath := filepath.Join(dir, "brain.json")
			const groupID = "synthetic-group"
			groupOwner := platform + ":group:" + groupID
			retainedEntries := []MemoryEntry{
				{ID: "retired-scoped", Owner: groupOwner, ScopeType: ScopeGroup, GroupID: groupID},
				{ID: "retired-owner-type", Owner: groupOwner, OwnerType: OwnerGroup, GroupID: groupID},
				{ID: "retired-owner-only", Owner: groupOwner, OwnerType: OwnerGroup},
				{ID: "retired-user", Owner: platform + ":user:synthetic-user", ScopeType: ScopeGroup, GroupID: groupID},
				{ID: "retired-group-field", Owner: GroupOwnerExplicit, ScopeType: ScopeGroup, GroupID: groupOwner},
			}
			qqSource := MemoryEntry{ID: "qq-source", Owner: "synthetic-user", ScopeType: ScopeGroup, GroupID: groupID}
			retainedArchives := []MemoryMergeArchive{
				{MergedID: "retired-owner", Owner: groupOwner, Sources: []MemoryEntry{qqSource}},
				{MergedID: "retired-source", Sources: []MemoryEntry{retainedEntries[0]}},
				{MergedID: "retired-later-source", Owner: "qq:group:" + groupID, Sources: []MemoryEntry{qqSource, retainedEntries[0]}},
				{MergedID: "retired-source-group-field", Owner: GroupOwnerExplicit, Sources: []MemoryEntry{retainedEntries[4]}},
			}
			qqEntries := []MemoryEntry{
				{ID: "qq-bare-group", Owner: GroupOwnerExplicit, ScopeType: ScopeGroup, GroupID: groupID},
				{ID: "qq-bare-user", Owner: "synthetic-user", ScopeType: ScopeGroup, GroupID: groupID},
				{ID: "qq-owner-type", Owner: groupID, OwnerType: OwnerGroup},
			}
			qqArchives := []MemoryMergeArchive{{MergedID: "qq-source-fallback", Sources: []MemoryEntry{qqSource}}}
			for _, prefix := range []string{"group:", "qq:group:", "onebot:group:"} {
				qqEntries = append(qqEntries, MemoryEntry{ID: prefix + "entry", Owner: prefix + groupID})
				qqArchives = append(qqArchives, MemoryMergeArchive{MergedID: prefix + "archive", Owner: prefix + groupID, Sources: []MemoryEntry{qqSource}})
			}
			initial := BrainData{
				Entries:       append(append([]MemoryEntry(nil), retainedEntries...), qqEntries...),
				MergeArchives: append(append([]MemoryMergeArchive(nil), retainedArchives...), qqArchives...),
			}
			raw, err := json.Marshal(initial)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(brainPath, raw, 0600); err != nil {
				t.Fatal(err)
			}

			// A second startup must preserve the retained records and not duplicate QQ data.
			for pass := range 2 {
				manager := NewGroupManager(dir, nil)
				if err := MigrateLegacyGroupMemories(dir, NewStore(brainPath), manager, nil); err != nil {
					t.Fatalf("migration pass %d: %v", pass, err)
				}
				remaining, err := os.ReadFile(brainPath)
				if err != nil {
					t.Fatal(err)
				}
				var brain BrainData
				if err := json.Unmarshal(remaining, &brain); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(brain.Entries, retainedEntries) || !reflect.DeepEqual(brain.MergeArchives, retainedArchives) {
					t.Fatalf("retired records changed or left brain.json: %+v", brain)
				}
				qq, err := manager.GetGroupStoreForPlatform("onebot", groupID)
				if err != nil {
					t.Fatal(err)
				}
				entries, err := qq.ListAll()
				if err != nil {
					t.Fatal(err)
				}
				wantIDs := make(map[string]bool)
				for _, entry := range qqEntries {
					wantIDs[entry.ID] = true
				}
				visible := NewGateway().FilterGroup(entries)
				if len(visible) != len(wantIDs) {
					t.Fatalf("QQ recall contains %d entries, want %d: %+v", len(visible), len(wantIDs), visible)
				}
				for _, entry := range visible {
					if !wantIDs[entry.ID] {
						t.Fatalf("QQ recalled unexpected memory %q", entry.ID)
					}
					delete(wantIDs, entry.ID)
				}
				if len(wantIDs) != 0 {
					t.Fatalf("QQ legacy memories missing: %v", wantIDs)
				}
				archives, err := qq.ListMergeArchives()
				if err != nil {
					t.Fatal(err)
				}
				wantArchives := make(map[string]bool)
				for _, archive := range qqArchives {
					wantArchives[archive.MergedID] = true
				}
				if len(archives) != len(wantArchives) {
					t.Fatalf("QQ archives contain %d records, want %d: %+v", len(archives), len(wantArchives), archives)
				}
				for _, archive := range archives {
					if !wantArchives[archive.MergedID] {
						t.Fatalf("QQ received unexpected archive %q", archive.MergedID)
					}
					delete(wantArchives, archive.MergedID)
				}
				groups, err := manager.ListGroups()
				if err != nil || len(groups) != 1 || groups[0].Platform != "qq" || groups[0].GroupID != groupID {
					t.Fatalf("unexpected migration targets: %+v, err=%v", groups, err)
				}
			}
		})
	}
}
