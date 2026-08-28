package modelrouter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const manualSecretStoreVersion = 1

type secretMutation struct {
	Source APIKeyStorage
	Ref    string
	Value  string
	Clear  bool
}

// SecretBackend owns API key material. Configuration and its DTOs only retain
// the source, reference and configured status.
type SecretBackend struct {
	mu         sync.RWMutex
	manualPath string
	manual     map[string]string
	draft      map[string]secretMutation
}

type manualSecretStore struct {
	Version int               `json:"version"`
	Secrets map[string]string `json:"secrets"`
}

func newSecretBackend(configurationPath string) (*SecretBackend, error) {
	extension := filepath.Ext(configurationPath)
	base := strings.TrimSuffix(filepath.Base(configurationPath), extension)
	backend := &SecretBackend{
		manualPath: filepath.Join(filepath.Dir(configurationPath), base+"_secrets.json"),
		manual:     make(map[string]string),
		draft:      make(map[string]secretMutation),
	}
	data, err := os.ReadFile(backend.manualPath)
	if os.IsNotExist(err) {
		return backend, nil
	}
	if err != nil {
		return backend, fmt.Errorf("读取模型路由 Secret 存储失败: %w", err)
	}
	var stored manualSecretStore
	if err := json.Unmarshal(data, &stored); err != nil {
		return backend, fmt.Errorf("解析模型路由 Secret 存储失败: %w", err)
	}
	if stored.Version != manualSecretStoreVersion {
		return backend, fmt.Errorf("不支持的模型路由 Secret 存储版本 %d", stored.Version)
	}
	for ref, value := range stored.Secrets {
		if ref = strings.TrimSpace(ref); ref != "" && value != "" {
			backend.manual[ref] = value
		}
	}
	return backend, nil
}

func manualAPIKeyRef(endpointID string) string {
	return "endpoint/" + strings.TrimSpace(endpointID)
}

func defaultAPIKeyRef(source APIKeyStorage, endpointID string) string {
	switch source {
	case APIKeyStorageEnv:
		return APIKeyEnvironment
	case APIKeyStorageWindowsCredentialManager:
		return windowsCredentialTarget(endpointID)
	case APIKeyStorageManual, "":
		return manualAPIKeyRef(endpointID)
	default:
		return ""
	}
}

// resolveEndpointAPIKey is the single boundary used immediately before an
// upstream request needs Authorization material.
func resolveEndpointAPIKey(backend *SecretBackend, endpoint Endpoint) (string, error) {
	if backend == nil {
		return "", fmt.Errorf("Secret Backend 不可用")
	}
	return backend.Resolve(endpoint)
}

func (b *SecretBackend) Resolve(endpoint Endpoint) (string, error) {
	ref := strings.TrimSpace(endpoint.APIKeyRef)
	switch endpoint.APIKeySource {
	case "", APIKeyStorageManual:
		b.mu.RLock()
		apiKey := b.manual[ref]
		b.mu.RUnlock()
		return apiKey, nil
	case APIKeyStorageEnv:
		apiKey := strings.TrimSpace(os.Getenv(ref))
		if apiKey == "" {
			return "", fmt.Errorf("环境变量 %s 为空", ref)
		}
		return apiKey, nil
	case APIKeyStorageSecretFile:
		if ref == "" {
			return "", fmt.Errorf("Secret 文件路径为空")
		}
		data, err := os.ReadFile(ref)
		if err != nil {
			return "", err
		}
		apiKey := strings.TrimSpace(string(data))
		if apiKey == "" {
			return "", fmt.Errorf("Secret 文件为空")
		}
		return apiKey, nil
	case APIKeyStorageWindowsCredentialManager:
		apiKey, _, err := readWindowsCredential(ref)
		return apiKey, err
	default:
		return "", fmt.Errorf("未知 Secret 来源 %q", endpoint.APIKeySource)
	}
}

func (b *SecretBackend) ResolveDraft(endpoint Endpoint) (string, error) {
	b.mu.RLock()
	mutation, exists := b.draft[endpoint.ID]
	b.mu.RUnlock()
	if exists && mutation.Source == endpoint.APIKeySource && mutation.Ref == endpoint.APIKeyRef {
		if mutation.Clear {
			return "", nil
		}
		return mutation.Value, nil
	}
	return b.Resolve(endpoint)
}

func (b *SecretBackend) Stage(endpoint Endpoint, value string, clearSecret bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.draft[endpoint.ID] = secretMutation{
		Source: endpoint.APIKeySource,
		Ref:    endpoint.APIKeyRef,
		Value:  value,
		Clear:  clearSecret,
	}
}

func (b *SecretBackend) DraftMutation(endpoint Endpoint) (secretMutation, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	mutation, exists := b.draft[endpoint.ID]
	if !exists || mutation.Source != endpoint.APIKeySource || mutation.Ref != endpoint.APIKeyRef {
		return secretMutation{}, false
	}
	return mutation, true
}

func (b *SecretBackend) ReconcileDraft(cfg Configuration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for endpointID, mutation := range b.draft {
		endpoint, err := endpointByID(cfg, endpointID)
		if err != nil || endpoint.APIKeySource != mutation.Source || endpoint.APIKeyRef != mutation.Ref {
			delete(b.draft, endpointID)
		}
	}
}

func (b *SecretBackend) DiscardDraft() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.draft)
}

func (b *SecretBackend) HasDraftChanges() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.draft) > 0
}

func (b *SecretBackend) Configured(endpoint Endpoint) bool {
	ref := strings.TrimSpace(endpoint.APIKeyRef)
	switch endpoint.APIKeySource {
	case "", APIKeyStorageManual:
		b.mu.RLock()
		value := b.manual[ref]
		b.mu.RUnlock()
		return value != ""
	case APIKeyStorageEnv, APIKeyStorageSecretFile:
		return ref != ""
	case APIKeyStorageWindowsCredentialManager:
		return endpoint.APIKeyConfigured
	default:
		return false
	}
}

func (b *SecretBackend) StoreForMigration(endpoint Endpoint, value string) error {
	if value == "" {
		return nil
	}
	switch endpoint.APIKeySource {
	case "", APIKeyStorageManual:
		b.mu.Lock()
		defer b.mu.Unlock()
		b.manual[endpoint.APIKeyRef] = value
		return b.writeManualLocked()
	case APIKeyStorageWindowsCredentialManager:
		return writeWindowsCredential(endpoint.APIKeyRef, value)
	default:
		return nil
	}
}

func (b *SecretBackend) Apply(previous, next Configuration) (func() error, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	manualBefore := cloneSecretMap(b.manual)
	windowsBefore := make(map[string]credentialState)
	manualChanged := false

	for _, endpoint := range previous.Endpoints {
		if !persistentSecretSource(endpoint.APIKeySource) || secretReferenceUsed(next, endpoint.APIKeySource, endpoint.APIKeyRef) {
			continue
		}
		switch endpoint.APIKeySource {
		case APIKeyStorageManual:
			delete(b.manual, endpoint.APIKeyRef)
			manualChanged = true
		case APIKeyStorageWindowsCredentialManager:
			if err := rememberWindowsCredential(windowsBefore, endpoint.APIKeyRef); err != nil {
				b.manual = manualBefore
				return nil, err
			}
			if err := writeWindowsCredential(endpoint.APIKeyRef, ""); err != nil {
				b.manual = manualBefore
				_ = restoreWindowsCredentials(windowsBefore)
				return nil, fmt.Errorf("清除 Windows 凭据 %q 失败: %w", endpoint.APIKeyRef, err)
			}
		}
	}

	for endpointID, mutation := range b.draft {
		if !persistentSecretSource(mutation.Source) {
			continue
		}
		endpoint, err := endpointByID(next, endpointID)
		if err != nil || endpoint.APIKeySource != mutation.Source || endpoint.APIKeyRef != mutation.Ref {
			continue
		}
		value := mutation.Value
		if mutation.Clear {
			value = ""
		}
		switch mutation.Source {
		case APIKeyStorageManual:
			if value == "" {
				delete(b.manual, mutation.Ref)
			} else {
				b.manual[mutation.Ref] = value
			}
			manualChanged = true
		case APIKeyStorageWindowsCredentialManager:
			if err := rememberWindowsCredential(windowsBefore, mutation.Ref); err != nil {
				b.manual = manualBefore
				_ = restoreWindowsCredentials(windowsBefore)
				return nil, err
			}
			if err := writeWindowsCredential(mutation.Ref, value); err != nil {
				b.manual = manualBefore
				_ = restoreWindowsCredentials(windowsBefore)
				return nil, fmt.Errorf("写入 Windows 凭据 %q 失败: %w", mutation.Ref, err)
			}
		}
	}

	if manualChanged {
		if err := b.writeManualLocked(); err != nil {
			b.manual = manualBefore
			_ = restoreWindowsCredentials(windowsBefore)
			return nil, err
		}
	}

	rolledBack := false
	return func() error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if rolledBack {
			return nil
		}
		rolledBack = true
		b.manual = cloneSecretMap(manualBefore)
		var firstErr error
		if manualChanged {
			firstErr = b.writeManualLocked()
		}
		if err := restoreWindowsCredentials(windowsBefore); firstErr == nil {
			firstErr = err
		}
		return firstErr
	}, nil
}

type credentialState struct {
	value  string
	exists bool
}

func rememberWindowsCredential(states map[string]credentialState, ref string) error {
	if _, exists := states[ref]; exists {
		return nil
	}
	value, exists, err := readWindowsCredential(ref)
	if err != nil {
		return fmt.Errorf("读取 Windows 凭据 %q 失败: %w", ref, err)
	}
	states[ref] = credentialState{value: value, exists: exists}
	return nil
}

func restoreWindowsCredentials(states map[string]credentialState) error {
	for ref, state := range states {
		value := ""
		if state.exists {
			value = state.value
		}
		if err := writeWindowsCredential(ref, value); err != nil {
			return fmt.Errorf("恢复 Windows 凭据 %q 失败: %w", ref, err)
		}
	}
	return nil
}

func (b *SecretBackend) writeManualLocked() error {
	return writeJSONAtomic(b.manualPath, manualSecretStore{
		Version: manualSecretStoreVersion,
		Secrets: cloneSecretMap(b.manual),
	}, "模型路由 Secret")
}

func persistentSecretSource(source APIKeyStorage) bool {
	return source == APIKeyStorageManual || source == APIKeyStorageWindowsCredentialManager
}

func secretReferenceUsed(cfg Configuration, source APIKeyStorage, ref string) bool {
	for _, endpoint := range cfg.Endpoints {
		if endpoint.APIKeySource == source && endpoint.APIKeyRef == ref {
			return true
		}
	}
	return false
}

func cloneSecretMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
