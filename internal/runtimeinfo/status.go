// Package runtimeinfo formats the reserved, model-independent runtime diagnostic.
package runtimeinfo

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

const Command = "#fa"
const EnabledEnv = "ENABLE_RUNTIME_STATUS_COMMAND"

// Enabled preserves the default-on behavior when the setting is unset.
func Enabled(value string) bool { return value != "false" }

func architecture(goos, goarch string, docker bool) string {
	goarch = strings.TrimSpace(goarch)
	if goarch == "" {
		goarch = "Unknown"
	}
	platform := map[string]string{
		"darwin":  "Darwin",
		"freebsd": "FreeBSD",
		"linux":   "Linux",
		"openbsd": "OpenBSD",
		"windows": "Windows",
	}[strings.ToLower(strings.TrimSpace(goos))]
	if docker {
		platform = "Docker"
	}
	if platform == "" {
		platform = "Unknown"
	}
	return goarch + "-" + platform
}

func runningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func runtimeArchitecture() string {
	return architecture(runtime.GOOS, runtime.GOARCH, runningInDocker())
}

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
	return fmt.Sprintf("FrostAgent 运行状态\n[ 200 OK ]\n架构: %s\n运行时长: %dd %dh %dm\n\n版本: %s",
		runtimeArchitecture(), minutes/(24*60), minutes/60%24, minutes%60, version)
}
