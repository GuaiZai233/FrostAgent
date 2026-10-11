package instance

import (
	"FrostAgent/internal/instanceconfig"
	"net/http"
	"os"
	"path/filepath"
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
	manager, err := newManager(seed.root, seed.global, seed.templateDialogue,
		func(m *Manager, item *managed, config *instanceconfig.Store, dir string, enabled bool) (*Runtime, error) {
			calls[item.id] = append(calls[item.id], enabled)
			return buildManagedRuntime(m, item, config, dir, enabled)
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
	response := rpc(t, manager, disabled.ID, "SettingsService/UpdateRawEnvFile", `{"content":"BOT_NAME=changed\n"}`, false)
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
	seed.Close()
	if err := os.WriteFile(filepath.Join(seed.dir(bad.ID), ".env"), []byte("BOT_NAME=\"unterminated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(seed.root, seed.global, seed.templateDialogue)
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
	if badItem.config == nil || badItem.config.Error() == nil || badItem.runtime == nil || badItem.runtime.Scope.Context().Err() == nil {
		t.Fatal("malformed instance did not retain a stopped, repairable management runtime")
	}
	if manager.instances[good.ID].runtime.Scope.Context().Err() != nil {
		t.Fatal("malformed peer blocked healthy startup")
	}
	response := rpc(t, manager, bad.ID, "SettingsService/UpdateRawEnvFile", `{"content":"BOT_NAME=repaired\n"}`, false)
	if response.Code != http.StatusOK {
		t.Fatalf("repair settings: %d %s", response.Code, response.Body.String())
	}
	if err := manager.Enable(bad.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := manager.instances[bad.ID].runtime.Scope.Getenv("BOT_NAME"); got != "repaired" {
		t.Fatalf("repaired runtime BOT_NAME = %q", got)
	}
}
