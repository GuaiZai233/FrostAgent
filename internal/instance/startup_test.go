package instance

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"net/http"
	"reflect"
	"testing"
)

func TestStartupConstructsEachRuntimeOnce(t *testing.T) {
	seed := testManager(t)
	a := create(t, seed, "enabled-a")
	b := create(t, seed, "enabled-b")
	disabled := create(t, seed, "disabled")
	for _, info := range []Info{a, b} {
		if err := seed.Enable(info.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	seed.Close()

	calls := map[string][]bool{}
	db, err := storage.Open(context.Background(), seed.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	global, err := instanceconfig.OpenDatabase(db, "", true)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newManager(seed.root, global, db,
		func(m *Manager, item *managed, config *instanceconfig.Store, enabled bool) (*Runtime, error) {
			calls[item.id] = append(calls[item.id], enabled)
			return buildManagedRuntime(m, item, config, enabled)
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	for _, info := range []Info{a, b, disabled} {
		want := []bool{info.ID != disabled.ID}
		if !reflect.DeepEqual(calls[info.ID], want) {
			t.Fatalf("instance %s runtime constructions = %v, want %v", info.Name, calls[info.ID], want)
		}
		item := manager.instances[info.ID]
		if item.config == nil || item.runtime == nil {
			t.Fatalf("instance %s has no management runtime/config", info.Name)
		}
		if active := item.runtime.Scope.Context().Err() == nil; active != want[0] {
			t.Fatalf("instance %s active = %t, want %t", info.Name, active, want[0])
		}
	}
	response := rpc(t, manager, disabled.ID, "SettingsService/UpdateEnvVar", `{"key":"BOT_NAME","value":"changed"}`, false)
	if response.Code != http.StatusOK {
		t.Fatalf("disabled instance settings: %d %s", response.Code, response.Body.String())
	}
	original := manager.instances[a.ID].runtime
	if err := manager.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if manager.instances[a.ID].runtime != original || len(calls[a.ID]) != 1 {
		t.Fatal("idempotent enable rebuilt the running instance")
	}
	if err := manager.Enable(a.ID, false); err != nil {
		t.Fatal(err)
	}
	stopped := manager.instances[a.ID].runtime
	if original.Scope.Context().Err() == nil || stopped.Scope.Context().Err() == nil {
		t.Fatal("disable retained active runtime resources")
	}
	if err := manager.Enable(a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := calls[a.ID]; !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Fatalf("startup and restart constructions = %v", got)
	}
	if stopped.Scope.Context().Err() == nil || manager.instances[a.ID].runtime.Scope.Context().Err() != nil {
		t.Fatal("restart did not replace the stopped runtime with an active runtime")
	}
	manager.Close()
	for _, item := range manager.instances {
		if item.runtime.Scope.Context().Err() == nil {
			t.Fatal("manager close retained an active runtime")
		}
	}
}

func TestStartupMalformedEnabledInstanceRemainsRepairable(t *testing.T) {
	seed := testManager(t)
	bad := create(t, seed, "bad-enabled")
	good := create(t, seed, "good-enabled")
	for _, info := range []Info{bad, good} {
		if err := seed.Enable(info.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	setInvalidConnection(t, seed, bad.ID)
	seed.Close()
	manager, err := NewDatabase(seed.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	infos, _ := manager.List()
	for _, info := range infos {
		if info.ID == bad.ID && (info.Enabled || info.Error == "") {
			t.Fatalf("malformed enabled instance state = %+v", info)
		}
	}
	badItem := manager.instances[bad.ID]
	if badItem.config == nil || badItem.runtime == nil || badItem.runtime.Scope.Context().Err() == nil {
		t.Fatal("malformed instance did not retain a stopped, repairable management runtime")
	}
	if manager.instances[good.ID].runtime.Scope.Context().Err() != nil {
		t.Fatal("malformed peer blocked healthy startup")
	}
	response := rpc(t, manager, bad.ID, "SettingsService/UpdateEnvVar", `{"key":"ONEBOT_WS_URL","value":"ws://127.0.0.1:1"}`, false)
	if response.Code != http.StatusOK {
		t.Fatalf("repair settings: %d %s", response.Code, response.Body.String())
	}
	if err := manager.Enable(bad.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := manager.instances[bad.ID].runtime.Scope.Getenv("ONEBOT_WS_URL"); got != "ws://127.0.0.1:1" {
		t.Fatalf("repaired runtime ONEBOT_WS_URL = %q", got)
	}
}
