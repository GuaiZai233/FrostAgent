package modelrouter

import "errors"

// EndpointAPIKey resolves only a published provider's credential for backend use.
// The catalog and secret are read under the publication lock so drafts and
// concurrent credential changes cannot supply a mismatched reference.
func (m *Manager) EndpointAPIKey(endpointID string) (string, error) {
	unavailable := errors.New("Provider 的 API Key 不可用")
	if m == nil {
		return "", unavailable
	}
	if m.db != nil {
		m.db.ModelRouterMu.RLock()
		defer m.db.ModelRouterMu.RUnlock()
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := m.loadSQL(); err != nil {
			return "", unavailable
		}
	} else {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if m.loadErr != nil {
			return "", unavailable
		}
	}
	for _, endpoint := range m.active.Endpoints {
		if endpoint.ID == endpointID && endpoint.Enabled {
			key, err := resolveEndpointAPIKey(m.secrets, endpoint)
			if err != nil || key == "" {
				return "", unavailable
			}
			return key, nil
		}
	}
	return "", unavailable
}
