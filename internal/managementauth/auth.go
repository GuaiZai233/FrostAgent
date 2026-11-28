// Package managementauth carries authenticated management identities between
// listeners and business handlers. Network headers never grant a local identity.
package managementauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

type principalKey struct{}
type Principal struct{ Kind string }

// WithLocalAdmin is only used by the OS-authenticated IPC listener.
func WithLocalAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, principalKey{}, Principal{Kind: "local-admin"})
}
func Authenticated(ctx context.Context) bool {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return ok && (p.Kind == "local-admin" || p.Kind == "token")
}
func LocalAdmin(ctx context.Context) bool {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return ok && p.Kind == "local-admin"
}
func CheckToken(header http.Header, getenv func(string) string) error {
	token := ""
	if getenv != nil {
		token = strings.TrimSpace(getenv("MCP_CONTROL_TOKEN"))
		if token == "" {
			token = strings.TrimSpace(getenv("ADMIN_TOKEN"))
		}
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		return errors.New("management token required or invalid")
	}
	return nil
}

// Authenticate rechecks HTTP credentials against the current backend. IPC
// identities remain valid across token rotation and storage selection changes.
func Authenticate(ctx context.Context, header http.Header, getenv func(string) string) (context.Context, error) {
	if LocalAdmin(ctx) {
		return ctx, nil
	}
	if err := CheckToken(header, getenv); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, principalKey{}, Principal{Kind: "token"}), nil
}

// Authorize accepts an identity established by a listener or checks a direct
// service call. It never infers privilege from a peer address.
func Authorize(ctx context.Context, header http.Header, getenv func(string) string) error {
	if Authenticated(ctx) {
		return nil
	}
	return CheckToken(header, getenv)
}
