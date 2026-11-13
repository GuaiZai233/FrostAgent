package timesleep

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	ToolName       = "time_sleep"
	TimeoutKey     = "TIME_SLEEP_TIMEOUT_SECONDS"
	DefaultTimeout = time.Minute
)

var ErrTimeout = errors.New("Timeout")

// Duration validates seconds before converting to nanoseconds.
func Duration(seconds float64) (time.Duration, error) {
	nanos := seconds * float64(time.Second)
	if math.IsNaN(nanos) || math.IsInf(nanos, 0) || nanos < 1 || nanos >= float64(math.MaxInt64) {
		return 0, errors.New("秒数必须大于 0，且在可表示的时间范围内")
	}
	return time.Duration(nanos), nil
}

func Timeout(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultTimeout, nil
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, errors.New("挂起超时必须是有效的正数秒数")
	}
	return Duration(seconds)
}
