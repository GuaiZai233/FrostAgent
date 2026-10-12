package security

import (
	"FrostAgent/internal/storage"
	"context"
	"path/filepath"
	"testing"
)

func TestSQLAccessAndAuditPersistWithoutLegacyFiles(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "security.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	principal, err := NewPrincipal("onebot", "test-user")
	if err != nil {
		t.Fatal(err)
	}
	controller := NewControllerSQL(db)
	if err := controller.Access.Lock(principal, "test reason"); err != nil {
		t.Fatal(err)
	}
	if err := controller.Audit.Append(AuditEvent{ID: "event-a", Principal: principal,
		Stage: "test", Source: "test", Action: "block", Hash: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	reopened := NewControllerSQL(db)
	locked, record, err := reopened.Access.IsLocked(principal)
	if err != nil || !locked || record.Reason != "test reason" {
		t.Fatalf("access lock was not recovered: %#v, %v", record, err)
	}
	events, err := reopened.Audit.List(10)
	if err != nil || len(events) != 1 || events[0].ID != "event-a" {
		t.Fatalf("audit event was not recovered: %#v, %v", events, err)
	}
	if err := reopened.Access.Unlock(principal); err != nil {
		t.Fatal(err)
	}
	locked, _, err = controller.Access.IsLocked(principal)
	if err != nil || locked {
		t.Fatalf("SQL unlock did not take effect: %v, %v", locked, err)
	}
}
