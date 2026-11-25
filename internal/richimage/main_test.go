package richimage

import (
	"os"
	"testing"
)

// Unit tests stay offline: pinned fonts are used only when already cached
// (e.g. by an earlier run with RICHIMAGE_ONLINE=1); otherwise tests that need
// them skip or use the embedded Go fonts.
func TestMain(m *testing.M) {
	if os.Getenv("RICHIMAGE_ONLINE") == "" {
		fontDownloadEnabled = false
	}
	os.Exit(m.Run())
}
