package modelrouter

import "errors"

// EndpointCredential is a published provider's API key together with its base URL. Both come from the
// same published endpoint under one lock, so a key can never be paired with a base URL from another version.
type EndpointCredential struct {
	APIKey  string
	BaseURL string
}

// EndpointCredential resolves only a published provider's credential for backend use.
// The catalog and secret are read under the publication lock so drafts and
// concurrent credential changes cannot supply a mismatched reference.
func (m *Manager) EndpointCredential(endpointID string) (EndpointCredential, error) {
	unavailable := errors.New("Provider 的 API Key 不可用")
	if m == nil {
		return EndpointCredential{}, unavailable
	}
	if m.db != nil {
		m.db.ModelRouterMu.RLock()
		defer m.db.ModelRouterMu.RUnlock()
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := m.loadSQL(); err != nil {
			return EndpointCredential{}, unavailable
		}
	} else {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.loadErr != nil {
			return EndpointCredential{}, unavailable
		}
	}
	for _, endpoint := range m.active.Endpoints {
		if endpoint.ID == endpointID && endpoint.Enabled {
			key, err := resolveEndpointAPIKey(m.secrets, endpoint)
			if err != nil || key == "" {
				return EndpointCredential{}, unavailable
			}
			return EndpointCredential{APIKey: key, BaseURL: endpoint.BaseURL}, nil
		}
	}
	return EndpointCredential{}, unavailable
}

// EndpointAPIKey resolves only the key of a published provider.
func (m *Manager) EndpointAPIKey(endpointID string) (string, error) {
	credential, err := m.EndpointCredential(endpointID)
	if err != nil {
		return "", err
	}
	return credential.APIKey, nil
}
