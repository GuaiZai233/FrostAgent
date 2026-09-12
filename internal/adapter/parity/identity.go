package parity

import (
	"FrostAgent/internal/memory"
	"fmt"
	"strings"
)

// CanonicalBillingPlatform maps external adapter platforms (e.g. aiocqhttp, onebot, qq)
// to the unified billing platform identity ("qq" for QQ ecosystem).
func CanonicalBillingPlatform(platform string) string {
	return memory.CanonicalPlatform(platform)
}

// BillingTaskID produces a consistent billing task ID across adapters.
func BillingTaskID(platform, userID, messageID string) string {
	return fmt.Sprintf("%s_%s_%s", strings.TrimSpace(platform), strings.TrimSpace(userID), strings.TrimSpace(messageID))
}
