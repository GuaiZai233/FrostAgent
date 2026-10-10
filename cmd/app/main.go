package main

import (
	"FrostAgent/internal/frontend"
	"FrostAgent/internal/instance"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	secsvc "FrostAgent/internal/service/security"
	"context"
	"fmt"
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
	desiredHandlers := func(global *instanceconfig.Store) map[string]http.Handler {
		listen := global.Get("LISTEN_ADDR")
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		wsListen := global.Get("WS_LISTEN_ADDR")
		if wsListen == "" {
			wsListen = "127.0.0.1:1234"
		}
		handlers := map[string]http.Handler{listen: corsMiddleware(mux, global.Get)}
		if wsListen != listen {
			handlers[wsListen] = wsMux
		}
		return handlers
	}
	gateway.apply = func(next *instance.Manager) error {
		if err := listeners.Apply(desiredHandlers(next.GlobalConfig())); err != nil {
			return err
		}
		return next.ApplyGlobalSettings()
	}
	manager.SetGlobalApplyHook(func() error { return gateway.apply(manager) })
	if err := listeners.Apply(desiredHandlers(global)); err != nil {
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
				applyErr := listeners.Apply(desiredHandlers(global))
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
						err = listeners.Apply(desiredHandlers(global))
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

func managementMux(manager http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/api/storage", manager)
	mux.Handle("/api/instances", manager)
	if gateway, ok := manager.(interface{ SecurityHandler() http.Handler }); ok {
		mux.Handle("/api/security/", gateway.SecurityHandler())
	} else if registry, ok := manager.(*instance.Manager); ok {
		mux.Handle("/api/security/", secsvc.NewWithStore(registry.SecurityController(), registry.ControlPlaneGetenv(), registry.GlobalConfig()))
	}
	mux.Handle("/api/instances/", manager)
	mux.Handle("/instances/", manager)
	mux.Handle("/api/actionscat/", manager)
	mux.Handle("/api/v1/actionscat/", manager)
	mux.Handle("/api/v1/messages/send", manager)
	mux.Handle("/frostagent.v1.LogService/", manager)
	mux.Handle("/frostagent.v1.MCPService/", http.NotFoundHandler())
	mux.Handle("/api/log-images/", manager)
	mux.Handle("/", frontend.Handler())
	return mux
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
