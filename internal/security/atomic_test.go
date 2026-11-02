package security

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicReplaceFileFailurePreservesLockedState(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "access.json")
	principal := testPrincipal(t, "test-platform", "actor-under-test")
	if err := NewAccessStore(destination).Lock(principal, "existing lock"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}

	if err := atomicReplaceFile(filepath.Join(dir, "missing-replacement"), destination); err == nil {
		t.Fatal("expected replacement to fail")
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("existing store lost after failed replacement: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed replacement changed existing store")
	}
	locked, record, err := NewAccessStore(destination).IsLocked(principal)
	if err != nil || !locked || record.Reason != "existing lock" {
		t.Fatalf("failed replacement lost locked state: locked=%v record=%+v err=%v", locked, record, err)
	}
}

func TestAtomicReplaceFileUpdatesExistingStore(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "access.json")
	source := filepath.Join(dir, "replacement.json")
	principal := testPrincipal(t, "test-platform", "actor-under-test")
	store := NewAccessStore(destination)
	if err := store.Lock(principal, "existing lock"); err != nil {
		t.Fatal(err)
	}
	if err := NewAccessStore(source).Lock(principal, "updated lock"); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}

	if err := atomicReplaceFile(source, destination); err != nil {
		t.Fatalf("replace existing store: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("existing store does not contain replacement data")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("replacement source should have been moved, got %v", err)
	}
	locked, record, err := store.IsLocked(principal)
	if err != nil || !locked || record.Reason != "updated lock" {
		t.Fatalf("replacement not visible: locked=%v record=%+v err=%v", locked, record, err)
	}

	if err := store.Unlock(principal); err != nil {
		t.Fatalf("unlock through store replacement: %v", err)
	}
	locked, _, err = NewAccessStore(destination).IsLocked(principal)
	if err != nil || locked {
		t.Fatalf("persisted unlock not visible: locked=%v err=%v", locked, err)
	}
}
