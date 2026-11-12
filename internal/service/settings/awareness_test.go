package settings

import (
	"github.com/GuaiZai233/FrostAgent/internal/proactive"
	"testing"
)

func TestAwarenessSettingsAreManagedAndValidated(t *testing.T) {
	keys := ExportableValueKeys()
	for _, key := range []string{proactive.AwarenessEnabled, proactive.AwarenessInterval, proactive.AwarenessPrivateWhitelist,
		proactive.AwarenessGroupWhitelist, proactive.AwarenessPrivateWhitelistEnabled, proactive.AwarenessGroupWhitelistEnabled} {
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
}
