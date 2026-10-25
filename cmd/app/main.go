package main

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/frontend"
	"github.com/GuaiZai233/FrostAgent/internal/instance"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	mcpsvc "github.com/GuaiZai233/FrostAgent/internal/service/mcp"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func run() error {
	gateway, err := newStorageGateway(context.Background(), "data")
	if err != nil {
		return err
	}
	defer gateway.Close()
	manager, _, generation := gateway.snapshot()
	global := manager.GlobalConfig()
	mux := managementMux(gateway)
	wsMux := http.NewServeMux()
	wsMux.Handle("/instances/", instanceWebSocketHandler(gateway))
	listeners := newListenerSet()
	applyListeners := func(global *instanceconfig.Store) error {
		listen := global.Get("LISTEN_ADDR")
		if listen == "" {
			listen = instanceconfig.DefaultListenAddr
		}
		wsListen := global.Get("WS_LISTEN_ADDR")
		if wsListen == "" {
			wsListen = instanceconfig.DefaultWSListenAddr
		}
		if instanceconfig.ManagementLoopbackOnly == "true" {
			if err := instanceconfig.ValidateListenerIsolation(listen, wsListen); err != nil {
				return err
			}
		}
		handlers := map[string]http.Handler{listen: corsMiddleware(mux, global.Get)}
		if wsListen != listen {
			handlers[wsListen] = wsMux
		}
		return listeners.Apply(handlers)
	}
	gateway.apply = func(next *instance.Manager) error {
		if err := applyListeners(next.GlobalConfig()); err != nil {
			return err
		}
		return next.ApplyGlobalSettings()
	}
	manager.SetGlobalApplyHook(func() error { return gateway.apply(manager) })
	if err := applyListeners(global); err != nil {
		return err
	}
	lastApplied := global.Snapshot()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	select {
	case <-ctx.Done():
	case err = <-listeners.errors:
	case <-ticker.C:
	}
	for ctx.Err() == nil && err == nil {
		gateway.switchMu.Lock()
		manager, _, currentGeneration := gateway.snapshot()
		if manager != nil {
			global = manager.GlobalConfig()
			if currentGeneration != generation {
				generation = currentGeneration
				lastApplied = global.Snapshot()
			}
			current := global.Snapshot()
			if !maps.Equal(lastApplied, current) {
				applyErr := applyListeners(global)
				if applyErr == nil {
					applyErr = manager.ApplyGlobalSettings()
					if applyErr == nil {
						lastApplied = current
					}
				}
				if applyErr != nil {
					logs.General.Warn(logs.SYSTEM, fmt.Sprintf("应用全局设置失败，恢复旧值: %v", applyErr))
					if rollbackErr := restoreGlobalSettings(global, lastApplied); rollbackErr != nil {
						err = rollbackErr
					}
					if err == nil {
						err = applyListeners(global)
					}
					if err == nil {
						err = manager.ApplyGlobalSettings()
					}
				}
			}
		}
		gateway.switchMu.Unlock()
		if err != nil {
			break
		}
		select {
		case <-ctx.Done():
		case err = <-listeners.errors:
		case <-ticker.C:
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listeners.Close(shutdown)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func restoreGlobalSettings(global *instanceconfig.Store, previous map[string]string) error {
	current := global.Snapshot()
	for key := range current {
		if _, exists := previous[key]; !exists {
			if err := global.Update(key, "", true); err != nil {
				return err
			}
		}
	}
	for key, value := range previous {
		if current[key] != value {
			if err := global.Update(key, value, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func managementMux(manager http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/storage", manager)
	mux.Handle("/api/instances", manager)
	if gateway, ok := manager.(interface{ SecurityHandler() http.Handler }); ok {
		mux.Handle("/api/security/", gateway.SecurityHandler())
	} else if registry, ok := manager.(*instance.Manager); ok {
		mux.Handle("/api/security/", securityHandlerFor(registry))
	}
	mux.Handle("/api/instances/", manager)
	mux.Handle("/instances/", manager)
	mux.Handle("/api/actionscat/", manager)
	mux.Handle("/api/v1/actionscat/", manager)
	mux.Handle("/api/v1/messages/send", manager)
	mux.Handle("/frostagent.v1.LogService/", manager)
	mux.Handle("/frostagent.v1.ModelRouterService/", manager)
	mux.Handle("/frostagent.v1.MCPService/", http.NotFoundHandler())
	mux.Handle("/api/log-images/", manager)
	mux.Handle("/", frontend.Handler())
	if provider, ok := manager.(interface{ ControlPlaneGetenv() func(string) string }); ok {
		return managementAuthMiddleware(mux, provider.ControlPlaneGetenv())
	}
	return mux
}

func managementAuthMiddleware(next http.Handler, getenv func(string) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		management := strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/instances/") ||
			strings.HasPrefix(path, "/frostagent.v1.")
		if !management || path == "/api/v1/messages/send" || strings.HasPrefix(path, "/frostagent.v1.MCPService/") ||
			isAdapterOrMessagePath(path) {
			next.ServeHTTP(w, r)
			return
		}
		peer := r.RemoteAddr
		// Container builds isolate this listener on loopback shared with the connector.
		// Authentication belongs to the deployment's Cloudflare Access policy.
		trustedLocalConnector := instanceconfig.ManagementLoopbackOnly == "true" && isLoopbackHost(peer)
		if !trustedLocalConnector && (!isLoopbackHost(r.Host) || hasProxyHeaders(r)) {
			peer = "remote-management"
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := mcpsvc.CheckSensitiveControlPlaneAuthScoped(peer, r.Header, getenv); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasProxyHeaders(r *http.Request) bool {
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		if strings.TrimSpace(r.Header.Get(name)) != "" {
			return true
		}
	}
	return false
}

func isAdapterOrMessagePath(path string) bool {
	parts := strings.SplitN(strings.TrimPrefix(path, "/instances/"), "/", 2)
	if len(parts) != 2 || !strings.HasPrefix(path, "/instances/") {
		return false
	}
	return parts[1] == "ws/onebot" || parts[1] == "ws/astrbot" || parts[1] == "api/v1/messages/send"
}

// Keep the adapter listener separate from the HTTP management surface.
func instanceWebSocketHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/instances/"), "/")
		if len(parts) != 3 || parts[1] != "ws" || (parts[2] != "onebot" && parts[2] != "astrbot") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
