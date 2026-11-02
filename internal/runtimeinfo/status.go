// Package runtimeinfo formats the reserved, model-independent runtime diagnostic.
package runtimeinfo

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

const Command = "#fa"
const EnabledEnv = "ENABLE_RUNTIME_STATUS_COMMAND"

// Enabled preserves the default-on behavior when the setting is unset.
func Enabled(value string) bool { return value != "false" }

// Format reports instance uptime, not host uptime or external service health.
func Format(version string, startedAt, now time.Time) string {
	var minutes int64
	if !startedAt.IsZero() && now.After(startedAt) {
		minutes = int64(now.Sub(startedAt) / time.Minute)
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("FrostAgent 运行状态\n[ 200 OK ]\nArch: %s\nUptime: %dd %dh %dm\n\nVer: %s",
		runtime.GOARCH, minutes/(24*60), minutes/60%24, minutes%60, version)
}
