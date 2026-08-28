package modelrouter

import "strings"

const windowsCredentialTargetPrefix = "guaitech.frostagent/endpoint/"

func windowsCredentialTarget(endpointID string) string {
	return windowsCredentialTargetPrefix + strings.TrimSpace(endpointID)
}
