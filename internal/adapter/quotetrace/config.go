package quotetrace

import (
	"strconv"
	"strings"
)

const DefaultDepth = 5
const MaxDepth = 10

// Depth returns the number of quoted messages to resolve, including the direct quote.
func Depth(getenv func(string) string) int {
	if getenv == nil {
		return DefaultDepth
	}
	if strings.EqualFold(strings.TrimSpace(getenv("ENABLE_QUOTE_TRACE")), "false") {
		return 0
	}
	value := strings.TrimSpace(getenv("QUOTE_TRACE_DEPTH"))
	if value == "" {
		return DefaultDepth
	}
	depth, err := strconv.Atoi(value)
	if err != nil || depth < 1 || depth > MaxDepth {
		return DefaultDepth
	}
	return depth
}
