package modelrouter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const configurationVersion = 1

type Snapshot struct {
	config Configuration
}

type Manager struct {
	mu      sync.RWMutex
	path    string
	active  Configuration
	draft   Configuration
	loadErr error
}

func New(path string) *Manager {
	m := &Manager{path: path, active: defaultConfiguration()}
	if err := m.load(); err != nil {
		m.loadErr = err
	}
	m.draft = cloneConfiguration(m.active)
	return m
}

func defaultConfiguration() Configuration {
	bindings := make(map[Workload]Binding, len(Workloads))
	for _, workload := range Workloads {
		mode := BindingDisabled
		if workload == WorkloadReflection {
			mode = BindingFollowDialogue
		}
		bindings[workload] = Binding{Mode: mode}
	}
	return Configuration{
		Version:        configurationVersion,
		GlobalBindings: bindings,
		Endpoints:      []Endpoint{},
		Models:         []Model{},
		GroupOverrides: []GroupOverride{},
	}
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取模型路由配置失败: %w", err)
	}
	var cfg Configuration
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("解析模型路由配置失败: %w", err)
	}
	normalizeConfiguration(&cfg)
	if err := validateConfiguration(cfg); err != nil {
		return fmt.Errorf("模型路由配置无效: %w", err)
	}
	m.active = cfg
	return nil
}

func (m *Manager) LoadError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadErr
}

func (m *Manager) Active() Configuration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfiguration(m.active)
}

func (m *Manager) Draft() Configuration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfiguration(m.draft)
}

func (m *Manager) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &Snapshot{config: cloneConfiguration(m.active)}
}

func (m *Manager) SaveDraft(cfg Configuration) error {
	normalizeConfiguration(&cfg)
	if err := validateConfiguration(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg.Revision = m.active.Revision
	m.draft = cloneConfiguration(cfg)
	return nil
}

func (m *Manager) DiscardDraft() Configuration {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.draft = cloneConfiguration(m.active)
	return cloneConfiguration(m.draft)
}

func (m *Manager) Publish() (Configuration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := cloneConfiguration(m.draft)
	normalizeConfiguration(&cfg)
	if err := validateConfiguration(cfg); err != nil {
		return Configuration{}, err
	}
	cfg.Revision = m.active.Revision + 1
	if err := writeAtomic(m.path, cfg); err != nil {
		return Configuration{}, err
	}
	m.active = cloneConfiguration(cfg)
	m.draft = cloneConfiguration(cfg)
	m.loadErr = nil
	return cloneConfiguration(cfg), nil
}

func (m *Manager) Resolve(workload Workload, scope Scope) (Target, error) {
	return m.Snapshot().Resolve(workload, scope)
}

func (s *Snapshot) Resolve(workload Workload, scope Scope) (Target, error) {
	if s == nil {
		return Target{}, fmt.Errorf("model router snapshot is unavailable")
	}
	return resolveConfiguration(s.config, workload, scope)
}

func (s *Snapshot) IsDisabled(workload Workload, scope Scope) bool {
	_, err := s.Resolve(workload, scope)
	return err != nil
}

func (m *Manager) Effective(scope Scope) []EffectiveBinding {
	snapshot := m.Snapshot()
	result := make([]EffectiveBinding, 0, len(Workloads))
	for _, workload := range Workloads {
		binding, inherited := effectiveBinding(snapshot.config, workload, scope)
		item := EffectiveBinding{
			Workload:       workload,
			Binding:        binding,
			Inherited:      inherited,
			RuntimeApplied: RuntimeApplied(workload),
		}
		if target, err := resolveConfiguration(snapshot.config, workload, scope); err == nil {
			item.Target = &target
		}
		result = append(result, item)
	}
	return result
}

func resolveConfiguration(cfg Configuration, workload Workload, scope Scope) (Target, error) {
	binding, _ := effectiveBinding(cfg, workload, scope)
	if binding.Mode != BindingModel || binding.ModelID == "" {
		return Target{}, ErrDisabled
	}
	var model *Model
	for i := range cfg.Models {
		if cfg.Models[i].ID == binding.ModelID {
			model = &cfg.Models[i]
			break
		}
	}
	if model == nil || !model.Enabled {
		return Target{}, fmt.Errorf("model target %q is unavailable", binding.ModelID)
	}
	var endpoint *Endpoint
	for i := range cfg.Endpoints {
		if cfg.Endpoints[i].ID == model.EndpointID {
			endpoint = &cfg.Endpoints[i]
			break
		}
	}
	if endpoint == nil || !endpoint.Enabled {
		return Target{}, fmt.Errorf("endpoint %q is unavailable", model.EndpointID)
	}
	apiKey, err := resolveEndpointAPIKey(*endpoint)
	if err != nil {
		return Target{}, fmt.Errorf("读取 Endpoint %q 的 API Key 失败: %w", endpoint.DisplayName, err)
	}
	return Target{
		EndpointID:          endpoint.ID,
		EndpointDisplayName: endpoint.DisplayName,
		ModelID:             model.ID,
		ModelDisplayName:    model.DisplayName,
		BaseURL:             endpoint.BaseURL,
		APIKey:              apiKey,
		UpstreamModel:       model.UpstreamModel,
	}, nil
}

func effectiveBinding(cfg Configuration, workload Workload, scope Scope) (Binding, bool) {
	scope = scope.Normalized()
	binding, inherited := globalBinding(cfg, workload, scope.GroupID != "")
	if scope.GroupID != "" {
		for _, override := range cfg.GroupOverrides {
			if strings.EqualFold(strings.TrimSpace(override.Platform), scope.Platform) && strings.TrimSpace(override.GroupID) == scope.GroupID {
				if binding, ok := override.Bindings[workload]; ok && binding.Mode != "" && binding.Mode != BindingInherit {
					if workload != WorkloadDialogue && binding.Mode == BindingFollowDialogue {
						dialogue, _ := effectiveBinding(cfg, WorkloadDialogue, scope)
						return dialogue, true
					}
					return binding, false
				}
				break
			}
		}
	}
	if workload != WorkloadDialogue && binding.Mode == BindingFollowDialogue {
		dialogue, _ := effectiveBinding(cfg, WorkloadDialogue, scope)
		return dialogue, true
	}
	return binding, inherited
}

func globalBinding(cfg Configuration, workload Workload, inherited bool) (Binding, bool) {
	binding, ok := cfg.GlobalBindings[workload]
	if !ok || binding.Mode == "" || binding.Mode == BindingInherit {
		return Binding{Mode: BindingDisabled}, true
	}
	return binding, inherited
}

func normalizeConfiguration(cfg *Configuration) {
	if cfg.Version == 0 {
		cfg.Version = configurationVersion
	}
	if cfg.GlobalBindings == nil {
		cfg.GlobalBindings = make(map[Workload]Binding)
	}
	for _, workload := range Workloads {
		binding, ok := cfg.GlobalBindings[workload]
		if workload == WorkloadReflection {
			if !ok || binding.Mode == "" || binding.Mode == BindingInherit {
				cfg.GlobalBindings[workload] = Binding{Mode: BindingFollowDialogue}
			}
			continue
		}
		if !ok || binding.Mode == "" || binding.Mode == BindingInherit {
			cfg.GlobalBindings[workload] = Binding{Mode: BindingDisabled}
		}
	}
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].DisplayName = strings.TrimSpace(cfg.Endpoints[i].DisplayName)
		cfg.Endpoints[i].BaseURL = strings.TrimSpace(cfg.Endpoints[i].BaseURL)
		cfg.Endpoints[i].SecretFile = strings.TrimSpace(cfg.Endpoints[i].SecretFile)
		if cfg.Endpoints[i].APIKeyStorage == "" {
			cfg.Endpoints[i].APIKeyStorage = APIKeyStorageManual
		}
		switch cfg.Endpoints[i].APIKeyStorage {
		case APIKeyStorageManual:
			cfg.Endpoints[i].SecretFile = ""
		case APIKeyStorageEnv:
			cfg.Endpoints[i].APIKey = ""
			cfg.Endpoints[i].SecretFile = ""
		case APIKeyStorageSecretFile:
			cfg.Endpoints[i].APIKey = ""
		}
	}
	for i := range cfg.Models {
		cfg.Models[i].DisplayName = strings.TrimSpace(cfg.Models[i].DisplayName)
		cfg.Models[i].UpstreamModel = strings.TrimSpace(cfg.Models[i].UpstreamModel)
		cfg.Models[i].EndpointID = strings.TrimSpace(cfg.Models[i].EndpointID)
		sort.Strings(cfg.Models[i].Capabilities)
	}
	for i := range cfg.GroupOverrides {
		cfg.GroupOverrides[i].Platform = strings.ToLower(strings.TrimSpace(cfg.GroupOverrides[i].Platform))
		cfg.GroupOverrides[i].GroupID = strings.TrimSpace(cfg.GroupOverrides[i].GroupID)
		if cfg.GroupOverrides[i].Bindings == nil {
			cfg.GroupOverrides[i].Bindings = make(map[Workload]Binding)
		}
		for workload, binding := range cfg.GroupOverrides[i].Bindings {
			if binding.Mode == "" || binding.Mode == BindingInherit {
				delete(cfg.GroupOverrides[i].Bindings, workload)
			}
		}
	}
}

func validateConfiguration(cfg Configuration) error {
	if cfg.Version != configurationVersion {
		return fmt.Errorf("unsupported configuration version %d", cfg.Version)
	}
	endpointNames := make(map[string]struct{})
	endpointIDs := make(map[string]Endpoint)
	for _, endpoint := range cfg.Endpoints {
		if endpoint.ID == "" || endpoint.DisplayName == "" || endpoint.BaseURL == "" {
			return fmt.Errorf("endpoint id, display name and base url are required")
		}
		nameKey := strings.ToLower(strings.TrimSpace(endpoint.DisplayName))
		if _, exists := endpointNames[nameKey]; exists {
			return fmt.Errorf("endpoint display name %q is duplicated", endpoint.DisplayName)
		}
		endpointNames[nameKey] = struct{}{}
		if _, exists := endpointIDs[endpoint.ID]; exists {
			return fmt.Errorf("endpoint id %q is duplicated", endpoint.ID)
		}
		parsed, err := url.Parse(endpoint.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("endpoint %q has an invalid http/https base url", endpoint.DisplayName)
		}
		if parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("endpoint %q must not contain query or fragment", endpoint.DisplayName)
		}
		switch endpoint.APIKeyStorage {
		case APIKeyStorageManual, APIKeyStorageEnv:
		case APIKeyStorageSecretFile:
			if endpoint.SecretFile == "" {
				return fmt.Errorf("endpoint %q secret file path is required", endpoint.DisplayName)
			}
		default:
			return fmt.Errorf("endpoint %q has unknown api key storage %q", endpoint.DisplayName, endpoint.APIKeyStorage)
		}
		endpointIDs[endpoint.ID] = endpoint
	}
	modelNames := make(map[string]struct{})
	modelIDs := make(map[string]Model)
	for _, model := range cfg.Models {
		if model.ID == "" || model.DisplayName == "" || model.EndpointID == "" || model.UpstreamModel == "" {
			return fmt.Errorf("model id, display name, endpoint and upstream model are required")
		}
		nameKey := strings.ToLower(strings.TrimSpace(model.DisplayName))
		if _, exists := modelNames[nameKey]; exists {
			return fmt.Errorf("model display name %q is duplicated", model.DisplayName)
		}
		modelNames[nameKey] = struct{}{}
		if _, exists := modelIDs[model.ID]; exists {
			return fmt.Errorf("model id %q is duplicated", model.ID)
		}
		if _, exists := endpointIDs[model.EndpointID]; !exists {
			return fmt.Errorf("model %q references a missing endpoint", model.DisplayName)
		}
		modelIDs[model.ID] = model
	}
	for workload := range cfg.GlobalBindings {
		if !knownWorkload(workload) {
			return fmt.Errorf("global bindings contain unknown workload %q", workload)
		}
	}
	for _, workload := range Workloads {
		binding := cfg.GlobalBindings[workload]
		if workload == WorkloadReflection && binding.Mode == BindingDisabled {
			return fmt.Errorf("global reflection binding cannot be disabled")
		}
		if err := validateBinding(binding, modelIDs, endpointIDs, false, workload != WorkloadDialogue); err != nil {
			return fmt.Errorf("global %s binding: %w", workload, err)
		}
	}
	groupKeys := make(map[string]struct{})
	for _, group := range cfg.GroupOverrides {
		if group.Platform == "" || group.GroupID == "" {
			return fmt.Errorf("group override platform and group id are required")
		}
		key := strings.ToLower(group.Platform) + "\x00" + group.GroupID
		if _, exists := groupKeys[key]; exists {
			return fmt.Errorf("group override %s/%s is duplicated", group.Platform, group.GroupID)
		}
		groupKeys[key] = struct{}{}
		for workload, binding := range group.Bindings {
			if !knownWorkload(workload) {
				return fmt.Errorf("group %s/%s has unknown workload %q", group.Platform, group.GroupID, workload)
			}
			if workload == WorkloadReflection && binding.Mode == BindingDisabled {
				return fmt.Errorf("group %s/%s reflection binding cannot be disabled", group.Platform, group.GroupID)
			}
			if err := validateBinding(binding, modelIDs, endpointIDs, true, workload != WorkloadDialogue); err != nil {
				return fmt.Errorf("group %s/%s %s binding: %w", group.Platform, group.GroupID, workload, err)
			}
		}
	}
	return nil
}

func validateBinding(binding Binding, models map[string]Model, endpoints map[string]Endpoint, allowInherit, allowFollowDialogue bool) error {
	switch binding.Mode {
	case BindingDisabled:
		if binding.ModelID != "" {
			if _, ok := models[binding.ModelID]; !ok {
				return fmt.Errorf("remembered model %q does not exist", binding.ModelID)
			}
		}
		return nil
	case BindingInherit:
		if allowInherit {
			return nil
		}
		return fmt.Errorf("inherit is not valid globally")
	case BindingFollowDialogue:
		if allowFollowDialogue {
			return nil
		}
		return fmt.Errorf("dialogue cannot follow itself")
	case BindingModel:
		model, ok := models[binding.ModelID]
		if !ok {
			return fmt.Errorf("model %q does not exist", binding.ModelID)
		}
		endpoint, ok := endpoints[model.EndpointID]
		if !ok {
			return fmt.Errorf("endpoint %q does not exist", model.EndpointID)
		}
		if !model.Enabled || !endpoint.Enabled {
			return fmt.Errorf("referenced model and endpoint must be enabled")
		}
		return nil
	default:
		return fmt.Errorf("unknown binding mode %q", binding.Mode)
	}
}

func knownWorkload(workload Workload) bool {
	for _, candidate := range Workloads {
		if candidate == workload {
			return true
		}
	}
	return false
}

func cloneConfiguration(cfg Configuration) Configuration {
	data, _ := json.Marshal(cfg)
	var cloned Configuration
	_ = json.Unmarshal(data, &cloned)
	normalizeConfiguration(&cloned)
	return cloned
}

func writeAtomic(path string, cfg Configuration) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建模型路由配置目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".model-router-*.tmp")
	if err != nil {
		return fmt.Errorf("创建模型路由临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("设置模型路由配置权限失败: %w", err)
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cfg); err != nil {
		tmp.Close()
		return fmt.Errorf("序列化模型路由配置失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步模型路由配置失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭模型路由临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("原子替换模型路由配置失败: %w", err)
	}
	return nil
}
