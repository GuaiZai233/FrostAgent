// Package wsauth authenticates real inbound adapter WebSocket handshakes.
package wsauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

type requiredKey struct{}

// RequireToken marks an ingress that must not inherit the dedicated adapter
// listener's legacy anonymous mode. Only server-side routing sets this policy.
func RequireToken(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requiredKey{}, true))
}

// Authorize accepts credentials only from one Authorization Bearer header.
// Callers must exclude Direct Chat, whose authentication belongs to management.
func Authorize(w http.ResponseWriter, r *http.Request, token string) bool {
	required, _ := r.Context().Value(requiredKey{}).(bool)
	if token == "" && !required {
		return true
	}
	values := r.Header.Values("Authorization")
	if token != "" && len(values) == 1 {
		scheme, credential, ok := strings.Cut(values[0], " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			// Fixed-size digests avoid length-dependent secret comparison.
			want, got := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(credential))
			if subtle.ConstantTimeCompare(want[:], got[:]) == 1 {
				return true
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}
