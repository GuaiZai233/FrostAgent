package proactive

import (
	"testing"
	"time"
)

func TestAwarenessQuietTimeValidation(t *testing.T) {
	for raw, want := range map[string]int{"00:00": 0, "07:00": 420, "23:59": 1439, " 09:30 ": 570} {
		got, err := ParseAwarenessQuietTime(raw)
		if err != nil || got != want {
			t.Errorf("ParseAwarenessQuietTime(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "7:00", "24:00", "23:60", "-1:00", "07:00:00", "07:0a", "morning"} {
		if _, err := ParseAwarenessQuietTime(raw); err == nil {
			t.Errorf("accepted invalid time %q", raw)
		}
	}
}

func TestAwarenessQuietHoursBoundariesAndDefaults(t *testing.T) {
	location := time.FixedZone("fixture-local", 8*60*60)
	for _, tc := range []struct {
		name, start, end, enabled, clock string
		want                             bool
	}{
		{"default before start", "", "", "", "22:59:59", false},
		{"default start inclusive", "", "", "", "23:00:00", true},
		{"default midnight", "", "", "", "00:00:00", true},
		{"default before end", "", "", "", "06:59:59", true},
		{"default end exclusive", "", "", "", "07:00:00", false},
		{"day before start", "09:30", "12:15", "true", "09:29:59", false},
		{"day start", "09:30", "12:15", "true", "09:30:00", true},
		{"day before end", "09:30", "12:15", "true", "12:14:59", true},
		{"day end", "09:30", "12:15", "true", "12:15:00", false},
		{"all day", "08:00", "08:00", "true", "12:00:00", true},
		{"all day before bound", "08:00", "08:00", "true", "07:59:59", true},
		{"disabled", "23:00", "07:00", "false", "23:00:00", false},
		{"invalid start blocks", "24:00", "07:00", "true", "12:00:00", true},
		{"invalid end blocks", "23:00", "bad", "true", "12:00:00", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{AwarenessQuietHoursStart: tc.start, AwarenessQuietHoursEnd: tc.end, AwarenessQuietHoursEnabled: tc.enabled}
			now, err := time.ParseInLocation("15:04:05", tc.clock, location)
			if err != nil {
				t.Fatal(err)
			}
			if got := AwarenessQuietHoursActive(func(key string) string { return values[key] }, now); got != tc.want {
				t.Fatalf("quiet = %v, want %v at %s", got, tc.want, now)
			}
		})
	}
}
