package backup

import (
	"context"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestCompleteInstanceBackupRejectsCompatibleFutureSchema(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const id = "synthetic-instance"
	if err := db.CreateInstance(ctx, storage.InstanceRecord{ID: id, Name: "Synthetic", CreatedAt: time.Now()}, 2); err != nil {
		t.Fatal(err)
	}
	images := t.TempDir()
	if archive, err := BuildInstanceZIP(db, id, images); err != nil || len(archive) == 0 {
		t.Fatalf("native-schema backup failed: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE future_instance_data (instance_id TEXT PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE, payload TEXT NOT NULL)`,
		`INSERT INTO future_instance_data VALUES ('synthetic-instance', '{"unknown_module":"retain exactly"}')`,
		`UPDATE schema_meta SET version = 15, min_compatible_version = 14 WHERE id = 1`,
	} {
		if _, err := db.SQL.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	wantErr := db.RequireNativeSchema(ctx, "export a complete instance backup")
	if wantErr == nil {
		t.Fatal("fixture did not enter future-schema compatibility mode")
	}
	archive, err := BuildInstanceZIP(db, id, images)
	if err == nil || err.Error() != wantErr.Error() || len(archive) != 0 {
		t.Fatalf("incomplete full backup was not rejected: bytes=%d err=%v", len(archive), err)
	}
	// Part exports describe their scope explicitly and remain usable.
	if _, err := ExportSettings(db, id); err != nil {
		t.Fatalf("partial settings export was blocked: %v", err)
	}
	var version, floor int
	if err := db.SQL.QueryRow(`SELECT version, min_compatible_version FROM schema_meta WHERE id = 1`).Scan(&version, &floor); err != nil || version != 15 || floor != 14 {
		t.Fatalf("backup changed compatibility metadata: version=%d floor=%d err=%v", version, floor, err)
	}
	var payload string
	if err := db.SQL.QueryRow(`SELECT payload FROM future_instance_data WHERE instance_id = 'synthetic-instance'`).Scan(&payload); err != nil || payload != `{"unknown_module":"retain exactly"}` {
		t.Fatalf("backup changed unknown module data: %q %v", payload, err)
	}
}
