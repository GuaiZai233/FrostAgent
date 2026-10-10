package main

import (
	"FrostAgent/internal/instance"
	secsvc "FrostAgent/internal/service/security"
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// storageGateway keeps the management routes stable while the selected SQL
// database and its instance runtimes are replaced.
type storageGateway struct {
	mu         sync.RWMutex
	switchMu   sync.Mutex
	root       string
	bootstrap  *storage.Bootstrap
	current    *instance.Manager
	security   http.Handler
	config     storage.Config
	generation uint64
	apply      func(*instance.Manager) error
}

func newStorageGateway(ctx context.Context, root string) (*storageGateway, error) {
	bootstrap, err := storage.OpenBootstrap(ctx, root)
	if err != nil {
		return nil, err
	}
	config, err := bootstrap.Load(ctx)
	if err != nil {
		bootstrap.Close()
		return nil, err
	}
	manager, err := openSelectedManager(ctx, root, config)
	if err != nil {
		bootstrap.Close()
		return nil, err
	}
	return &storageGateway{root: root, bootstrap: bootstrap, current: manager,
		security: securityHandlerFor(manager), config: config}, nil
}

func securityHandlerFor(manager *instance.Manager) http.Handler {
	return secsvc.NewWithStore(manager.SecurityController(), manager.ControlPlaneGetenv(), manager.GlobalConfig())
}

func openSelectedManager(ctx context.Context, root string, config storage.Config) (*instance.Manager, error) {
	db, err := storage.OpenWithConfig(ctx, root, config)
	if err != nil {
		return nil, err
	}
	return instance.NewDatabaseWithDB(root, db)
}

func (g *storageGateway) snapshot() (*instance.Manager, storage.Config, uint64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.current, g.config, g.generation
}

func (g *storageGateway) Close() {
	g.switchMu.Lock()
	defer g.switchMu.Unlock()
	g.mu.Lock()
	manager := g.current
	g.current = nil
	g.security = nil
	g.mu.Unlock()
	if manager != nil {
		manager.Close()
	}
	_ = g.bootstrap.Close()
}

func (g *storageGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/storage" {
		g.serveStorage(w, r)
		return
	}
	if r.URL.Path == "/api/instances/global/import/settings" ||
		(strings.Contains(r.URL.Path, "/frostagent.v1.SettingsService/") && r.Method == http.MethodPost) {
		g.switchMu.Lock()
		defer g.switchMu.Unlock()
	}
	manager, _, _ := g.snapshot()
	if manager == nil {
		http.Error(w, "database is switching", http.StatusServiceUnavailable)
		return
	}
	manager.ServeHTTP(w, r)
}

func (g *storageGateway) SecurityHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		handler := g.security
		g.mu.RUnlock()
		if handler == nil {
			http.Error(w, "database is switching", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func (g *storageGateway) serveStorage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_, config, _ := g.snapshot()
		_ = json.NewEncoder(w).Encode(config)
	case http.MethodPost:
		var config storage.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&config); err != nil {
			writeStorageError(w, http.StatusBadRequest, err)
			return
		}
		if err := g.switchTo(r.Context(), config); err != nil {
			writeStorageError(w, http.StatusServiceUnavailable, err)
			return
		}
		manager, _, _ := g.snapshot()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "listen_addr": manager.GlobalConfig().Get("LISTEN_ADDR"),
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeStorageError(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (g *storageGateway) switchTo(ctx context.Context, requested storage.Config) error {
	config, err := storage.NormalizeConfig(requested)
	if err != nil {
		return err
	}
	g.switchMu.Lock()
	defer g.switchMu.Unlock()
	old, previous, _ := g.snapshot()
	if old == nil {
		return errors.New("database is unavailable")
	}
	if config == previous {
		return nil
	}
	// Drain the old manager before opening a new PostgreSQL connection; the
	// single-process lease also covers a DSN edit targeting the same database.
	g.mu.Lock()
	g.current = nil
	g.security = nil
	g.mu.Unlock()
	old.Close()
	manager, err := openSelectedManager(ctx, g.root, config)
	if err == nil && g.apply != nil {
		manager.SetGlobalApplyHook(func() error { return g.apply(manager) })
	}
	if err == nil && g.apply != nil {
		err = g.apply(manager)
	}
	if err == nil {
		err = g.bootstrap.Save(ctx, config)
	}
	if err != nil {
		if manager != nil {
			manager.Close()
		}
		recovered, recoveryErr := openSelectedManager(context.Background(), g.root, previous)
		if recoveryErr == nil && g.apply != nil {
			recovered.SetGlobalApplyHook(func() error { return g.apply(recovered) })
		}
		if recoveryErr == nil && g.apply != nil {
			recoveryErr = g.apply(recovered)
		}
		if recoveryErr == nil {
			g.mu.Lock()
			g.current = recovered
			g.security = securityHandlerFor(recovered)
			g.mu.Unlock()
		} else if recovered != nil {
			recovered.Close()
		}
		return errors.Join(fmt.Errorf("switch database: %w", err), recoveryErr)
	}
	g.mu.Lock()
	g.current = manager
	g.security = securityHandlerFor(manager)
	g.config = config
	g.generation++
	g.mu.Unlock()
	return nil
}
