package backup

import (
	"FrostAgent/internal/security"
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGlobalSecurityBackupRoundTripAndAtomicFailure(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "security.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	principal, err := security.NewPrincipal("qq", "synthetic-user")
	if err != nil {
		t.Fatal(err)
	}
	access := security.NewSQLAccessStore(db)
	if err := access.Lock(principal, "synthetic reason"); err != nil {
		t.Fatal(err)
	}
	audit := security.NewSQLAuditStore(db, 1000)
	if err := audit.Append(security.AuditEvent{ID: "synthetic-event", At: time.Now(), Principal: principal,
		Stage: "INPUT", Source: "USER_MESSAGE", Action: "BLOCK", Preview: "synthetic preview"}); err != nil {
		t.Fatal(err)
	}
	backup, err := ExportGlobalSecurity(db)
	if err != nil || len(backup.AccessRecords) != 1 || len(backup.AuditEvents) != 1 {
		t.Fatalf("export: %#v, %v", backup, err)
	}
	if err := access.Unlock(principal); err != nil {
		t.Fatal(err)
	}
	if err := ImportGlobalSecurity(db, backup); err != nil {
		t.Fatal(err)
	}
	locked, _, err := access.IsLocked(principal)
	if err != nil || !locked {
		t.Fatalf("access state not restored: %v, %v", locked, err)
	}
	backup.AuditEvents = append(backup.AuditEvents, backup.AuditEvents[0])
	if err := ImportGlobalSecurity(db, backup); err == nil {
		t.Fatal("duplicate audit ID was accepted")
	}
	locked, _, err = access.IsLocked(principal)
	if err != nil || !locked {
		t.Fatalf("failed import changed access state: %v, %v", locked, err)
	}
}
