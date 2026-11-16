package logs

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentLogsRestartImagesUUIDAndClear(t *testing.T) {
	dir := t.TempDir()
	s := New("synthetic", "Test", 2)
	if err := s.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	uuid := s.Error(SYSTEM, "old error")
	image := []byte("synthetic-image")
	s.InfoWithInlineImages(LLM_REQUEST, "prompt", []InlineImage{{ContentType: "image/png", Data: image}})
	s.Info(SYSTEM, "newest")
	// The disk archive exceeds the in-memory ring's entry count.
	next := New("synthetic", "Test", 2)
	if err := next.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if entries := next.Snapshot(); len(entries) != 3 || entries[0].Content != "old error" {
		t.Fatalf("restored entries: %v", entries)
	}
	if _, ok := next.FindError(uuid); !ok {
		t.Fatal("lost error UUID")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(image))
	response := httptest.NewRecorder()
	next.ImageHandler(response, httptest.NewRequest("GET", LogImagePathPrefix+hash, nil))
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), image) {
		t.Fatal("lost log image")
	}
	next.Clear()
	last := New("synthetic", "Test", 2)
	if err := last.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	if len(last.Snapshot()) != 0 {
		t.Fatal("clear resurrected logs after restart")
	}
	last.Info(SYSTEM, "after clear")
	if got := last.Snapshot()[0].ID; got <= 3 {
		t.Fatal("log IDs reused after clear")
	}
}

func TestPersistentBudgetIncludesImagesAndEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	s := New("synthetic", "Test", 20)
	if err := s.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	s.InfoWithInlineImages(SYSTEM, "first", []InlineImage{{ContentType: "image/png", Data: bytes.Repeat([]byte{1}, 4096)}})
	s.Info(SYSTEM, "second")
	s.Info(SYSTEM, "third")
	s.SetPersistentBudget(1000)
	entries := s.Snapshot()
	if len(entries) != 2 || entries[0].Content != "second" || entries[1].Content != "third" {
		t.Fatalf("oldest eviction: %v", entries)
	}
	var diskBytes int64
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		info, err := os.Stat(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		diskBytes += info.Size()
	}
	if status := s.PersistenceStatus(); status.UsedBytes != diskBytes || diskBytes > 1000 {
		t.Fatalf("disk accounting: %+v actual=%d", status, diskBytes)
	}
	next := New("synthetic", "Test", 20)
	if err := next.EnablePersistence(dir, 1000); err != nil {
		t.Fatal(err)
	}
	if len(next.Snapshot()) != 2 {
		t.Fatal("evicted logs returned on restart")
	}
}

func TestInterruptedLogWriteDoesNotHideReadableRecords(t *testing.T) {
	dir := t.TempDir()
	s := New("synthetic", "Test", 20)
	if err := s.EnablePersistence(dir, 1<<20); err != nil {
		t.Fatal(err)
	}
	s.Info(SYSTEM, "good")
	if err := os.WriteFile(filepath.Join(dir, ".log-atomic-interrupted"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.disk.path(2), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	next := New("synthetic", "Test", 20)
	if err := next.EnablePersistence(dir, 1<<20); err == nil {
		t.Fatal("corrupt file not reported")
	}
	if entries := next.Snapshot(); len(entries) != 1 || entries[0].Content != "good" {
		t.Fatal("readable records lost")
	}
}
