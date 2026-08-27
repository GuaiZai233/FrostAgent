package modelrouter

import (
	"context"
	"strings"

	v1 "FrostAgent/gen/proto/frostagent/v1"
	router "FrostAgent/internal/modelrouter"

	"connectrpc.com/connect"
)

type Service struct {
	manager *router.Manager
}

func New(manager *router.Manager) *Service {
	return &Service{manager: manager}
}

func (s *Service) GetState(
	context.Context,
	*connect.Request[v1.GetStateRequest],
) (*connect.Response[v1.GetStateResponse], error) {
	loadError := ""
	if err := s.manager.LoadError(); err != nil {
		loadError = err.Error()
	}
	return connect.NewResponse(&v1.GetStateResponse{
		Active:    configurationToProto(s.manager.Active()),
		Draft:     configurationToProto(s.manager.Draft()),
		LoadError: loadError,
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
	if err := s.manager.SaveDraft(cfg); err != nil {
		return connect.NewResponse(&v1.SaveDraftResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.SaveDraftResponse{
		Success: true,
		Draft:   configurationToProto(s.manager.Draft()),
	}), nil
}

func (s *Service) DiscardDraft(
	context.Context,
	*connect.Request[v1.DiscardDraftRequest],
) (*connect.Response[v1.DiscardDraftResponse], error) {
	return connect.NewResponse(&v1.DiscardDraftResponse{
		Draft: configurationToProto(s.manager.DiscardDraft()),
	}), nil
}

func (s *Service) Publish(
	context.Context,
	*connect.Request[v1.PublishRequest],
) (*connect.Response[v1.PublishResponse], error) {
	active, err := s.manager.Publish()
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
	models, err := s.manager.ListUpstreamModels(strings.TrimSpace(req.Msg.GetEndpointId()))
	if err != nil {
		return connect.NewResponse(&v1.ListUpstreamModelsResponse{Error: err.Error()}), nil
	}
	return connect.NewResponse(&v1.ListUpstreamModelsResponse{Models: models}), nil
}

func (s *Service) TestModel(
	_ context.Context,
	req *connect.Request[v1.TestModelRequest],
) (*connect.Response[v1.TestModelResponse], error) {
	content, duration, err := s.manager.TestModel(strings.TrimSpace(req.Msg.GetModelId()))
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
			Id:          endpoint.ID,
			DisplayName: endpoint.DisplayName,
			BaseUrl:     endpoint.BaseURL,
			ApiKey:      endpoint.APIKey,
			Enabled:     endpoint.Enabled,
		})
	}
	for _, model := range cfg.Models {
		result.Models = append(result.Models, &v1.ModelTarget{
			Id:            model.ID,
			DisplayName:   model.DisplayName,
			EndpointId:    model.EndpointID,
			UpstreamModel: model.UpstreamModel,
			Enabled:       model.Enabled,
			Capabilities:  append([]string(nil), model.Capabilities...),
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
			ID:          endpoint.GetId(),
			DisplayName: endpoint.GetDisplayName(),
			BaseURL:     endpoint.GetBaseUrl(),
			APIKey:      endpoint.GetApiKey(),
			Enabled:     endpoint.GetEnabled(),
		})
	}
	for _, model := range cfg.GetModels() {
		result.Models = append(result.Models, router.Model{
			ID:            model.GetId(),
			DisplayName:   model.GetDisplayName(),
			EndpointID:    model.GetEndpointId(),
			UpstreamModel: model.GetUpstreamModel(),
			Enabled:       model.GetEnabled(),
			Capabilities:  append([]string(nil), model.GetCapabilities()...),
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
