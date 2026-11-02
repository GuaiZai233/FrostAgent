//go:build !windows

package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestAuditRotationPreservesFIFOAndSymlink(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "audit-2000-01-01.jsonl")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "keep")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "audit-2000-01-02.jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	a := &fileAudit{dir: dir, retentionDays: 7, now: time.Now}
	if err := a.append(Operation{Identity: testRequest("rotation").Identity}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(fifo); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("rotation removed FIFO: %v %v", info, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("rotation removed symlink: %v %v", info, err)
	}
}
