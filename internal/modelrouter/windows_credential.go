package modelrouter

import (
	"fmt"
	"sort"
)

const windowsCredentialTargetPrefix = "guaitech.frostagent/endpoint/"

func windowsCredentialTarget(endpointID string) string {
	return windowsCredentialTargetPrefix + endpointID
}

func loadWindowsCredentials(cfg *Configuration) error {
	for i := range cfg.Endpoints {
		endpoint := &cfg.Endpoints[i]
		if endpoint.APIKeyStorage != APIKeyStorageWindowsCredentialManager {
			continue
		}
		apiKey, exists, err := readWindowsCredential(endpoint.ID)
		if err != nil {
			return fmt.Errorf("读取 Endpoint %q 的 Windows 凭据失败: %w", endpoint.DisplayName, err)
		}
		if exists {
			endpoint.APIKey = apiKey
		}
	}
	return nil
}

func persistWindowsCredentials(previous, next Configuration) (func() error, error) {
	ids := make(map[string]struct{})
	for _, endpoint := range previous.Endpoints {
		if endpoint.APIKeyStorage == APIKeyStorageWindowsCredentialManager {
			ids[endpoint.ID] = struct{}{}
		}
	}
	for _, endpoint := range next.Endpoints {
		if endpoint.APIKeyStorage == APIKeyStorageWindowsCredentialManager {
			ids[endpoint.ID] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)

	type previousCredential struct {
		value  string
		exists bool
	}
	before := make(map[string]previousCredential, len(ordered))
	applied := make([]string, 0, len(ordered))
	restore := func() error {
		for i := len(applied) - 1; i >= 0; i-- {
			id := applied[i]
			credential := before[id]
			value := ""
			if credential.exists {
				value = credential.value
			}
			if err := writeWindowsCredential(id, value); err != nil {
				return err
			}
		}
		return nil
	}

	for _, id := range ordered {
		value, exists, err := readWindowsCredential(id)
		if err != nil {
			_ = restore()
			return nil, fmt.Errorf("读取 Windows 凭据 %q 失败: %w", windowsCredentialTarget(id), err)
		}
		before[id] = previousCredential{value: value, exists: exists}
		desired := ""
		for _, endpoint := range next.Endpoints {
			if endpoint.ID == id && endpoint.APIKeyStorage == APIKeyStorageWindowsCredentialManager {
				desired = endpoint.APIKey
				break
			}
		}
		if (exists && value == desired) || (!exists && desired == "") {
			continue
		}
		if err := writeWindowsCredential(id, desired); err != nil {
			_ = restore()
			return nil, fmt.Errorf("写入 Windows 凭据 %q 失败: %w", windowsCredentialTarget(id), err)
		}
		applied = append(applied, id)
	}
	return restore, nil
}
