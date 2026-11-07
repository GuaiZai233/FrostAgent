package main

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/directchat"
	"github.com/gorilla/websocket"
)

// Browsers cannot set Authorization on a WebSocket handshake. Decode their
// credential-bearing protocol offer for the existing control-plane auth check.
// Do not mutate the request: the storage gateway rechecks the current database's
// credentials, and the adapters only negotiate the credential-free Subprotocol.
func directChatAuthHeaders(r *http.Request) (http.Header, error) {
	var token string
	found := false
	for _, protocol := range websocket.Subprotocols(r) {
		if !strings.HasPrefix(protocol, directchat.TokenProtocolPrefix) {
			continue
		}
		if found {
			return nil, errors.New("multiple control credentials")
		}
		found = true
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(protocol, directchat.TokenProtocolPrefix))
		if err != nil || len(decoded) == 0 || strings.ContainsAny(string(decoded), "\r\n") {
			return nil, errors.New("invalid control credential")
		}
		token = string(decoded)
	}
	if !found || strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return r.Header, nil
	}
	header := r.Header.Clone()
	header.Set("Authorization", "Bearer "+token)
	return header, nil
}
