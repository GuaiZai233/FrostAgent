package runtimescope

import (
	"FrostAgent/internal/envinject"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/storage"
	"context"
	"os"
	"testing"
)

func TestSQLScopeExposesOnlyExplicitExtraEnv(t *testing.T) {
	const injected = "FROSTAGENT_SYNTHETIC_PROVIDER_TOKEN"
	const ordinary = "FROSTAGENT_SYNTHETIC_HOST_ONLY_TOKEN"
	if _, exists := os.LookupEnv(injected); exists {
		t.Skip("synthetic injected key already exists")
	}
	t.Cleanup(func() { _ = os.Unsetenv(injected) })
	t.Setenv(ordinary, "host-only")
	ctx := context.Background()
	db, err := storage.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := envinject.Put(ctx, db, injected, "injected-token"); err != nil {
		t.Fatal(err)
	}
	if err := envinject.ApplyAtStartup(ctx, db); err != nil {
		t.Fatal(err)
	}
	config, err := instanceconfig.OpenDatabase(db, "synthetic-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	scope := New(config, nil, nil)
	if got := scope.Getenv(injected); got != "injected-token" {
		t.Fatalf("injected key unavailable to runtime: %q", got)
	}
	if got := scope.Getenv(ordinary); got != "" {
		t.Fatalf("ordinary host environment leaked into SQL runtime: %q", got)
	}
}
