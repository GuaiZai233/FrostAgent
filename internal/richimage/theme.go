package richimage

import (
	"fmt"
	"strings"
	"time"
)

// ThemeModeKey is the server setting that decides how the colour theme of a
// rendered image is chosen.
const ThemeModeKey = "RICH_IMAGE_THEME_MODE"

const (
	// ThemeModeModel lets the model choose light or dark per call. It is the default.
	ThemeModeModel = "model"
	// ThemeModeDayNight ignores the model and uses dark between 20:00 and 04:00
	// server-local time, light otherwise.
	ThemeModeDayNight = "day_night"
)

// Night theme window: from NightStartHour (inclusive) through NightEndHour (exclusive)
// on the next day. 20:00 to 03:59:59 is dark; 04:00 is light.
const (
	NightStartHour = 20
	NightEndHour   = 4
)

// ParseThemeMode normalises a setting value. An empty value selects the default
// model mode; anything else must be one of the known modes.
func ParseThemeMode(raw string) (string, error) {
	switch mode := strings.TrimSpace(raw); mode {
	case "", ThemeModeModel:
		return ThemeModeModel, nil
	case ThemeModeDayNight:
		return ThemeModeDayNight, nil
	default:
		return "", fmt.Errorf("渲染深浅色模式必须是 %s 或 %s", ThemeModeModel, ThemeModeDayNight)
	}
}

// DayNightTheme returns the theme for the given server-local time.
func DayNightTheme(now time.Time) string {
	hour := now.Hour()
	if hour >= NightStartHour || hour < NightEndHour {
		return "dark"
	}
	return "light"
}

// EffectiveTheme decides the theme of one render. In day/night mode the model's
// requested theme is ignored entirely; in model mode it is used as given.
func EffectiveTheme(mode, requested string, now time.Time) string {
	if mode == ThemeModeDayNight {
		return DayNightTheme(now)
	}
	return requested
}
