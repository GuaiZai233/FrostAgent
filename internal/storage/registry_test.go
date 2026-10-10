package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestInstanceRegistryUpdatesOneRowWithoutRemovingOthers(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "registry.db"))
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for i, id := range []string{"instance-a", "instance-b"} {
		if err := db.CreateInstance(ctx, InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, i+2); err != nil {
			t.Fatal(err)
		}
	}
	records, next, err := db.LoadInstances(ctx)
	if err != nil || len(records) != 2 || next != 3 {
		t.Fatalf("registry load: %#v, %d, %v", records, next, err)
	}
	records[0].Enabled = true
	if err := db.UpdateInstance(ctx, records[0]); err != nil {
		t.Fatal(err)
	}
	records, _, err = db.LoadInstances(ctx)
	if err != nil || len(records) != 2 || !records[0].Enabled {
		t.Fatalf("row update damaged registry: %#v, %v", records, err)
	}
}
