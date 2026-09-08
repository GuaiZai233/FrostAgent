package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrSandboxDisabled is returned when sandbox functionality is not enabled.
var ErrSandboxDisabled = errors.New("sandbox is disabled")

// BackendFactory creates a Backend from the given Config.
type BackendFactory func(Config) Backend

// ConfigManager manages atomic sandbox configuration snapshots.
// It guarantees that connection endpoint (BaseURL) and credentials (AuthToken)
// are applied together as atomic snapshots, eliminating mixed-configuration windows
// during gateway migrations or token rotations.
type ConfigManager struct {
	mu      sync.RWMutex
	current Config
}

// NewConfigManager creates a new ConfigManager with the initial configuration.
func NewConfigManager(initial Config) *ConfigManager {
	return &ConfigManager{current: initial}
}

// Get returns the current configuration snapshot.
func (m *ConfigManager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// ApplySnapshot atomically updates the full configuration snapshot.
// All fields (Enabled, BaseURL, AuthToken, SessionNamespace) are replaced at once.
func (m *ConfigManager) ApplySnapshot(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = cfg
}

// SetEnabled updates only the Enabled flag without altering endpoint or credential settings.
func (m *ConfigManager) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current.Enabled = enabled
}

// RefreshEnabled updates Enabled from an authoritative source while holding the
// manager lock, so concurrent Control Plane editors cannot publish stale values.
func (m *ConfigManager) RefreshEnabled(load func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current.Enabled = load()
}

// DynamicBackend implements Backend with runtime-configurable sandbox support.
// Configuration is re-read on each operation, allowing the sandbox to be
// enabled or disabled via environment variables without restarting the process.
type DynamicBackend struct {
	loadConfig func() Config
	factory    BackendFactory
	mu         sync.Mutex
	backend    Backend
	lastConfig Config
}

// NewDynamicBackend creates a DynamicBackend that lazily creates and caches
// the underlying Backend based on the current configuration.
func NewDynamicBackend(loadConfig func() Config, factory BackendFactory) *DynamicBackend {
	return &DynamicBackend{
		loadConfig: loadConfig,
		factory:    factory,
	}
}

func (d *DynamicBackend) resolve() (Backend, error) {
	cfg := d.loadConfig()
	if !cfg.Enabled {
		return nil, ErrSandboxDisabled
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("sandbox config invalid: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.backend != nil && d.lastConfig == cfg {
		return d.backend, nil
	}

	d.backend = d.factory(cfg)
	d.lastConfig = cfg
	return d.backend, nil
}

// Exec executes a command in the sandbox, resolving the backend dynamically.
func (d *DynamicBackend) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	b, err := d.resolve()
	if err != nil {
		return ExecResult{}, err
	}
	return b.Exec(ctx, req)
}

// Release releases a sandbox session, resolving the backend dynamically.
func (d *DynamicBackend) Release(ctx context.Context, sessionID string) error {
	b, err := d.resolve()
	if err != nil {
		return err
	}
	return b.Release(ctx, sessionID)
}

// Health checks the sandbox runtime health, resolving the backend dynamically.
func (d *DynamicBackend) Health(ctx context.Context) error {
	b, err := d.resolve()
	if err != nil {
		return err
	}
	return b.Health(ctx)
}
