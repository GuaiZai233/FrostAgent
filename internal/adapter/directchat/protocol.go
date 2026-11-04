// Package directchat defines the shared mock WebSocket request markers.
// Authentication is enforced by the management listener before adapter dispatch.
package directchat

import "net/http"

const Subprotocol = "frostagent.direct-chat"
const TokenProtocolPrefix = "frostagent.control-token."

func IsRequest(r *http.Request) bool {
	return r.URL.Query().Get("mock") == "true" || r.Header.Get("X-Mock-Adapter") == "true"
}
