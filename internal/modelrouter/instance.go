package modelrouter

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
)

type CredentialChange struct{ Target, Value string }

// PersistentRefs ensures one endpoint can never read or overwrite another endpoint's secret.
func PersistentRefs(endpoints []Endpoint) error {
	for _, e := range endpoints {
		if persistentSecretSource(e.APIKeySource) && e.APIKeyRef != defaultAPIKeyRef(e.APIKeySource, e.ID) {
			return fmt.Errorf("Endpoint %s 的凭据引用必须为 %s", e.ID, defaultAPIKeyRef(e.APIKeySource, e.ID))
		}
	}
	return nil
}
func CredentialExists(id string) (bool, error) {
	if runtime.GOOS != "windows" {
		return false, nil
	}
	_, ok, err := readWindowsCredential(windowsCredentialTarget(id))
	return ok, err
}

// CopyPublished snapshots only committed settings. External files remain references.
func (m *Manager) CopyPublished(reserve func([]Endpoint) error) ([]byte, []byte, []CredentialChange, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := cloneConfiguration(m.active)
	if m.loadErr != nil {
		return nil, nil, nil, m.loadErr
	}
	refs := map[string]string{}
	manual := manualSecretStore{Version: manualSecretStoreVersion, Secrets: map[string]string{}}
	changes := []CredentialChange{}
	for i := range cfg.Endpoints {
		old := cfg.Endpoints[i]
		e := &cfg.Endpoints[i]
		for attempt := 0; ; attempt++ {
			if attempt > 100 {
				return nil, nil, nil, fmt.Errorf("无法分配 Endpoint ID")
			}
			b := make([]byte, 12)
			if _, err := rand.Read(b); err != nil {
				return nil, nil, nil, err
			}
			e.ID = "endpoint_" + hex.EncodeToString(b)
			if persistentSecretSource(e.APIKeySource) {
				e.APIKeyRef = defaultAPIKeyRef(e.APIKeySource, e.ID)
			}
			if err := reserve([]Endpoint{*e}); err != nil {
				return nil, nil, nil, err
			}
			break
		}
		refs[old.ID] = e.ID
		if persistentSecretSource(old.APIKeySource) {
			value, err := m.secrets.Resolve(old)
			if err != nil {
				return nil, nil, nil, err
			}
			if old.APIKeyConfigured && value == "" {
				return nil, nil, nil, fmt.Errorf("Endpoint %s 的凭据不存在", old.ID)
			}
			e.APIKeyConfigured = value != ""
			if e.APIKeySource == APIKeyStorageManual {
				manual.Secrets[e.APIKeyRef] = value
			} else {
				changes = append(changes, CredentialChange{e.APIKeyRef, value})
			}
		}
	}
	for i := range cfg.Models {
		cfg.Models[i].EndpointID = refs[cfg.Models[i].EndpointID]
	}
	cfg.Revision = 1
	configJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, nil, err
	}
	secretJSON, err := json.MarshalIndent(manual, "", "  ")
	return configJSON, secretJSON, changes, err
}
func (m *Manager) DeleteCredentials() ([]CredentialChange, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := PersistentRefs(m.active.Endpoints); err != nil {
		return nil, err
	}
	changes := []CredentialChange{}
	for _, e := range m.active.Endpoints {
		if e.APIKeySource == APIKeyStorageWindowsCredentialManager {
			changes = append(changes, CredentialChange{e.APIKeyRef, ""})
		}
	}
	return changes, nil
}

// ApplyCredentials returns a rollback even on a partial write failure.
func ApplyCredentials(changes []CredentialChange) (func() error, error) {
	previous := []CredentialChange{}
	rollback := func() error {
		var errs error
		for i := len(previous) - 1; i >= 0; i-- {
			if err := writeWindowsCredential(previous[i].Target, previous[i].Value); err != nil {
				errs = fmt.Errorf("%v; %w", errs, err)
			}
		}
		return errs
	}
	for _, change := range changes {
		value, _, err := readWindowsCredential(change.Target)
		if err != nil {
			return rollback, err
		}
		previous = append(previous, CredentialChange{change.Target, value})
		if err := writeWindowsCredential(change.Target, change.Value); err != nil {
			return rollback, err
		}
	}
	return rollback, nil
}

func CredentialTarget(id string) string { return windowsCredentialTarget(id) }
