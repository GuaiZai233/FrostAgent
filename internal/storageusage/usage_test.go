package storageusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func testDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		if _, err := db.SQL.Exec(`INSERT INTO instances(id,name,created_at) VALUES(?,?,?)`, id, id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func bytesOf(t *testing.T, s Snapshot, category string) int64 {
	t.Helper()
	for _, item := range s.Items {
		if item.Category == category {
			return item.Bytes
		}
	}
	t.Fatalf("missing category %s", category)
	return 0
}

func TestSnapshotGroupsCategoriesPerInstance(t *testing.T) {
	db := testDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.SQL.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO chat_history(instance_id,id,platform,message_type,target_id,message_id,sender_id,sender_name,role,content,search_text,reply_to,occurred_at,delivery_status)
		VALUES('synthetic-a','1','p','group','t','m','s','n','user','12345','12345','',1,'')`)
	exec(`INSERT INTO chat_history(instance_id,id,platform,message_type,target_id,message_id,sender_id,sender_name,role,content,search_text,reply_to,occurred_at,delivery_status)
		VALUES('synthetic-b','1','p','group','t','m','s','n','user','this other instance must not count','x','',1,'')`)
	exec(`INSERT INTO memory_entries(instance_id,scope_type,id,owner,content,source,created_at,updated_at) VALUES('synthetic-a','user','1','o','memory-content','s','c','u')`)
	exec(`INSERT INTO sticker_entries(instance_id,id,file_name,weight,status,model_suspected,manual_blocked,created_at,updated_at,position) VALUES('synthetic-a','1','a.png',1,'ok',0,0,1,1,0)`)

	stickerDir, logDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(stickerDir, "a.png"), make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "entry.json"), make([]byte, 300), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := New(db, "synthetic-a", stickerDir, logDir).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesOf(t, snapshot, CategoryChat); got < 10 || got > 40 {
		t.Fatalf("chat bytes = %d, want only instance a rows", got)
	}
	if got := bytesOf(t, snapshot, CategorySticker); got < 1000 {
		t.Fatalf("sticker bytes = %d, want >= 1000 file bytes", got)
	}
	if got := bytesOf(t, snapshot, CategoryOther); got != 300 {
		t.Fatalf("other bytes = %d, want 300 log bytes", got)
	}
	if bytesOf(t, snapshot, CategoryMemory) == 0 {
		t.Fatal("memory bytes = 0")
	}
	if got := bytesOf(t, snapshot, CategoryImages); got != 0 {
		t.Fatalf("images bytes = %d, want 0 while session_images is absent", got)
	}
	var sum int64
	for _, item := range snapshot.Items {
		sum += item.Bytes
	}
	if sum != snapshot.Total || len(snapshot.Items) != len(Order) {
		t.Fatalf("items %+v do not sum to total %d", snapshot.Items, snapshot.Total)
	}
}

func TestSnapshotCountsSessionImagesWhenTableExists(t *testing.T) {
	db := testDB(t)
	// The migration belongs to the image-persistence change; mimic its shape.
	if _, err := db.SQL.Exec(`CREATE TABLE session_images (instance_id TEXT NOT NULL, key TEXT NOT NULL, content_type TEXT NOT NULL, size INTEGER NOT NULL, data BLOB NOT NULL, created_at BIGINT NOT NULL, last_used_at BIGINT NOT NULL, PRIMARY KEY(instance_id,key))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO session_images VALUES('synthetic-a','k','image/png',2048,?,1,1)`, make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO session_images VALUES('synthetic-b','k','image/png',4096,?,1,1)`, make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := New(db, "synthetic-a", "", "").Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesOf(t, snapshot, CategoryImages); got < 2048 || got > 2048+64 {
		t.Fatalf("images bytes = %d, want about 2048", got)
	}
}

func TestSnapshotIsCachedBriefly(t *testing.T) {
	db := testDB(t)
	collector := New(db, "synthetic-a", "", "")
	now := time.Now()
	collector.now = func() time.Time { return now }
	first, err := collector.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO memory_entries(instance_id,scope_type,id,owner,content,source,created_at,updated_at) VALUES('synthetic-a','user','1','o','later','s','c','u')`); err != nil {
		t.Fatal(err)
	}
	cached, _ := collector.Snapshot(context.Background())
	if cached.Total != first.Total {
		t.Fatal("snapshot was not served from cache")
	}
	now = now.Add(cacheTTL + time.Second)
	fresh, _ := collector.Snapshot(context.Background())
	if fresh.Total <= first.Total {
		t.Fatal("snapshot was not refreshed after the cache expired")
	}
}

func TestSnapshotWithoutDatabaseFails(t *testing.T) {
	if _, err := New(nil, "synthetic-a", "", "").Snapshot(context.Background()); err == nil {
		t.Fatal("want an error without a SQL backend")
	}
}
