package storageusage

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
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

	stickerDir, logDir, checkpointDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(checkpointDir, "session.json"), make([]byte, 500), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stickerDir, "a.png"), make([]byte, 1000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "entry.json"), make([]byte, 300), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot, err := New(db, "synthetic-a", stickerDir, logDir, checkpointDir).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 500 bytes of local session snapshots plus a few bytes of instance a rows.
	if got := bytesOf(t, snapshot, CategoryChat); got < 510 || got > 540 {
		t.Fatalf("chat bytes = %d, want only instance a rows plus the 500-byte local snapshot", got)
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

func TestSnapshotCountsSessionImagesBySizeColumn(t *testing.T) {
	db := testDB(t)
	// Same shape as the image store's migration: Base64 TEXT payload, decoded size.
	if _, err := db.SQL.Exec(`CREATE TABLE session_images (
		instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
		key TEXT NOT NULL, content_type TEXT NOT NULL, size BIGINT NOT NULL, data TEXT NOT NULL,
		created_at BIGINT NOT NULL, last_used_at BIGINT NOT NULL,
		PRIMARY KEY (instance_id, key)
	)`); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 2048)) // 2732 characters
	if _, err := db.SQL.Exec(`INSERT INTO session_images VALUES('synthetic-a','k','image/png',2048,?,1,1)`, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO session_images VALUES('synthetic-b','k','image/png',4096,?,1,1)`, encoded); err != nil {
		t.Fatal(err)
	}
	snapshot, err := New(db, "synthetic-a", "", "", "").Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesOf(t, snapshot, CategoryImages); got < 2048 || got > 2048+32 {
		t.Fatalf("images bytes = %d, want the decoded size (2048) plus a short key, not the Base64 length", got)
	}
}

func TestSnapshotIsCachedBriefly(t *testing.T) {
	db := testDB(t)
	collector := New(db, "synthetic-a", "", "", "")
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
	// A stale request returns immediately with the old value and refreshes behind it.
	stale, err := collector.Snapshot(context.Background())
	if err != nil || stale.Total != first.Total {
		t.Fatalf("stale request must return the last good snapshot at once: %v", err)
	}
	waitUntil(t, func() bool {
		collector.mu.Lock()
		defer collector.mu.Unlock()
		return collector.refreshing == nil && collector.cached.Total > first.Total
	})
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSnapshotNeverWaitsForSlowMeasurement(t *testing.T) {
	collector := New(testDB(t), "synthetic-a", "", "", "")
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	collector.measureFunc = func(ctx context.Context) (Snapshot, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return Snapshot{Total: 7}, nil
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	defer close(release)

	// A cancelled request returns promptly even though the first measurement hangs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	begin := time.Now()
	if _, err := collector.Snapshot(ctx); err == nil {
		t.Fatal("want an unavailable result while the first measurement is pending")
	}
	if time.Since(begin) > time.Second {
		t.Fatal("overview request waited for the measurement")
	}
	<-started
	// Concurrent polls share the single running measurement.
	for i := 0; i < 5; i++ {
		c, stop := context.WithCancel(context.Background())
		stop()
		_, _ = collector.Snapshot(c)
	}
	if calls.Load() != 1 {
		t.Fatalf("measurements started = %d, want a single flight", calls.Load())
	}
}

func TestSnapshotBacksOffAfterFailureAndKeepsStaleResult(t *testing.T) {
	collector := New(testDB(t), "synthetic-a", "", "", "")
	now := time.Now()
	collector.now = func() time.Time { return now }
	var calls atomic.Int32
	fail := atomic.Bool{}
	collector.measureFunc = func(context.Context) (Snapshot, error) {
		calls.Add(1)
		if fail.Load() {
			return Snapshot{}, errors.New("synthetic failure")
		}
		return Snapshot{Total: 5}, nil
	}
	if got, err := collector.Snapshot(context.Background()); err != nil || got.Total != 5 {
		t.Fatalf("first snapshot: %v %v", got, err)
	}
	fail.Store(true)
	now = now.Add(cacheTTL + time.Second)
	_, _ = collector.Snapshot(context.Background()) // starts the failing refresh
	waitUntil(t, func() bool {
		collector.mu.Lock()
		defer collector.mu.Unlock()
		return collector.refreshing == nil && collector.lastErr != nil
	})
	before := calls.Load()
	for i := 0; i < 5; i++ {
		got, err := collector.Snapshot(context.Background())
		if err != nil || got.Total != 5 {
			t.Fatalf("a failed refresh must keep serving the last good value: %v %v", got, err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != before {
		t.Fatal("measurement retried during the failure backoff")
	}
}

func TestMeasureReportsUnreadableDirectoryAsFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if _, err := New(testDB(t), "synthetic-a", "", dir, "").measure(context.Background()); err == nil {
		t.Fatal("an unreadable directory must fail the measurement, not report a low total")
	}
}

func TestDirBytesMissingDirectoryIsZero(t *testing.T) {
	got, err := dirBytes(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if err != nil || got != 0 {
		t.Fatalf("missing directory = %d, %v; want 0, nil", got, err)
	}
}

func TestSnapshotCountsLocalCheckpointWithoutSQLRow(t *testing.T) {
	checkpoints := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkpoints, "pending.json"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := New(testDB(t), "synthetic-a", "", "", checkpoints).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := bytesOf(t, snapshot, CategoryChat); got != 4096 {
		t.Fatalf("chat bytes = %d, want the local snapshot even though no session_states row exists", got)
	}
}

func TestSnapshotWithoutDatabaseFails(t *testing.T) {
	if _, err := New(nil, "synthetic-a", "", "", "").Snapshot(context.Background()); err == nil {
		t.Fatal("want an error without a SQL backend")
	}
}
