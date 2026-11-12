package settings

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/thinking"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"testing"
)

func TestThinkingStatusSettingPersistenceAndHotApply(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := instanceconfig.OpenDatabase(db, "synthetic-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(store, nil, nil)
	scope := runtimescope.New(store, nil, nil)
	if got := scope.Getenv(thinking.EnvKey); got != "" {
		t.Fatalf("legacy instance must have an unset thinking status, got %q", got)
	}
	if err := instanceconfig.InitializeInstanceSettings(db, "fresh-instance"); err != nil {
		t.Fatal(err)
	}
	fresh, err := instanceconfig.OpenDatabase(db, "fresh-instance", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := fresh.Get(thinking.EnvKey); got != "true" {
		t.Fatalf("new instance must default to enabled, got %q", got)
	}
	for _, value := range []string{"false", "true"} {
		res, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{{Key: thinking.EnvKey, Value: value}}}))
		if err != nil || !res.Msg.Success {
			t.Fatalf("update: %v %v", res, err)
		}
		reopened, err := instanceconfig.OpenDatabase(db, "synthetic-instance", false)
		if err != nil || reopened.Get(thinking.EnvKey) != value || scope.Getenv(thinking.EnvKey) != value {
			t.Fatalf("not persisted/hot: %v", err)
		}
	}
	if !ExportableValueKeys()[thinking.EnvKey] || instanceconfig.InstanceRestartKeys[thinking.EnvKey] {
		t.Fatal("must export and hot apply")
	}
	for _, value := range []string{"", "true", "false"} {
		if err := validateUpdate(thinking.EnvKey, value); err != nil {
			t.Fatal(err)
		}
	}
	if validateUpdate(thinking.EnvKey, "yes") == nil {
		t.Fatal("accepted invalid boolean")
	}
	if _, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: thinking.EnvKey})); err != nil {
		t.Fatal(err)
	}
	if got := scope.Getenv(thinking.EnvKey); got != "" {
		t.Fatalf("clearing an override must restore default-on behavior, got %q", got)
	}
	if got := svc.resetHint(thinking.EnvKey); got != "开启" {
		t.Fatalf("reset hint must match default-on behavior, got %q", got)
	}
}
