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
	applier NetworkPolicyApplier

	syncMu  sync.Mutex // serializes SyncNetworkPolicy and guards applied
	applied map[string]appliedPolicy
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

// NetworkPolicyApplier pushes the network switch to the gateway described by cfg.
type NetworkPolicyApplier func(ctx context.Context, cfg Config, allow bool) error

type appliedPolicy struct {
	authToken string
	allow     bool
}

// SetNetworkPolicyApplier registers the function that pushes the network switch
// to a gateway. It must be idempotent.
func (m *ConfigManager) SetNetworkPolicyApplier(apply NetworkPolicyApplier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applier = apply
}

// SyncNetworkPolicy makes every gateway this manager has talked to match the
// switch. A gateway that was last told to allow networking and is no longer the
// configured endpoint is told to deny, so a combined endpoint-and-disable change
// still revokes the old workers. The state of a gateway is recorded only after a
// successful push, so a failure is returned and retried by the next call. Nothing
// happens until a gateway token is configured.
func (m *ConfigManager) SyncNetworkPolicy(ctx context.Context) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	m.mu.RLock()
	cfg, apply := m.current, m.applier
	m.mu.RUnlock()
	if apply == nil || cfg.AuthToken == "" || cfg.BaseURL == "" {
		return nil
	}
	if m.applied == nil {
		m.applied = map[string]appliedPolicy{}
	}

	var errs []error
	for baseURL, state := range m.applied {
		if baseURL == cfg.BaseURL || !state.allow {
			continue
		}
		old := cfg
		old.BaseURL, old.AuthToken = baseURL, state.authToken
		if err := apply(ctx, old, false); err != nil {
			errs = append(errs, fmt.Errorf("revoke on previous gateway %s: %w", baseURL, err))
			continue
		}
		m.applied[baseURL] = appliedPolicy{authToken: state.authToken}
	}

	if state, ok := m.applied[cfg.BaseURL]; !ok || state.allow != cfg.AllowNetwork || state.authToken != cfg.AuthToken {
		if err := apply(ctx, cfg, cfg.AllowNetwork); err != nil {
			errs = append(errs, err)
		} else {
			m.applied[cfg.BaseURL] = appliedPolicy{authToken: cfg.AuthToken, allow: cfg.AllowNetwork}
		}
	}
	return errors.Join(errs...)
}

// RefreshAllowNetwork updates AllowNetwork from an authoritative source while
// holding the manager lock.
func (m *ConfigManager) RefreshAllowNetwork(load func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current.AllowNetwork = load()
}

// SetAllowNetwork updates only the AllowNetwork flag.
func (m *ConfigManager) SetAllowNetwork(allow bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current.AllowNetwork = allow
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
// If the sandbox is currently disabled, it performs best-effort cleanup on the
// previously cached backend (if any) rather than failing immediately.
func (d *DynamicBackend) Release(ctx context.Context, sessionID string) error {
	b, err := d.resolve()
	if err != nil {
		if errors.Is(err, ErrSandboxDisabled) {
			d.mu.Lock()
			cached := d.backend
			d.mu.Unlock()
			if cached != nil {
				return cached.Release(ctx, sessionID)
			}
		}
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
