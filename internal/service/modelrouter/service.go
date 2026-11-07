package modelrouter

import (
	"context"
	"errors"
	"strings"

	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	router "github.com/GuaiZai233/FrostAgent/internal/modelrouter"

	"connectrpc.com/connect"
)

type Service struct {
	manager *router.Manager
}

const draftOwnerHeader = "X-FrostAgent-Catalog-Draft"
const missingDraftOwner = "共享供应商修改需要编辑页面标识"

func (s *Service) missingDraftOwner(owner string) bool {
	return s.manager.IsSharedCatalog() && owner == ""
}

func New(manager *router.Manager) *Service {
	return &Service{manager: manager}
}

func (s *Service) GetState(
	_ context.Context,
	req *connect.Request[v1.GetStateRequest],
) (*connect.Response[v1.GetStateResponse], error) {
	owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
	active, draft, err := s.manager.StateForOwner(owner)
	if s.manager.IsSharedCatalog() && owner == "" {
		draft = active
	}
	loadError := ""
	if err != nil {
		loadError = err.Error()
	}
	return connect.NewResponse(&v1.GetStateResponse{
		Active:                configurationToProto(active),
		Draft:                 configurationToProto(draft),
		LoadError:             loadError,
		HasDraftSecretChanges: owner != "" && s.manager.HasDraftSecretChangesForOwner(owner),
	}), nil
}

func (s *Service) SaveDraft(
	_ context.Context,
	req *connect.Request[v1.SaveDraftRequest],
) (*connect.Response[v1.SaveDraftResponse], error) {
	if req.Msg.GetConfiguration() == nil {
		return connect.NewResponse(&v1.SaveDraftResponse{
			Error: "configuration is required",
		}), nil
	}
	cfg := configurationFromProto(req.Msg.GetConfiguration())
	owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
	if s.missingDraftOwner(owner) {
		return connect.NewResponse(&v1.SaveDraftResponse{Error: missingDraftOwner}), nil
	}
	if err := s.manager.SaveDraftForOwner(cfg, owner); err != nil {
		return connect.NewResponse(&v1.SaveDraftResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.SaveDraftResponse{
		Success: true,
		Draft:   configurationToProto(s.manager.Draft()),
	}), nil
}

func (s *Service) SetDraftEndpointSecret(
	_ context.Context,
	req *connect.Request[v1.SetDraftEndpointSecretRequest],
) (*connect.Response[v1.SetDraftEndpointSecretResponse], error) {
	owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
	if s.missingDraftOwner(owner) {
		return connect.NewResponse(&v1.SetDraftEndpointSecretResponse{Error: missingDraftOwner}), nil
	}
	configured, err := s.manager.SetDraftEndpointSecretForOwner(
		strings.TrimSpace(req.Msg.GetEndpointId()),
		req.Msg.GetApiKey(),
		owner,
	)
	if err != nil {
		return connect.NewResponse(&v1.SetDraftEndpointSecretResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.SetDraftEndpointSecretResponse{
		Success:    true,
		Configured: configured,
	}), nil
}

func (s *Service) ClearDraftEndpointSecret(
	_ context.Context,
	req *connect.Request[v1.ClearDraftEndpointSecretRequest],
) (*connect.Response[v1.ClearDraftEndpointSecretResponse], error) {
	owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
	if s.missingDraftOwner(owner) {
		return connect.NewResponse(&v1.ClearDraftEndpointSecretResponse{Error: missingDraftOwner}), nil
	}
	configured, err := s.manager.ClearDraftEndpointSecretForOwner(strings.TrimSpace(req.Msg.GetEndpointId()),
		owner)
	if err != nil {
		return connect.NewResponse(&v1.ClearDraftEndpointSecretResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.ClearDraftEndpointSecretResponse{
		Success:    true,
		Configured: configured,
	}), nil
}

func (s *Service) DiscardDraft(
	_ context.Context,
	req *connect.Request[v1.DiscardDraftRequest],
) (*connect.Response[v1.DiscardDraftResponse], error) {
	var draft router.Configuration
	var err error
	if req.Header().Get("X-FrostAgent-Catalog-Draft-Reset") == "true" {
		draft, err = s.manager.ResetSharedDraft()
	} else {
		owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
		if s.missingDraftOwner(owner) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(missingDraftOwner))
		}
		draft, err = s.manager.DiscardDraftForOwner(owner)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connect.NewResponse(&v1.DiscardDraftResponse{
		Draft: configurationToProto(draft),
	}), nil
}

func (s *Service) Publish(
	_ context.Context,
	req *connect.Request[v1.PublishRequest],
) (*connect.Response[v1.PublishResponse], error) {
	owner := strings.TrimSpace(req.Header().Get(draftOwnerHeader))
	if s.missingDraftOwner(owner) {
		return connect.NewResponse(&v1.PublishResponse{Error: missingDraftOwner}), nil
	}
	var active router.Configuration
	var err error
	if s.manager.IsSharedCatalog() {
		active, err = s.manager.PublishForOwner(owner)
	} else {
		if req.Msg.GetExpectedConfiguration() == nil {
			return connect.NewResponse(&v1.PublishResponse{Error: "实例路由发布缺少预期配置，请刷新页面后重试"}), nil
		}
		active, err = s.manager.PublishExpectedRoutes(configurationFromProto(req.Msg.GetExpectedConfiguration()))
	}
	if err != nil {
		return connect.NewResponse(&v1.PublishResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.PublishResponse{
		Success: true,
		Active:  configurationToProto(active),
	}), nil
}

func (s *Service) ListUpstreamModels(
	_ context.Context,
	req *connect.Request[v1.ListUpstreamModelsRequest],
) (*connect.Response[v1.ListUpstreamModelsResponse], error) {
	models, err := s.manager.ListUpstreamModelsForOwner(strings.TrimSpace(req.Msg.GetEndpointId()),
		strings.TrimSpace(req.Header().Get(draftOwnerHeader)))
	if err != nil {
		return connect.NewResponse(&v1.ListUpstreamModelsResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.ListUpstreamModelsResponse{Models: models}), nil
}

func (s *Service) TestModel(
	_ context.Context,
	req *connect.Request[v1.TestModelRequest],
) (*connect.Response[v1.TestModelResponse], error) {
	content, duration, err := s.manager.TestModelForOwner(strings.TrimSpace(req.Msg.GetModelId()),
		strings.TrimSpace(req.Header().Get(draftOwnerHeader)))
	if err != nil {
		return connect.NewResponse(&v1.TestModelResponse{
			Error:      err.Error(),
			DurationMs: duration.Milliseconds(),
		}), nil
	}
	return connect.NewResponse(&v1.TestModelResponse{
		Success:    true,
		Content:    content,
		DurationMs: duration.Milliseconds(),
	}), nil
}

func configurationToProto(cfg router.Configuration) *v1.ModelRouterConfiguration {
	result := &v1.ModelRouterConfiguration{
		Version:  int32(cfg.Version),
		Revision: cfg.Revision,
	}
	for _, endpoint := range cfg.Endpoints {
		result.Endpoints = append(result.Endpoints, &v1.ModelEndpoint{
			Id:               endpoint.ID,
			DisplayName:      endpoint.DisplayName,
			BaseUrl:          endpoint.BaseURL,
			Enabled:          endpoint.Enabled,
			ApiKeySource:     apiKeyStorageToProto(endpoint.APIKeySource),
			ApiKeyRef:        endpoint.APIKeyRef,
			ApiKeyConfigured: endpoint.APIKeyConfigured,
		})
	}
	for _, model := range cfg.Models {
		result.Models = append(result.Models, &v1.ModelTarget{
			Id:             model.ID,
			DisplayName:    model.DisplayName,
			EndpointId:     model.EndpointID,
			UpstreamModel:  model.UpstreamModel,
			Enabled:        model.Enabled,
			Capabilities:   append([]string(nil), model.Capabilities...),
			MaxInputTokens: int32(model.MaxInputTokens),
		})
	}
	for _, workload := range router.Workloads {
		binding := cfg.GlobalBindings[workload]
		result.GlobalBindings = append(result.GlobalBindings, bindingToProto(workload, binding))
	}
	for _, group := range cfg.GroupOverrides {
		item := &v1.GroupModelOverride{Platform: group.Platform, GroupId: group.GroupID}
		for _, workload := range router.Workloads {
			if binding, ok := group.Bindings[workload]; ok {
				item.Bindings = append(item.Bindings, bindingToProto(workload, binding))
			}
		}
		result.GroupOverrides = append(result.GroupOverrides, item)
	}
	return result
}

func configurationFromProto(cfg *v1.ModelRouterConfiguration) router.Configuration {
	result := router.Configuration{
		Version:        int(cfg.GetVersion()),
		Revision:       cfg.GetRevision(),
		GlobalBindings: make(map[router.Workload]router.Binding),
	}
	for _, endpoint := range cfg.GetEndpoints() {
		result.Endpoints = append(result.Endpoints, router.Endpoint{
			ID:               endpoint.GetId(),
			DisplayName:      endpoint.GetDisplayName(),
			BaseURL:          endpoint.GetBaseUrl(),
			APIKeySource:     apiKeyStorageFromProto(endpoint.GetApiKeySource()),
			APIKeyRef:        endpoint.GetApiKeyRef(),
			APIKeyConfigured: endpoint.GetApiKeyConfigured(),
			Enabled:          endpoint.GetEnabled(),
		})
	}
	for _, model := range cfg.GetModels() {
		result.Models = append(result.Models, router.Model{
			ID:             model.GetId(),
			DisplayName:    model.GetDisplayName(),
			EndpointID:     model.GetEndpointId(),
			UpstreamModel:  model.GetUpstreamModel(),
			Enabled:        model.GetEnabled(),
			Capabilities:   append([]string(nil), model.GetCapabilities()...),
			MaxInputTokens: int(model.GetMaxInputTokens()),
		})
	}
	for _, binding := range cfg.GetGlobalBindings() {
		workload := workloadFromProto(binding.GetWorkload())
		if workload != "" {
			result.GlobalBindings[workload] = bindingFromProto(binding)
		}
	}
	for _, group := range cfg.GetGroupOverrides() {
		item := router.GroupOverride{
			Platform: group.GetPlatform(),
			GroupID:  group.GetGroupId(),
			Bindings: make(map[router.Workload]router.Binding),
		}
		for _, binding := range group.GetBindings() {
			workload := workloadFromProto(binding.GetWorkload())
			if workload != "" {
				item.Bindings[workload] = bindingFromProto(binding)
			}
		}
		result.GroupOverrides = append(result.GroupOverrides, item)
	}
	return result
}

func bindingToProto(workload router.Workload, binding router.Binding) *v1.ModelBinding {
	return &v1.ModelBinding{
		Workload: workloadToProto(workload),
		Mode:     bindingModeToProto(binding.Mode),
		ModelId:  binding.ModelID,
	}
}

func bindingFromProto(binding *v1.ModelBinding) router.Binding {
	return router.Binding{
		Mode:    bindingModeFromProto(binding.GetMode()),
		ModelID: binding.GetModelId(),
	}
}

func workloadToProto(workload router.Workload) v1.ModelWorkload {
	switch workload {
	case router.WorkloadDialogue:
		return v1.ModelWorkload_MODEL_WORKLOAD_DIALOGUE
	case router.WorkloadSubagent:
		return v1.ModelWorkload_MODEL_WORKLOAD_SUBAGENT
	case router.WorkloadVision:
		return v1.ModelWorkload_MODEL_WORKLOAD_VISION
	case router.WorkloadReflection:
		return v1.ModelWorkload_MODEL_WORKLOAD_REFLECTION
	case router.WorkloadMemoryExtract:
		return v1.ModelWorkload_MODEL_WORKLOAD_MEMORY_EXTRACT
	case router.WorkloadEmbedding:
		return v1.ModelWorkload_MODEL_WORKLOAD_EMBEDDING
	case router.WorkloadGroupCompact:
		return v1.ModelWorkload_MODEL_WORKLOAD_GROUP_COMPACT
	default:
		return v1.ModelWorkload_MODEL_WORKLOAD_UNSPECIFIED
	}
}

func workloadFromProto(workload v1.ModelWorkload) router.Workload {
	switch workload {
	case v1.ModelWorkload_MODEL_WORKLOAD_DIALOGUE:
		return router.WorkloadDialogue
	case v1.ModelWorkload_MODEL_WORKLOAD_SUBAGENT:
		return router.WorkloadSubagent
	case v1.ModelWorkload_MODEL_WORKLOAD_VISION:
		return router.WorkloadVision
	case v1.ModelWorkload_MODEL_WORKLOAD_REFLECTION:
		return router.WorkloadReflection
	case v1.ModelWorkload_MODEL_WORKLOAD_MEMORY_EXTRACT:
		return router.WorkloadMemoryExtract
	case v1.ModelWorkload_MODEL_WORKLOAD_EMBEDDING:
		return router.WorkloadEmbedding
	case v1.ModelWorkload_MODEL_WORKLOAD_GROUP_COMPACT:
		return router.WorkloadGroupCompact
	default:
		return ""
	}
}

func bindingModeToProto(mode router.BindingMode) v1.ModelBindingMode {
	switch mode {
	case router.BindingInherit:
		return v1.ModelBindingMode_MODEL_BINDING_MODE_INHERIT
	case router.BindingModel:
		return v1.ModelBindingMode_MODEL_BINDING_MODE_MODEL
	case router.BindingDisabled:
		return v1.ModelBindingMode_MODEL_BINDING_MODE_DISABLED
	case router.BindingFollowDialogue:
		return v1.ModelBindingMode_MODEL_BINDING_MODE_FOLLOW_DIALOGUE
	default:
		return v1.ModelBindingMode_MODEL_BINDING_MODE_UNSPECIFIED
	}
}

func bindingModeFromProto(mode v1.ModelBindingMode) router.BindingMode {
	switch mode {
	case v1.ModelBindingMode_MODEL_BINDING_MODE_INHERIT:
		return router.BindingInherit
	case v1.ModelBindingMode_MODEL_BINDING_MODE_MODEL:
		return router.BindingModel
	case v1.ModelBindingMode_MODEL_BINDING_MODE_DISABLED:
		return router.BindingDisabled
	case v1.ModelBindingMode_MODEL_BINDING_MODE_FOLLOW_DIALOGUE:
		return router.BindingFollowDialogue
	default:
		return ""
	}
}

func apiKeyStorageToProto(storage router.APIKeyStorage) v1.ModelAPIKeyStorage {
	switch storage {
	case router.APIKeyStorageManual, "":
		return v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_MANUAL
	case router.APIKeyStorageEnv:
		return v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_ENV
	case router.APIKeyStorageSecretFile:
		return v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_SECRET_FILE
	case router.APIKeyStorageWindowsCredentialManager:
		return v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_WINDOWS_CREDENTIAL_MANAGER
	default:
		return v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_UNSPECIFIED
	}
}

func apiKeyStorageFromProto(storage v1.ModelAPIKeyStorage) router.APIKeyStorage {
	switch storage {
	case v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_ENV:
		return router.APIKeyStorageEnv
	case v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_SECRET_FILE:
		return router.APIKeyStorageSecretFile
	case v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_WINDOWS_CREDENTIAL_MANAGER:
		return router.APIKeyStorageWindowsCredentialManager
	case v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_MANUAL,
		v1.ModelAPIKeyStorage_MODEL_API_KEY_STORAGE_UNSPECIFIED:
		return router.APIKeyStorageManual
	default:
		return ""
	}
}
