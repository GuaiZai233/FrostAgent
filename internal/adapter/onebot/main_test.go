package onebot

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Existing wire-level tests assert the first frame sent to the fake peer.
	implementationProbeEnabled = false
	os.Exit(m.Run())
}
