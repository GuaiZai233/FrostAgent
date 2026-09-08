package settings

import (
	"FrostAgent/internal/instanceconfig"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyAdapterSelectionFlagsAreHiddenButStillParseable(t *testing.T) {
	for _, key := range []string{"ENABLE_ONEBOT_ADAPTER", "ENABLE_ASTRBOT_ADAPTER"} {
		if _, ok := knownEnvVars[key]; ok {
			t.Fatalf("legacy adapter selection flag is still exposed by Settings: %s", key)
		}
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("ENABLE_ONEBOT_ADAPTER=false\nENABLE_ASTRBOT_ADAPTER=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := instanceconfig.Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if store.Get("ENABLE_ONEBOT_ADAPTER") != "false" || store.Get("ENABLE_ASTRBOT_ADAPTER") != "false" {
		t.Fatal("legacy adapter flags are no longer parseable from existing instance configuration")
	}
}
