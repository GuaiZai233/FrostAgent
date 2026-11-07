package parity

import "time"

// CurrentTimeGuidance keeps request-local time next to the reply guidance instead
// of the stable system instructions. Use the same timezone on every host.
func CurrentTimeGuidance(now time.Time) string {
	return "当前系统时间：" +
		now.In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-1-2 15:04") + "(UTC+8)。"
}
