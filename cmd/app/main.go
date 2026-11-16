package main

import (
	"context"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/directchat"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/wsauth"
	"github.com/GuaiZai233/FrostAgent/internal/buildinfo"
	"github.com/GuaiZai233/FrostAgent/internal/frontend"
	"github.com/GuaiZai233/FrostAgent/internal/instance"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/modelcatalog"
	mcpsvc "github.com/GuaiZai233/FrostAgent/internal/service/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/updatecheck"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func run() error {
	catalogCtx, stopCatalog := context.WithCancel(context.Background())
	defer stopCatalog()
	modelcatalog.Start(catalogCtx, modelcatalog.New(
		modelcatalog.DefaultURL,
		filepath.Join("data", "models_dev_cache.json"),
		modelcatalog.RefreshInterval,
	))
	gateway, err := newStorageGateway(context.Background(), "data")
	if err != nil {
		return err
	}
	defer gateway.Close()
	manager, _, generation := gateway.snapshot()
	global := manager.GlobalConfig()
	gateway.prepare = func(next *instance.Manager) error {
		return initializeDockerControlToken(next.GlobalConfig(), os.Getenv)
	}
	if err := gateway.prepare(manager); err != nil {
		return err
	}
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
		if instanceconfig.ContainerManagement == "true" || instanceconfig.ManagementLoopbackOnly == "true" {
			if err := instanceconfig.ValidateContainerListeners(listen, wsListen); err != nil {
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
	return managementMuxWithUpdateSource(manager, updatecheck.Source{
		Current: updatecheck.Build{Display: buildinfo.DisplayVersion(), Version: buildinfo.Version, Commit: buildinfo.SourceCommit()},
	})
}

func managementMuxWithUpdateSource(manager http.Handler, source updatecheck.Source) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/storage", manager)
	mux.Handle("/api/instances", manager)
	mux.Handle("/api/update-check", managementReadHandler(manager, updatecheck.Handler(source)))
	mux.Handle("/api/version", managementReadHandler(manager, updatecheck.VersionHandler(source)))
	if gateway, ok := manager.(interface{ SecurityHandler() http.Handler }); ok {
		mux.Handle("/api/security/", gateway.SecurityHandler())
	} else if registry, ok := manager.(*instance.Manager); ok {
		mux.Handle("/api/security/", securityHandlerFor(registry))
	}
	mux.Handle("/api/instances/", manager)
	mux.Handle("/instances/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAdapterWebSocketPath(r.URL.Path) && !directchat.IsRequest(r) && instanceconfig.ContainerManagement == "true" && instanceconfig.ManagementLoopbackOnly != "true" {
			// Bridge images expose management independently of WS_LISTEN_ADDR.
			// Apply this policy at the listener boundary, not inside the shared
			// gateway, which also dispatches the dedicated adapter listener.
			r = wsauth.RequireToken(r)
		}
		manager.ServeHTTP(w, r)
	}))
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
		mock := isAdapterWebSocketPath(path) && directchat.IsRequest(r)
		management := strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/instances/") ||
			strings.HasPrefix(path, "/frostagent.v1.")
		if !management || path == "/api/v1/messages/send" || strings.HasPrefix(path, "/frostagent.v1.MCPService/") ||
			(isAdapterOrMessagePath(path) && !mock) {
			next.ServeHTTP(w, r)
			return
		}
		peer := r.RemoteAddr
		// Only an actual loopback connector retains the deployment's Access policy.
		// Docker bridge and host-published requests always require a control token.
		container := instanceconfig.ContainerManagement == "true" || instanceconfig.ManagementLoopbackOnly == "true"
		trustedLocalConnector := container && isLoopbackHost(peer)
		if !trustedLocalConnector && (!isLoopbackHost(r.Host) || hasProxyHeaders(r)) {
			peer = "remote-management"
		}
		w.Header().Set("Cache-Control", "no-store")
		header := r.Header
		if mock {
			var err error
			header, err = directChatAuthHeaders(r)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if err := mcpsvc.CheckSensitiveControlPlaneAuthScoped(peer, header, getenv); err != nil {
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
	return parts[1] == "ws/onebot" || parts[1] == "api/v1/messages/send"
}

func isAdapterWebSocketPath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/instances/"), "/")
	return strings.HasPrefix(path, "/instances/") && len(parts) == 3 && parts[1] == "ws" &&
		parts[2] == "onebot"
}

// Keep the adapter listener separate from the HTTP management surface.
func instanceWebSocketHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdapterWebSocketPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// Mock connections belong to Direct Chat on the management listener, not
		// the adapter ingress, even when they omit Origin.
		if directchat.IsRequest(r) {
			http.Error(w, "mock connections require the management listener", http.StatusForbidden)
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
