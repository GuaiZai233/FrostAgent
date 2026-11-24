package richimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Dockerfile bakes the same pinned fonts into the image; its URLs, file
// names and SHA-256 values must never drift from fontstore.go.
func TestDockerfileFontPinsMatchGo(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Skipf("Dockerfile not available: %v", err)
	}
	dockerfile := string(raw)
	for _, rf := range []remoteFont{lxgwZhenKai, stixTwoMath} {
		for what, want := range map[string]string{"url": rf.url, "sha256": rf.sha256, "file name": rf.name} {
			if !strings.Contains(dockerfile, want) {
				t.Errorf("Dockerfile is missing the pinned %s for %s: %s", what, rf.name, want)
			}
		}
	}
	if !strings.Contains(dockerfile, FontDirEnvKey+"=/usr/share/fonts/frostagent") {
		t.Error("Dockerfile must point " + FontDirEnvKey + " at the baked-in fonts")
	}
}
