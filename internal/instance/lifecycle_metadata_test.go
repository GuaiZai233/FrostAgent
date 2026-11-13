package instance

import (
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/settingscontract"
)

func TestSettingsLifecycleSQLClassification(t *testing.T) {
	entries := settingscontract.Load(t)
	for key, entry := range entries {
		t.Run(key, func(t *testing.T) {
			hot := hotInstanceSettings
			if entry.Scope == "global" {
				hot = hotGlobalSettings
			}
			if hot[key] != entry.SQLHot {
				t.Errorf("%s: SQL hot table=%v, inventory=%v (%s)", key, hot[key], entry.SQLHot, entry.Scope)
			}
			if entry.Scope != "global" && entry.SQLHot && (entry.InstanceRestart || entry.ControlRestart) {
				t.Errorf("%s: confirmed instance hot setting conflicts with restart tables", key)
			}
			// Exercise insert, replacement and deletion, keeping an unrelated key
			// unchanged. Missing catalog restart flags never imply hot support.
			for _, change := range []struct {
				name          string
				before, after map[string]string
			}{
				{"insert", map[string]string{"SYNTHETIC_UNCHANGED_SETTING": "sentinel"}, map[string]string{"SYNTHETIC_UNCHANGED_SETTING": "sentinel", key: "first"}},
				{"replace", map[string]string{key: "first"}, map[string]string{key: "second"}},
				{"delete", map[string]string{key: "first"}, map[string]string{}},
				{"insert empty", map[string]string{}, map[string]string{key: ""}},
				{"replace with empty", map[string]string{key: "first"}, map[string]string{key: ""}},
				{"delete empty", map[string]string{key: ""}, map[string]string{}},
			} {
				if entry.Scope == "global" {
					if got := needsGlobalRuntimeReload(change.before, change.after); got != !entry.SQLHot {
						t.Errorf("%s/%s: global reload=%v, want %v", key, change.name, got, !entry.SQLHot)
					}
				} else if got := onlyHotInstanceChanges(change.before, change.after); got != entry.SQLHot {
					t.Errorf("%s/%s: onlyHotInstanceChanges=%v, want %v", key, change.name, got, entry.SQLHot)
				}
			}
		})
	}
	for name, keys := range map[string]map[string]bool{"instance": hotInstanceSettings, "global": hotGlobalSettings} {
		for key := range keys {
			entry, exists := entries[key]
			if !exists || (name == "global") != (entry.Scope == "global") {
				t.Errorf("%s: %s hot table has no matching scope inventory", key, name)
			}
		}
	}
	if onlyHotInstanceChanges(nil, nil) || needsGlobalRuntimeReload(nil, nil) {
		t.Error("no-op settings change must not request an apply")
	}
	for _, key := range []string{"AGENT_MAX_ITERATIONS", "GROUP_COMPACT_BUFFER_SIZE", "MEMORY_REFLECTION_TIMEOUT", "SYNTHETIC_UNKNOWN_SETTING"} {
		if onlyHotInstanceChanges(nil, map[string]string{"BOT_NAME": "new", key: "new"}) {
			t.Errorf("%s: mixed draft incorrectly classified as hot", key)
		}
	}
	if !needsGlobalRuntimeReload(nil, map[string]string{"SYNTHETIC_UNKNOWN_SETTING": "new"}) {
		t.Error("unknown global keys must retain conservative reload behavior")
	}
}
