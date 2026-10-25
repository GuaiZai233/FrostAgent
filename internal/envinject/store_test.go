package envinject

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"os"
	"testing"
)

func TestAdditionalEnvAppliesOnlyAtStartup(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const key = "FROSTAGENT_SYNTHETIC_INJECTION_TEST"
	if _, exists := os.LookupEnv(key); exists {
		t.Skip("synthetic key already provided by host")
	}
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	if err := Put(ctx, db, key, "first"); err != nil {
		t.Fatal(err)
	}
	if _, exists := os.LookupEnv(key); exists {
		t.Fatal("editing injected environment changed the current process")
	}
	if err := ApplyAtStartup(ctx, db); err != nil {
		t.Fatal(err)
	}
	if value, ok := LookupApplied(key); !ok || value != "first" {
		t.Fatalf("startup did not inject stored value: %q, %t", value, ok)
	}
	if err := Put(ctx, db, key, "second"); err != nil {
		t.Fatal(err)
	}
	if value, _ := LookupApplied(key); value != "first" {
		t.Fatalf("editing changed active process environment: %q", value)
	}
	if err := Delete(ctx, db, key); err != nil {
		t.Fatal(err)
	}
	entries, err := List(ctx, db)
	if err != nil || len(entries) != 0 {
		t.Fatalf("deleted entry still stored: %#v, %v", entries, err)
	}
}

func TestAdditionalEnvRejectsInvalidValues(t *testing.T) {
	for _, entry := range []Entry{{Key: "9BAD", Value: "x"}, {Key: "OK", Value: "one\ntwo"}, {Key: "OK", Value: "x\x00y"}} {
		if err := Validate(entry.Key, entry.Value); err == nil {
			t.Fatalf("accepted invalid entry %#v", entry)
		}
	}
}

func TestAdditionalEnvKeepsHostOverride(t *testing.T) {
	const key = "FROSTAGENT_SYNTHETIC_HOST_OVERRIDE"
	t.Setenv(key, "host-value")
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Put(ctx, db, key, "database-value"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyAtStartup(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(key); got != "host-value" {
		t.Fatalf("database value overrode host environment: %q", got)
	}
}

func TestSuspendRevokesOldDatabaseInjectionAndRestore(t *testing.T) {
	const key = "FROSTAGENT_SYNTHETIC_SWITCH_TOKEN"
	if _, exists := os.LookupEnv(key); exists {
		t.Skip("synthetic key already provided by host")
	}
	t.Cleanup(func() {
		Suspend()
		_ = os.Unsetenv(key)
	})
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Put(ctx, db, key, "original-database"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyAtStartup(ctx, db); err != nil {
		t.Fatal(err)
	}
	previous := Suspend()
	if _, exists := LookupApplied(key); exists {
		t.Fatal("old database injection remained visible after suspension")
	}
	if _, exists := os.LookupEnv(key); exists {
		t.Fatal("old database injection remained in the process environment")
	}
	if err := Restore(previous); err != nil {
		t.Fatal(err)
	}
	if value, exists := LookupApplied(key); !exists || value != "original-database" {
		t.Fatalf("failed switch did not restore startup injection: %q %t", value, exists)
	}
}
