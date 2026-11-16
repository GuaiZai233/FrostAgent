package settings

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/settingscontract"
	"github.com/joho/godotenv"
)

func TestSettingsLifecycleCatalogScopesAndDefaults(t *testing.T) {
	entries := loadSettingsLifecycle(t)
	defaults, err := godotenv.Unmarshal(instanceconfig.Template)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	global, err := instanceconfig.Open(filepath.Join(t.TempDir(), "global.env"), true)
	if err != nil {
		t.Fatal(err)
	}
	scoped := NewScoped(instance, global, nil)
	globalOnly := NewScoped(nil, global, nil)
	for key, entry := range entries {
		t.Run(key, func(t *testing.T) {
			meta, exists := knownEnvVars[key]
			if exists != (entry.Catalog != "absent") {
				t.Errorf("%s: catalog presence=%v, inventory=%s", key, exists, entry.Catalog)
			}
			if exists && meta.RequiresRestart != (entry.Catalog == "restart") {
				t.Errorf("%s: catalog RequiresRestart=%v, inventory=%s", key, meta.RequiresRestart, entry.Catalog)
			}
			for _, check := range []struct {
				name      string
				got, want bool
			}{
				{"GlobalKeys", instanceconfig.GlobalKeys[key], entry.Scope == "global"},
				{"SharedKeys", instanceconfig.SharedKeys[key], entry.Scope == "shared"},
				{"InstanceRestartKeys", instanceconfig.InstanceRestartKeys[key], entry.InstanceRestart},
				{"ControlPlaneRestartKeys", instanceconfig.ControlPlaneRestartKeys[key], entry.ControlRestart},
			} {
				if check.got != check.want {
					t.Errorf("%s: %s=%v, inventory=%v", key, check.name, check.got, check.want)
				}
			}
			target := instance
			if entry.Scope == "global" {
				target = global
			}
			if scoped.store(key) != target {
				t.Errorf("%s: scoped service routes to the wrong store (%s)", key, entry.Scope)
			}
			globalTarget := global
			if entry.Scope == "instance" {
				globalTarget = nil
			}
			if globalOnly.store(key) != globalTarget {
				t.Errorf("%s: global-only service routes to the wrong store (%s)", key, entry.Scope)
			}
			value, seeded := defaults[key]
			if seeded != (entry.Seed != nil) || (seeded && value != *entry.Seed) {
				want := ""
				if entry.Seed != nil {
					want = *entry.Seed
				}
				t.Errorf("%s: new-instance default=%q (present=%v), inventory=%q (present=%v)", key, value, seeded, want, entry.Seed != nil)
			}
			if seeded && (entry.Scope != "instance" || !exists) {
				t.Errorf("%s: default must belong to the instance catalog", key)
			}
		})
	}
	checkInventoried := func(name, key string) {
		t.Helper()
		if _, exists := entries[key]; !exists {
			t.Errorf("%s: %s has no lifecycle inventory entry", key, name)
		}
	}
	for key := range knownEnvVars {
		checkInventoried("catalog", key)
	}
	for key := range defaults {
		checkInventoried("new-instance defaults", key)
	}
	for name, keys := range map[string]map[string]bool{
		"GlobalKeys": instanceconfig.GlobalKeys, "SharedKeys": instanceconfig.SharedKeys,
		"InstanceRestartKeys": instanceconfig.InstanceRestartKeys, "ControlPlaneRestartKeys": instanceconfig.ControlPlaneRestartKeys,
	} {
		for key := range keys {
			checkInventoried(name, key)
		}
	}
}

// Keep executable test helpers in _test.go so ordinary package coverage measures
// the production paths exercised by these tests.
func loadSettingsLifecycle(t *testing.T) map[string]settingscontract.Entry {
	t.Helper()
	var rows []settingscontract.Entry
	decoder := json.NewDecoder(bytes.NewReader(settingscontract.Inventory))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("empty lifecycle inventory")
	}
	entries := make(map[string]settingscontract.Entry, len(rows))
	for _, row := range rows {
		if _, exists := entries[row.Key]; row.Key == "" || exists {
			t.Fatalf("empty or duplicate lifecycle key %q", row.Key)
		}
		entries[row.Key] = row
	}
	return entries
}
