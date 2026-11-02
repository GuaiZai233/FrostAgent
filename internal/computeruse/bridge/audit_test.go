package bridge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditDirectorySyncFailurePreventsDispatch(t *testing.T) {
	s, config := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("executor ran before audit directory durability")
		return Outcome{}, nil
	}})
	var calls int
	s.audit.(*fileAudit).syncDir = func(path string) error {
		calls++
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 1 {
			t.Errorf("directory barrier did not follow file publication: %v %v", entries, err)
		}
		return errors.New("synthetic directory sync failure")
	}
	op, err := s.Submit(context.Background(), testRequest("dir-sync"))
	if err != nil || op.State != Failed || op.Code != "audit_unavailable" || calls != 1 {
		t.Fatalf("directory sync failure not enforced: %+v %v calls=%d", op, err, calls)
	}
	if _, err := os.Stat(config.AuditDir); err != nil {
		t.Fatal(err)
	}
}

func TestAuditPublishDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishAuditFile(source, destination); err == nil {
		t.Fatal("audit publication overwrote an existing file")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "existing" {
		t.Fatalf("existing file changed: %q %v", data, err)
	}
}

func TestAuditCrashTemporaryFilesUseSameRetention(t *testing.T) {
	dir := t.TempDir()
	keep := []string{
		".audit-create-2026-01-03-123",
		".audit-create-2026-01-09-456",
		".audit-create-unknown",
	}
	expired := ".audit-create-2026-01-02-789"
	for _, name := range append(append([]string(nil), keep...), expired) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("metadata"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := &fileAudit{dir: dir, retentionDays: 7, now: func() time.Time {
		return time.Date(2026, 1, 9, 12, 0, 0, 0, time.UTC)
	}}
	if err := a.append(Operation{Identity: testRequest("temporary").Identity}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, expired)); !os.IsNotExist(err) {
		t.Fatalf("expired temporary metadata was retained: %v", err)
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("rotation removed recent or unrelated file: %s %v", name, err)
		}
	}
}

func TestAuditRotationRetainsSevenDaysAndNotJournal(t *testing.T) {
	s, config := testService(t, testExecutor{})
	a := s.audit.(*fileAudit)
	if a.retentionDays != 7 {
		t.Fatalf("default retention = %d, want 7", a.retentionDays)
	}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	req := testRequest("rotation")
	hash, _ := req.fingerprint()
	op := Operation{Identity: req.Identity, RequestHash: hash, Outcome: Outcome{State: Running}, CreatedAt: base, UpdatedAt: base}
	for day := range 9 {
		at := base.AddDate(0, 0, day)
		a.now = func() time.Time { return at }
		if err := a.append(op); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(config.AuditDir)
	if err != nil || len(entries) != 7 {
		t.Fatalf("rotation did not retain exactly seven daily files: %d %v", len(entries), err)
	}
	if entries[0].Name() != "audit-2026-01-03.jsonl" || entries[6].Name() != "audit-2026-01-09.jsonl" {
		t.Fatalf("wrong retained date range: %s ... %s", entries[0].Name(), entries[6].Name())
	}
	// A separately retained operation survives arbitrarily far audit rotation.
	a.now = time.Now
	if _, err := s.Submit(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, s, req.OperationID)
	s.mu.Lock()
	a.now = func() time.Time { return base.AddDate(10, 0, 0) }
	err = a.append(op)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := s.Lookup(req.OperationID); err != nil || saved.State != Succeeded {
		t.Fatalf("audit rotation lost dedup state: %+v %v", saved, err)
	}
}

func TestAuditAndJournalNeverPersistRawContent(t *testing.T) {
	s, config := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		return Outcome{State: Succeeded, Code: "synthetic-secret-output"}, nil
	}})
	if _, err := s.Submit(context.Background(), testRequest("privacy")); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, s, "privacy")
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{config.StateDir, config.AuditDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("synthetic-secret")) || bytes.Contains(data, []byte("parameters")) {
				t.Fatalf("raw content persisted in %s", entry.Name())
			}
		}
	}
}

func TestRealAuditFilesystemFailurePreventsDispatch(t *testing.T) {
	s, config := testService(t, testExecutor{run: func(context.Context, Request) (Outcome, error) {
		t.Error("executor ran with inaccessible audit directory")
		return Outcome{}, nil
	}})
	// The renamed directory belongs to this test's TempDir, not a real VM.
	if err := os.Rename(config.AuditDir, config.AuditDir+"-offline"); err != nil {
		t.Fatal(err)
	}
	op, err := s.Submit(context.Background(), testRequest("audit-offline"))
	if err != nil || op.State != Failed || op.Code != "audit_unavailable" {
		t.Fatalf("audit failure result: %+v %v", op, err)
	}
}

func TestAuditRotationOnlyRemovesItsOwnRegularDailyFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"keep.txt", "operations.db", "audit-invalid.jsonl", "audit-2020-13-90.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "audit-2000-01-01.jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &fileAudit{dir: dir, retentionDays: 7, now: time.Now}
	if err := a.append(Operation{Identity: testRequest("audit").Identity}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 6 {
		t.Fatalf("rotation touched unrelated entries: %v", entries)
	}
}

func TestDataDirectoriesMustBeSeparate(t *testing.T) {
	for _, nested := range []bool{false, true} {
		config := testConfig(t.TempDir())
		config.AuditDir = config.StateDir
		if nested {
			config.AuditDir = filepath.Join(config.StateDir, "audit")
		}
		if s, err := Open(config, testExecutor{}); err == nil {
			_ = s.Shutdown(context.Background())
			t.Fatal("overlapping state and audit directories accepted")
		}
	}
}
