package runtimescope

import (
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"os"
	"path/filepath"
	"testing"
)

func TestScopeSharedKeysPrecedence(t *testing.T) {
	dir := t.TempDir()
	globalStore, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatal(err)
	}
	instanceStore, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}

	testKey := "SECURITY_GATEWAY_TIMEOUT"
	if !instanceconfig.SharedKeys[testKey] {
		t.Fatalf("%s must be in SharedKeys", testKey)
	}

	// 1. Process environment only
	t.Setenv(testKey, "25s")
	scopeEmpty := New(instanceStore, globalStore, logs.General)
	if v := scopeEmpty.Getenv(testKey); v != "25s" {
		t.Fatalf("expected process env fallback %q, got %q", "25s", v)
	}
	if v, ok := scopeEmpty.LookupEnv(testKey); !ok || v != "25s" {
		t.Fatalf("expected LookupEnv process env fallback (true, %q), got (%v, %q)", "25s", ok, v)
	}

	// 2. Global config overrides process environment
	if err := globalStore.Update(testKey, "35s", false); err != nil {
		t.Fatal(err)
	}
	scopeGlobal := New(instanceStore, globalStore, logs.General)
	if v := scopeGlobal.Getenv(testKey); v != "35s" {
		t.Fatalf("expected global config override %q, got %q", "35s", v)
	}
	if v, ok := scopeGlobal.LookupEnv(testKey); !ok || v != "35s" {
		t.Fatalf("expected LookupEnv global config (true, %q), got (%v, %q)", "35s", ok, v)
	}

	// 3. Instance override overrides global config and process environment
	if err := instanceStore.Update(testKey, "45s", false); err != nil {
		t.Fatal(err)
	}
	scopeInstance := New(instanceStore, globalStore, logs.General)
	if v := scopeInstance.Getenv(testKey); v != "45s" {
		t.Fatalf("expected instance override %q, got %q", "45s", v)
	}
	if v, ok := scopeInstance.LookupEnv(testKey); !ok || v != "45s" {
		t.Fatalf("expected LookupEnv instance override (true, %q), got (%v, %q)", "45s", ok, v)
	}

	// 4. Remove instance override restores global config precedence
	if err := instanceStore.Update(testKey, "", true); err != nil {
		t.Fatal(err)
	}
	if v := scopeInstance.Getenv(testKey); v != "35s" {
		t.Fatalf("expected restoration of global config %q, got %q", "35s", v)
	}

	// 5. Remove global config restores process env
	if err := globalStore.Update(testKey, "", true); err != nil {
		t.Fatal(err)
	}
	if v := scopeInstance.Getenv(testKey); v != "25s" {
		t.Fatalf("expected restoration of process env %q, got %q", "25s", v)
	}

	// 6. Unset process env returns empty string and false
	t.Setenv(testKey, "")
	_ = os.Unsetenv(testKey)
	if v := scopeInstance.Getenv(testKey); v != "" {
		t.Fatalf("expected empty string when unset, got %q", v)
	}
	if v, ok := scopeInstance.LookupEnv(testKey); ok || v != "" {
		t.Fatalf("expected (false, \"\") when unset, got (%v, %q)", ok, v)
	}
}
