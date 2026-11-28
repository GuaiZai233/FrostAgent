// Package managementipc implements the OS-authenticated local management transport.
package managementipc

import (
	"context"
	"github.com/GuaiZai233/FrostAgent/internal/managementauth"
	"net"
	"net/http"
	"os"
)

func Endpoint() string {
	if path := os.Getenv("FROSTAGENT_IPC_PATH"); path != "" {
		return path
	}
	return defaultEndpoint
}
func Server(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler,
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return managementauth.WithLocalAdmin(ctx)
		},
	}
}
