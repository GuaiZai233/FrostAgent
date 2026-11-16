package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

func TestAwarenessSettingsAreManagedAndValidated(t *testing.T) {
	keys := ExportableValueKeys()
	for _, key := range []string{proactive.AwarenessEnabled, proactive.AwarenessInterval, proactive.AwarenessPrivateWhitelist,
		proactive.AwarenessGroupWhitelist, proactive.AwarenessPrivateWhitelistEnabled, proactive.AwarenessGroupWhitelistEnabled,
		proactive.AwarenessQuietHoursEnabled, proactive.AwarenessQuietHoursStart, proactive.AwarenessQuietHoursEnd} {
		if !keys[key] || !IsManagedKey(key) {
			t.Fatalf("setting missing from SQL export or catalog: %s", key)
		}
	}
	for _, value := range []string{"", "30m - 2h", "1h - 1h"} {
		if err := validateUpdate(proactive.AwarenessInterval, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"2h - 30m", "30s - 2h", "0m - 2h"} {
		if err := validateUpdate(proactive.AwarenessInterval, value); err == nil {
			t.Fatalf("accepted invalid interval %q", value)
		}
	}
	if err := validateUpdate(proactive.AwarenessEnabled, "yes"); err == nil {
		t.Fatal("accepted invalid switch")
	}
	if err := validateUpdate(proactive.AwarenessQuietHoursEnabled, "yes"); err == nil {
		t.Fatal("accepted invalid quiet hours switch")
	}
	for _, key := range []string{proactive.AwarenessQuietHoursStart, proactive.AwarenessQuietHoursEnd} {
		for _, value := range []string{"", "00:00", "07:00", "23:59"} {
			if err := validateUpdate(key, value); err != nil {
				t.Errorf("%s rejected %q: %v", key, value, err)
			}
		}
		for _, value := range []string{"24:00", "7:00", "23:60", "07:00:00"} {
			if err := validateUpdate(key, value); err == nil {
				t.Errorf("%s accepted invalid time %q", key, value)
			}
		}
	}
}

func TestAwarenessQuietHoursPersistenceDefaultsAndHotApply(t *testing.T) {
	t.Setenv("FROSTAGENT_DB_DRIVER", "sqlite")
	t.Setenv("FROSTAGENT_DB_DSN", filepath.Join(t.TempDir(), "settings.db"))
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defaults := map[string]string{proactive.AwarenessQuietHoursEnabled: "true", proactive.AwarenessQuietHoursStart: "23:00", proactive.AwarenessQuietHoursEnd: "07:00"}
	if err := instanceconfig.InitializeInstanceSettings(db, "fixture-fresh"); err != nil {
		t.Fatal(err)
	}
	fresh, err := instanceconfig.OpenDatabase(db, "fixture-fresh", false)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range defaults {
		if fresh.Get(key) != want || instanceconfig.InstanceRestartKeys[key] {
			t.Fatalf("%s must default to %q and support hot apply", key, want)
		}
	}
	store, err := instanceconfig.OpenDatabase(db, "fixture-existing", false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(store, nil, nil)
	defer scope.Cancel()
	svc := NewScoped(store, nil, nil)
	now := time.Date(2026, time.January, 2, 22, 45, 0, 0, time.Local)
	if proactive.AwarenessQuietHoursActive(scope.Getenv, now) || !proactive.AwarenessQuietHoursActive(scope.Getenv, now.Add(time.Hour)) {
		t.Fatal("existing instance did not use 23:00–07:00 defaults")
	}
	custom := map[string]string{proactive.AwarenessQuietHoursEnabled: "true", proactive.AwarenessQuietHoursStart: "22:30", proactive.AwarenessQuietHoursEnd: "08:15"}
	var updates []*v1.EnvVar
	for key, value := range custom {
		updates = append(updates, &v1.EnvVar{Key: key, Value: value})
	}
	response, err := svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{Updates: updates}))
	if err != nil || !response.Msg.Success {
		t.Fatalf("update: %v %v", response, err)
	}
	reopened, err := instanceconfig.OpenDatabase(db, "fixture-existing", false)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range custom {
		if reopened.Get(key) != want || scope.Getenv(key) != want {
			t.Fatalf("%s did not persist or hot apply", key)
		}
	}
	if !proactive.AwarenessQuietHoursActive(scope.Getenv, now) {
		t.Fatal("custom window did not apply immediately")
	}
	off, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: proactive.AwarenessQuietHoursEnabled, Value: "false"}))
	if err != nil || !off.Msg.Success || proactive.AwarenessQuietHoursActive(scope.Getenv, now) {
		t.Fatalf("disable: %v %v", off, err)
	}
	var clear []string
	for key := range defaults {
		clear = append(clear, key)
	}
	response, err = svc.UpdateEnvVars(context.Background(), connect.NewRequest(&v1.UpdateEnvVarsRequest{ClearKeys: clear}))
	if err != nil || !response.Msg.Success || proactive.AwarenessQuietHoursActive(scope.Getenv, now) || !proactive.AwarenessQuietHoursActive(scope.Getenv, now.Add(time.Hour)) {
		t.Fatalf("clearing settings did not restore defaults: %v %v", response, err)
	}
}
