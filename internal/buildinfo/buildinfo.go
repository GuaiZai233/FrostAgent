// Package buildinfo exposes the identity of the source used to build FrostAgent.
package buildinfo

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// Version and Commit are set at build time with go build -ldflags -X.
// Version is reserved for version-tag builds. Commit retains the full revision.
var (
	Version string
	Commit  string
)

var revisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

// DisplayVersion returns the injected release version or a seven-character
// source revision. Direct local builds use Go's embedded VCS metadata, never
// local tags or the runtime working directory. Modified trees keep the revision
// without a dirty suffix; this value is an identifier, not a provenance proof.
func DisplayVersion() string {
	info, _ := debug.ReadBuildInfo()
	return displayVersion(Version, Commit, info)
}

func displayVersion(version, commit string, info *debug.BuildInfo) string {
	if version != "" {
		return strings.TrimPrefix(version, "v")
	}
	if revisionPattern.MatchString(commit) {
		return strings.ToLower(commit[:7])
	}
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && revisionPattern.MatchString(setting.Value) {
				return strings.ToLower(setting.Value[:7])
			}
		}
	}
	return "unknown"
}
