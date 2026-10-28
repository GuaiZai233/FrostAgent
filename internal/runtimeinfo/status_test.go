package runtimeinfo

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		start       time.Time
		version     string
		uptime      string
		wantVersion string
	}{
		{"example", now.Add(-(233*24*time.Hour + 21*time.Hour + 16*time.Minute + 59*time.Second)), "1.0.0", "233d 21h 16m", "1.0.0"},
		{"sub-minute", now.Add(-59 * time.Second), "a1b2c3d", "0d 0h 0m", "a1b2c3d"},
		{"minute", now.Add(-time.Minute), "1.2.3-rc.1", "0d 0h 1m", "1.2.3-rc.1"},
		{"hour", now.Add(-time.Hour), "unknown", "0d 1h 0m", "unknown"},
		{"day", now.Add(-24 * time.Hour), "", "1d 0h 0m", "unknown"},
		{"missing-start", time.Time{}, "", "0d 0h 0m", "unknown"},
		{"future-start", now.Add(time.Hour), "", "0d 0h 0m", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := fmt.Sprintf("FrostAgent 运行状态\n[ 200 OK ]\nArch: %s\nUptime: %s\n\nVer: %s", runtime.GOARCH, tc.uptime, tc.wantVersion)
			if got := Format(tc.version, tc.start, now); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestEnabledDefaultsOn(t *testing.T) {
	for _, value := range []string{"", "true", "false"} {
		if Enabled(value) != (value != "false") {
			t.Fatalf("unexpected setting %q", value)
		}
	}
}
