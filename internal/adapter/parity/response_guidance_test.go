package parity

import (
	"testing"
	"time"
)

func TestResponseGuidanceTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want string
	}{
		{"requested format", time.Date(2026, 11, 7, 15, 43, 0, 0, time.UTC), "2026-11-7 23:43(UTC+8)。"},
		{"next minute", time.Date(2026, 11, 7, 15, 44, 0, 0, time.UTC), "2026-11-7 23:44(UTC+8)。"},
		{"date rollover", time.Date(2026, 11, 7, 16, 0, 0, 0, time.UTC), "2026-11-8 00:00(UTC+8)。"},
		{"different host zone", time.Date(2026, 11, 7, 10, 43, 0, 0, time.FixedZone("test-zone", -5*60*60)), "2026-11-7 23:43(UTC+8)。"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CurrentTimeGuidance(tc.now)
			if got != "当前系统时间："+tc.want {
				t.Fatalf("unexpected guidance: %q", got)
			}
		})
	}
}
