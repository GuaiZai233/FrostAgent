package buildinfo

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Compile and execute a real binary so a broken linker symbol or VCS fallback
// cannot pass solely because the display helper's unit tests still work.
func TestBuiltBinaryMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles probe binaries")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal(err)
	}
	const pkg = "github.com/GuaiZai233/FrostAgent/internal/buildinfo."
	commit := "a1b2c3d" + strings.Repeat("e", 33)
	for _, tt := range []struct {
		name, version, commit, display string
	}{
		{"release", "1.2.3", commit, "1.2.3"},
		{"prerelease", "1.2.3-rc.1+fixture", commit, "1.2.3-rc.1+fixture"},
		{"source", "", commit, "a1b2c3d"},
		{"no vcs", "", "", "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flags := "-X " + pkg + "Version=" + tt.version + " -X " + pkg + "Commit=" + tt.commit
			got := buildAndRunProbe(t, "-buildvcs=false", "-ldflags="+flags)
			if got["display"] != tt.display || got["version"] != tt.version || got["commit"] != tt.commit {
				t.Fatalf("built binary = %#v, want display=%q, version=%q, commit=%q", got, tt.display, tt.version, tt.commit)
			}
		})
	}
	t.Run("local vcs", func(t *testing.T) {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		out, err := cmd.Output()
		if err != nil {
			t.Skip("source archive has no Git metadata")
		}
		head := strings.TrimSpace(string(out))
		if !revisionPattern.MatchString(head) {
			t.Fatalf("invalid fixture checkout revision: %q", head)
		}
		got := buildAndRunProbe(t, "-buildvcs=true")
		if got["display"] != head[:7] || got["version"] != "" || got["commit"] != "" {
			t.Fatalf("local built binary = %#v, want revision %s without a release version or dirty suffix", got, head[:7])
		}
	})
}

func buildAndRunProbe(t *testing.T, flags ...string) map[string]string {
	t.Helper()
	name := "buildinfo-probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	args := append([]string{"build", "-o", binary}, flags...)
	args = append(args, "./testdata/probe")
	cmd := exec.Command("go", args...)
	// The fixture must be executable on the host even in cross-compilation CI.
	cmd.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(binary).CombinedOutput()
	if err != nil {
		t.Fatalf("run probe: %v\n%s", err, out)
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode probe: %v\n%s", err, out)
	}
	return result
}
