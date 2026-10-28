package settings

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestRuntimeStatusSettingsPersistenceAndIsolation(t *testing.T) {
	const key = "ENABLE_RUNTIME_STATUS_COMMAND"
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	t.Setenv(key, "false")
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := instanceconfig.OpenDatabase(db, "runtime-status-a", false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := instanceconfig.OpenDatabase(db, "runtime-status-b", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(store, nil, nil)
	scope := runtimescope.New(store, nil, nil)
	otherScope := runtimescope.New(other, nil, nil)
	if scope.Getenv(key) != "" || otherScope.Getenv(key) == "false" {
		t.Fatal("unconfigured instances must use the enabled default, not process environment")
	}
	listed, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range listed.Msg.EnvVars {
		if value.Key == key {
			found = true
			if value.Value != "" || value.IsSecret || value.IsInherited || value.ResetHint != "开启" {
				t.Fatalf("incorrect runtime status setting metadata: %v", value)
			}
		}
	}
	if !found || instanceconfig.GlobalKeys[key] || instanceconfig.SharedKeys[key] || instanceconfig.InstanceRestartKeys[key] || !ExportableValueKeys()[key] {
		t.Fatal("runtime status must be a managed, exportable, hot instance setting")
	}
	for _, value := range []string{"false", "true", "false"} {
		res, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{
			{Key: key, Value: value}, {Key: "ADMIN_COMMAND_PREFIX", Value: "!"},
		}}))
		if err != nil || !res.Msg.Success {
			t.Fatalf("save=%v error=%v", res, err)
		}
		if scope.Getenv(key) != value || otherScope.Getenv(key) != "" {
			t.Fatal("hot update changed the wrong instance or was not applied")
		}
		reopened, err := instanceconfig.OpenDatabase(db, "runtime-status-a", false)
		if err != nil || reopened.Get(key) != value || reopened.Get("ADMIN_COMMAND_PREFIX") != "!" {
			t.Fatalf("settings did not survive reopening: %v", err)
		}
	}
	res, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: key}))
	if err != nil || !res.Msg.Success || scope.Getenv(key) != "" {
		t.Fatalf("clear did not restore the enabled default: response=%v error=%v", res, err)
	}
	reopened, err := instanceconfig.OpenDatabase(db, "runtime-status-a", false)
	if err != nil || reopened.Get(key) != "" {
		t.Fatalf("cleared override was persisted: %v", err)
	}
}

func TestRuntimeStatusSettingsValidation(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		if err := validateUpdate("ENABLE_RUNTIME_STATUS_COMMAND", value); err != nil {
			t.Fatalf("valid setting %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"yes", "0", "False", " false", "false\n"} {
		if validateUpdate("ENABLE_RUNTIME_STATUS_COMMAND", value) == nil {
			t.Fatalf("invalid setting %q accepted", value)
		}
	}
}

func TestRuntimeStatusAndPrefixSettingsSaveAtomically(t *testing.T) {
	const key = "ENABLE_RUNTIME_STATUS_COMMAND"
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := instanceconfig.OpenDatabase(db, "runtime-status-atomic", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(store, nil, nil)
	assertUnchanged := func(res *connect.Response[v1.UpdateEnvVarsResponse], err error) {
		t.Helper()
		if err != nil || res.Msg.Success || store.Get(key) != "" || store.Get("ADMIN_COMMAND_PREFIX") != "" {
			t.Fatalf("failed save partially updated the runtime: response=%v error=%v", res, err)
		}
		reopened, err := instanceconfig.OpenDatabase(db, "runtime-status-atomic", false)
		if err != nil || reopened.Get(key) != "" || reopened.Get("ADMIN_COMMAND_PREFIX") != "" {
			t.Fatalf("failed save partially persisted: %v", err)
		}
	}
	assertUnchanged(svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{
		{Key: key, Value: "false"}, {Key: "ADMIN_COMMAND_PREFIX", Value: "invalid\nprefix"},
	}})))
	if _, err := db.SQL.Exec(`CREATE TRIGGER reject_command_prefix BEFORE INSERT ON settings
		WHEN NEW.key = 'ADMIN_COMMAND_PREFIX' BEGIN SELECT RAISE(ABORT, 'synthetic write failure'); END`); err != nil {
		t.Fatal(err)
	}
	assertUnchanged(svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: []*v1.EnvVar{
		{Key: key, Value: "false"}, {Key: "ADMIN_COMMAND_PREFIX", Value: "!"},
	}})))
}
