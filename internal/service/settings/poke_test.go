package settings

import (
	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/poke"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/storage"
	"connectrpc.com/connect"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPokeSettingsPersistenceHotApplyAndClear(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := instanceconfig.OpenDatabase(db, "poke-test", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(store, nil, nil)
	scope := runtimescope.New(store, nil, nil)
	if scope.Getenv("ENABLE_POKE") == "true" || poke.Cooldown(scope.Getenv) != 30*time.Second {
		t.Fatal("incorrect defaults")
	}
	res, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{
		{Key: "ENABLE_POKE", Value: "true"}, {Key: "POKE_CUSTOM_TEXT", Value: "的耳朵"}, {Key: "POKE_COOLDOWN_SECONDS", Value: "15"},
	}}))
	if err != nil || !res.Msg.Success {
		t.Fatalf("save=%v error=%v", res, err)
	}
	if scope.Getenv("ENABLE_POKE") != "true" || poke.Cooldown(scope.Getenv) != 15*time.Second || poke.Message(scope.Getenv("POKE_CUSTOM_TEXT")) != "（用户戳了戳你 的耳朵）" {
		t.Fatal("hot apply failed")
	}
	reopened, err := instanceconfig.OpenDatabase(db, "poke-test", false)
	if err != nil || reopened.Get("POKE_CUSTOM_TEXT") != "的耳朵" {
		t.Fatalf("persist failed: %v", err)
	}
	for _, key := range []string{"ENABLE_POKE", "POKE_CUSTOM_TEXT", "POKE_COOLDOWN_SECONDS"} {
		if instanceconfig.InstanceRestartKeys[key] || !ExportableValueKeys()[key] {
			t.Fatalf("setting must hot apply and export: %s", key)
		}
		if _, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: key})); err != nil {
			t.Fatal(err)
		}
	}
	if scope.Getenv("ENABLE_POKE") == "true" || scope.Getenv("POKE_CUSTOM_TEXT") != "" || poke.Cooldown(scope.Getenv) != 30*time.Second {
		t.Fatal("clear did not restore defaults")
	}
}
func TestPokeSettingsValidation(t *testing.T) {
	for _, value := range []string{"-1", "1.5", "NaN", "86401", "99999999999999999999999"} {
		if validateUpdate("POKE_COOLDOWN_SECONDS", value) == nil {
			t.Fatalf("accepted invalid cooldown %q", value)
		}
	}
	for _, value := range []string{"", "0", "30", "86400"} {
		if err := validateUpdate("POKE_COOLDOWN_SECONDS", value); err != nil {
			t.Fatal(err)
		}
	}
	if validateUpdate("ENABLE_POKE", "yes") == nil {
		t.Fatal("accepted invalid toggle")
	}
	if validateUpdate("POKE_CUSTOM_TEXT", "line\nnext") == nil {
		t.Fatal("accepted multiline")
	}
}
