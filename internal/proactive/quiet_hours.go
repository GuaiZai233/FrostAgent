package proactive

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	AwarenessQuietHoursEnabled = "ENABLE_PROACTIVE_AWARENESS_QUIET_HOURS"
	AwarenessQuietHoursStart   = "PROACTIVE_AWARENESS_QUIET_HOURS_START"
	AwarenessQuietHoursEnd     = "PROACTIVE_AWARENESS_QUIET_HOURS_END"
	DefaultAwarenessQuietStart = "23:00"
	DefaultAwarenessQuietEnd   = "07:00"
)

var awarenessQuietTime = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func ParseAwarenessQuietTime(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if !awarenessQuietTime.MatchString(raw) {
		return 0, fmt.Errorf("免打扰时间必须为 HH:mm 格式，例如 23:00 或 07:00")
	}
	return int(raw[0]-'0')*600 + int(raw[1]-'0')*60 + int(raw[3]-'0')*10 + int(raw[4]-'0'), nil
}

// AwarenessQuietHoursActive uses the supplied time's location. Missing settings
// retain the default window for existing instances as well as new ones.
func AwarenessQuietHoursActive(getenv func(string) string, now time.Time) bool {
	if getenv == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(getenv(AwarenessQuietHoursEnabled)), "false") {
		return false
	}
	start, end := strings.TrimSpace(getenv(AwarenessQuietHoursStart)), strings.TrimSpace(getenv(AwarenessQuietHoursEnd))
	if start == "" {
		start = DefaultAwarenessQuietStart
	}
	if end == "" {
		end = DefaultAwarenessQuietEnd
	}
	from, startErr := ParseAwarenessQuietTime(start)
	until, endErr := ParseAwarenessQuietTime(end)
	if startErr != nil || endErr != nil {
		// Invalid legacy/imported configuration must not accidentally send.
		return true
	}
	minute := now.Hour()*60 + now.Minute()
	if from < until {
		return minute >= from && minute < until
	}
	// Equal endpoints mean all day; an overnight window wraps at midnight.
	return minute >= from || minute < until
}
