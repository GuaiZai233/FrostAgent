package botstatus

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
)

func (s *Service) balanceConfigResponse(cfg balancequery.Config) *connect.Response[v1.BalanceQueryConfigResponse] {
	response := &v1.BalanceQueryConfigResponse{Curl: cfg.Curl, ApiKeyConfigured: cfg.APIKey != "", ProviderId: cfg.ProviderID}
	if s.engine.ModelRouter != nil {
		if catalog, err := s.engine.ModelRouter.ActiveChecked(); err == nil {
			for _, endpoint := range catalog.Endpoints {
				response.Providers = append(response.Providers, &v1.BalanceQueryProvider{
					Id: endpoint.ID, DisplayName: endpoint.DisplayName,
					Available: endpoint.Enabled && endpoint.APIKeyConfigured,
				})
			}
		}
	}
	if cfg.ProviderID != "" {
		key, err := s.engine.ModelRouter.EndpointAPIKey(cfg.ProviderID)
		response.ApiKeyConfigured = err == nil && key != ""
	}
	return connect.NewResponse(response)
}

func (s *Service) balanceConfig() (balancequery.Config, error) {
	if s.engine.Scope == nil || s.engine.Config == nil {
		return balancequery.Config{}, connect.NewError(connect.CodeFailedPrecondition, errors.New("实例配置不可用"))
	}
	cfg, err := balancequery.Load(s.engine.Config.Get)
	if err != nil {
		return cfg, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return cfg, nil
}

func (s *Service) GetBalanceQueryConfig(ctx context.Context, req *connect.Request[v1.GetBalanceQueryConfigRequest]) (*connect.Response[v1.BalanceQueryConfigResponse], error) {
	cfg, err := s.balanceConfig()
	if err != nil {
		return nil, err
	}
	return s.balanceConfigResponse(cfg), nil
}

func (s *Service) SaveBalanceQueryConfig(ctx context.Context, req *connect.Request[v1.SaveBalanceQueryConfigRequest]) (*connect.Response[v1.BalanceQueryConfigResponse], error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg, err := s.balanceConfig()
	if err != nil {
		return nil, err
	}
	if req.Msg.ProviderId != "" && req.Msg.ApiKey != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("复用 Provider 时无需填写 API Key"))
	}
	previousCurl := cfg.Curl
	cfg.Curl = req.Msg.Curl
	validated := cfg
	if req.Msg.ClearApiKey {
		if req.Msg.ApiKey != "" || req.Msg.ProviderId != "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("清除 API Key 时不能同时填写 API Key 或复用 Provider"))
		}
		cfg.APIKey, cfg.ProviderID = "", ""
		validated = cfg
	} else if req.Msg.ProviderId != "" {
		key, err := s.engine.ModelRouter.EndpointAPIKey(req.Msg.ProviderId)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("请选择已启用且已配置 API Key 的 Provider"))
		}
		cfg.ProviderID, cfg.APIKey = req.Msg.ProviderId, ""
		validated = balancequery.Config{Curl: cfg.Curl, APIKey: key}
	} else {
		if cfg.ProviderID != "" && req.Msg.ApiKey == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("切换为手动配置时请填写 API Key"))
		}
		cfg.ProviderID = ""
		// A blank key keeps the stored one. Only allow that when the request still targets the same
		// endpoint; otherwise the old secret would be sent to a different host or path.
		if req.Msg.ApiKey == "" && cfg.APIKey != "" && !balancequery.SameEndpoint(previousCurl, req.Msg.Curl) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("请求地址已变更，请重新输入 Key，不会复用原有密钥"))
		}
		if req.Msg.ApiKey != "" {
			cfg.APIKey = req.Msg.ApiKey
		}
		validated = cfg
	}
	if err := validated.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	raw, _ := json.Marshal(cfg)
	if err := s.engine.Config.Update(balancequery.ConfigKey, string(raw), false); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("保存额度查询配置失败"))
	}
	return s.balanceConfigResponse(cfg), nil
}

// TestBalanceQueryConfig runs a draft balance query once and never persists anything. Credentials
// follow the same rules as saving: a manual key is reused only when the endpoint is unchanged.
func (s *Service) TestBalanceQueryConfig(ctx context.Context, req *connect.Request[v1.TestBalanceQueryConfigRequest]) (*connect.Response[v1.TestBalanceQueryConfigResponse], error) {
	fail := func(message string) (*connect.Response[v1.TestBalanceQueryConfigResponse], error) {
		return connect.NewResponse(&v1.TestBalanceQueryConfigResponse{Ok: false, Message: message}), nil
	}
	if req.Msg.ProviderId != "" && req.Msg.ApiKey != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("复用 Provider 时无需填写 API Key"))
	}
	cfg, err := s.balanceConfig()
	if err != nil {
		return nil, err
	}
	candidate := balancequery.Config{Curl: req.Msg.Curl}
	switch {
	case req.Msg.ProviderId != "":
		key, err := s.engine.ModelRouter.EndpointAPIKey(req.Msg.ProviderId)
		if err != nil {
			return fail("请选择已启用且已配置 API Key 的 Provider")
		}
		candidate.APIKey = key
	case req.Msg.ApiKey != "":
		candidate.APIKey = req.Msg.ApiKey
	case cfg.APIKey != "" && cfg.ProviderID == "":
		if !balancequery.SameEndpoint(cfg.Curl, req.Msg.Curl) {
			return fail("请求地址已变更，请重新输入 Key，不会复用原有密钥")
		}
		candidate.APIKey = cfg.APIKey
	default:
		return fail("请填写 API Key")
	}
	if err := candidate.Validate(); err != nil {
		return fail(err.Error())
	}
	if _, err := balancequery.Execute(ctx, candidate); err != nil {
		return fail(err.Error())
	}
	return connect.NewResponse(&v1.TestBalanceQueryConfigResponse{Ok: true, Message: "连接成功，接口返回了有效结果"}), nil
}
