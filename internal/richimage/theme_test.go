package richimage

import (
	"testing"
	"time"
)

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 25, hour, minute, 0, 0, time.Local)
}

func TestDayNightBoundaries(t *testing.T) {
	cases := []struct {
		when time.Time
		want string
	}{
		{at(19, 59), "light"},
		{at(20, 0), "dark"},
		{at(23, 59), "dark"},
		{at(0, 0), "dark"},
		{at(3, 59), "dark"},
		{at(4, 0), "light"},
		{at(12, 0), "light"},
	}
	for _, tc := range cases {
		if got := DayNightTheme(tc.when); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.when.Format("15:04"), got, tc.want)
		}
	}
}

func TestParseThemeMode(t *testing.T) {
	for raw, want := range map[string]string{
		"":          ThemeModeModel,
		"  ":        ThemeModeModel,
		"model":     ThemeModeModel,
		"day_night": ThemeModeDayNight,
	} {
		got, err := ParseThemeMode(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"dark", "auto", "DAY_NIGHT"} {
		if _, err := ParseThemeMode(raw); err == nil {
			t.Errorf("%q must be rejected", raw)
		}
	}
}

// In day/night mode the model's requested theme must never decide the result.
func TestDayNightIgnoresRequestedTheme(t *testing.T) {
	night, day := at(22, 0), at(10, 0)
	for _, requested := range []string{"", "light", "dark"} {
		if got := EffectiveTheme(ThemeModeDayNight, requested, night); got != "dark" {
			t.Errorf("night with request %q: got %s", requested, got)
		}
		if got := EffectiveTheme(ThemeModeDayNight, requested, day); got != "light" {
			t.Errorf("day with request %q: got %s", requested, got)
		}
	}
}

// Model mode passes the requested theme through unchanged, ignoring the clock.
func TestModelModeUsesRequestedTheme(t *testing.T) {
	if got := EffectiveTheme(ThemeModeModel, "dark", at(10, 0)); got != "dark" {
		t.Errorf("model mode must keep the requested dark, got %s", got)
	}
	if got := EffectiveTheme(ThemeModeModel, "light", at(22, 0)); got != "light" {
		t.Errorf("model mode must keep the requested light at night, got %s", got)
	}
}
