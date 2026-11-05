package main

import (
	"net/http"

	"github.com/GuaiZai233/FrostAgent/internal/instance"
)

// Management reads outside Manager still authenticate against the currently
// selected database, including after a switch or control-token rotation.
func managementReadHandler(manager http.Handler, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var registry *instance.Manager
		switch manager := manager.(type) {
		case *storageGateway:
			registry, _, _ = manager.snapshot()
		case *instance.Manager:
			registry = manager
		}
		if registry == nil {
			http.Error(w, "management configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		managementAuthMiddleware(next, registry.ControlPlaneGetenv()).ServeHTTP(w, r)
	})
}
