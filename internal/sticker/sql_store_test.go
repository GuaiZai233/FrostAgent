package sticker

import (
	"FrostAgent/internal/storage"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLStickerMetadataKeepsImageOnFilesystem(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "stickers.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL.Exec(`INSERT INTO instances(id, name, created_at) VALUES ('test-instance', 'Test', 'now')`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "images")
	store, err := NewSQLStore(db, "test-instance", dir)
	if err != nil {
		t.Fatal(err)
	}
	image := []byte{0x89, 'P', 'N', 'G'}
	if err := store.Add("sticker-a", "image.png", image); err != nil {
		t.Fatal(err)
	}
	if err := store.Update("sticker-a", "example", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLStore(db, "test-instance", dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := reopened.Get("sticker-a")
	if !ok || entry.Description != "example" || len(entry.Keywords) != 2 {
		t.Fatalf("SQL sticker metadata not recovered: %#v, %v", entry, ok)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "image.png"))
	if err != nil || string(stored) != string(image) {
		t.Fatalf("sticker image was not retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy index was created: %v", err)
	}
}
