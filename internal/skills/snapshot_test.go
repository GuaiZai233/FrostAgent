package skills

import (
	"context"
	"testing"
)

func TestSnapshotInvalidatedAcrossStoreHandles(t *testing.T) {
	for _, change := range []string{"disable", "re-enable", "delete", "same-revision-import", "instance-delete"} {
		t.Run(change, func(t *testing.T) {
			db, store := skillTestStore(t)
			pkg, err := ParsePackage(map[string][]byte{"SKILL.md": []byte("---\nname: alpha\ndescription: Synthetic\n---\nInstructions")}, "")
			if err != nil {
				t.Fatal(err)
			}
			pkg.Info.Enabled = true
			if err := store.ImportPackages([]Package{pkg}); err != nil {
				t.Fatal(err)
			}
			writer := NewStore(db, "synthetic-instance")
			if change == "re-enable" {
				if err := writer.SetEnabled("alpha", false); err != nil {
					t.Fatal(err)
				}
			}
			if change == "same-revision-import" {
				if err := writer.Delete("alpha"); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := store.Snapshot("alpha")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "disable":
				err = writer.SetEnabled("alpha", false)
			case "re-enable":
				err = writer.SetEnabled("alpha", true)
			case "delete":
				err = writer.Delete("alpha")
			case "same-revision-import":
				err = writer.ImportPackages([]Package{pkg})
			case "instance-delete":
				if err := db.PrepareDeletion(context.Background(), "synthetic-instance", 1); err != nil {
					t.Fatal(err)
				}
				err = db.ConfirmDeletion(context.Background(), "synthetic-instance")
			}
			if err != nil {
				t.Fatal(err)
			}
			applied, err := store.WithSnapshot(snapshot, func() error {
				t.Error("stale snapshot reached session mutation")
				return nil
			})
			if applied || err != nil {
				t.Fatalf("stale snapshot: applied=%v err=%v", applied, err)
			}
			fresh, err := writer.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			called := false
			applied, err = store.WithSnapshot(fresh, func() error { called = true; return nil })
			if !applied || !called || err != nil {
				t.Fatalf("fresh shared snapshot: applied=%v called=%v err=%v", applied, called, err)
			}
		})
	}
}

func TestSnapshotGuardExcludesWritersOnlyForItsInstance(t *testing.T) {
	db, store := skillTestStore(t)
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	other := NewStore(db, "synthetic-other")
	applied, err := store.WithSnapshot(snapshot, func() error {
		if db.SkillState("synthetic-instance").Mu.TryLock() {
			db.SkillState("synthetic-instance").Mu.Unlock()
			t.Error("writer can acquire state during guarded mutation")
		}
		if !other.state.Mu.TryLock() {
			t.Error("unrelated instance shares the state lock")
		} else {
			other.state.Mu.Unlock()
		}
		return nil
	})
	if !applied || err != nil {
		t.Fatalf("guard: applied=%v err=%v", applied, err)
	}
	if applied, err := other.WithSnapshot(snapshot, func() error {
		t.Error("snapshot accepted by another instance")
		return nil
	}); applied || err != nil {
		t.Fatalf("wrong instance: applied=%v err=%v", applied, err)
	}
}

func TestSnapshotLoadsOnlyRequestedEnabledInstructions(t *testing.T) {
	_, store := skillTestStore(t)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		pkg, err := ParsePackage(map[string][]byte{
			"SKILL.md":               []byte("---\nname: " + name + "\ndescription: Synthetic\n---\nBODY-" + name),
			"references/example.txt": []byte("RESOURCE-BYTES"),
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		pkg.Info.Enabled = name != "bravo"
		if err := store.ImportPackages([]Package{pkg}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := store.Snapshot("alpha", "bravo", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Infos) != 3 || len(snapshot.Instructions) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	pkg := snapshot.Instructions["alpha"]
	if pkg.Body != "BODY-alpha" || len(pkg.Files) != 0 {
		t.Fatal("snapshot omitted instructions or loaded resource bytes")
	}
}
