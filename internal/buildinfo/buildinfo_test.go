package buildinfo

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestDisplayVersion(t *testing.T) {
	commit := "a1b2c3d" + strings.Repeat("e", 33)
	vcs := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "123abcd" + strings.Repeat("f", 33)},
		{Key: "vcs.modified", Value: "true"},
	}}
	for _, tt := range []struct {
		name, version, commit string
		info                  *debug.BuildInfo
		want                  string
	}{
		{"release", "1.2.3", commit, vcs, "1.2.3"},
		{"release prefix", "v1.2.3", commit, vcs, "1.2.3"},
		{"prerelease", "1.2.3-rc.1+build.2", commit, nil, "1.2.3-rc.1+build.2"},
		{"commit", "", commit, vcs, "a1b2c3d"},
		{"sha256", "", commit + strings.Repeat("a", 24), nil, "a1b2c3d"},
		{"uppercase", "", strings.ToUpper(commit), nil, "a1b2c3d"},
		{"vcs fallback ignores dirty", "", "", vcs, "123abcd"},
		{"invalid injected commit", "", "not-a-revision", vcs, "123abcd"},
		{"short hash unavailable", "", "a1b2c3d", nil, "unknown"},
		{"no metadata", "", "", nil, "unknown"},
		{"empty metadata", "", "", &debug.BuildInfo{}, "unknown"},
		{"invalid vcs", "", "", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "truncated"}}}, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayVersion(tt.version, tt.commit, tt.info); got != tt.want {
				t.Fatalf("displayVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
