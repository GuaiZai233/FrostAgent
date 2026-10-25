package settings

import (
	"context"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
)

func TestActionsCatSwitchSQLPersistenceAndInstanceIsolation(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stores := make([]*instanceconfig.Store, 2)
	for i, id := range []string{"test-first", "test-second"} {
		if err := db.CreateInstance(context.Background(), storage.InstanceRecord{ID: id, Name: id, CreatedAt: time.Now()}, 2); err != nil {
			t.Fatal(err)
		}
		if err := instanceconfig.InitializeInstanceSettings(db, id); err != nil {
			t.Fatal(err)
		}
		stores[i], err = instanceconfig.OpenDatabase(db, id, false)
		if err != nil {
			t.Fatal(err)
		}
		if stores[i].Get("ACTIONSCAT_ENABLED") != "true" {
			t.Fatal("new instance must default to enabled")
		}
	}
	svc := NewScoped(stores[0], nil, nil)
	client := actionscat.New(runtimescope.New(stores[0], nil, nil).Getenv)
	for _, value := range []string{"false", "true", "false"} {
		res, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: "ACTIONSCAT_ENABLED", Value: value}))
		if err != nil || !res.Msg.Success || client.IsEnabled() != (value == "true") {
			t.Fatalf("switch update = %v, error=%v, enabled=%v", res, err, client.IsEnabled())
		}
		if stores[1].Get("ACTIONSCAT_ENABLED") != "true" {
			t.Fatal("instance switch affected another instance")
		}
	}
	reopened, err := instanceconfig.OpenDatabase(db, "test-first", false)
	if err != nil || reopened.Get("ACTIONSCAT_ENABLED") != "false" {
		t.Fatalf("disabled setting did not persist: error=%v", err)
	}
	res, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: "ACTIONSCAT_ENABLED"}))
	if err != nil || !res.Msg.Success || !client.IsEnabled() {
		t.Fatalf("delete did not restore enabled default: %v, %v", res, err)
	}
	if instanceconfig.InstanceRestartKeys["ACTIONSCAT_ENABLED"] || !ExportableValueKeys()["ACTIONSCAT_ENABLED"] {
		t.Fatal("switch must be hot-readable and included in settings backup")
	}
}
