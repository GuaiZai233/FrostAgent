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
	response := &v1.BalanceQueryConfigResponse{Curl: cfg.Curl, ApiKeyConfigured: cfg.APIKey != "", ProviderId: cfg.ProviderID, QuotaPerUnit: cfg.QuotaPerUnit, Newapi: cfg.NewAPI, UsdExchangeRate: cfg.USDExchangeRate, QuotaDisplayType: cfg.QuotaDisplayType}
	if s.engine.ModelRouter != nil {
		if catalog, err := s.engine.ModelRouter.ActiveChecked(); err == nil {
			for _, endpoint := range catalog.Endpoints {
				response.Providers = append(response.Providers, &v1.BalanceQueryProvider{
					Id: endpoint.ID, DisplayName: endpoint.DisplayName,
					Available: endpoint.Enabled && endpoint.APIKeyConfigured,
					BaseUrl:   endpoint.BaseURL,
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
	if err := balancequery.ValidateQuotaPerUnit(req.Msg.QuotaPerUnit); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	cfg.Curl = req.Msg.Curl
	cfg.QuotaPerUnit = req.Msg.QuotaPerUnit
	cfg.NewAPI = req.Msg.Newapi
	cfg.USDExchangeRate = req.Msg.UsdExchangeRate
	cfg.QuotaDisplayType = req.Msg.QuotaDisplayType
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
	// A blank credential with nothing stored is a keyless template. Clearing a key is not keyless.
	cfg.Keyless = !req.Msg.ClearApiKey && cfg.APIKey == "" && cfg.ProviderID == ""
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
	if err := balancequery.ValidateQuotaPerUnit(req.Msg.QuotaPerUnit); err != nil {
		return fail(err.Error())
	}
	candidate := balancequery.Config{Curl: req.Msg.Curl, QuotaPerUnit: req.Msg.QuotaPerUnit, NewAPI: req.Msg.Newapi}
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
		// No credential typed and nothing reusable: only a keyless template can run without one.
		if cfg.ProviderID != "" {
			return fail("请填写 API Key")
		}
		candidate.Keyless = true
	}
	if err := candidate.Validate(); err != nil {
		return fail(err.Error())
	}
	body, err := balancequery.Execute(ctx, candidate)
	if err != nil {
		return fail(err.Error())
	}
	return connect.NewResponse(&v1.TestBalanceQueryConfigResponse{Ok: true, Message: "连接成功，接口返回：\n" + truncateRunes(body, testResultMaxRunes)}), nil
}

// testResultMaxRunes bounds the response body shown back to the operator by the test action.
const testResultMaxRunes = 2000

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…（已截断）"
}

// ClearBalanceQueryConfig deletes the stored configuration so balance_query reports unconfigured.
func (s *Service) ClearBalanceQueryConfig(ctx context.Context, req *connect.Request[v1.ClearBalanceQueryConfigRequest]) (*connect.Response[v1.BalanceQueryConfigResponse], error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if err := s.engine.Config.Update(balancequery.ConfigKey, "", true); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("清除额度查询配置失败"))
	}
	return s.balanceConfigResponse(balancequery.Config{Curl: balancequery.DefaultCurl}), nil
}

// FetchQuotaConfig reads the conversion settings from the template's New API site (/api/status).
func (s *Service) FetchQuotaConfig(ctx context.Context, req *connect.Request[v1.FetchQuotaConfigRequest]) (*connect.Response[v1.FetchQuotaConfigResponse], error) {
	origin, err := balancequery.OriginOf(req.Msg.Curl)
	if err != nil {
		return connect.NewResponse(&v1.FetchQuotaConfigResponse{Ok: false, Message: "请先填写有效的请求模板"}), nil
	}
	status, err := balancequery.FetchQuotaStatus(ctx, origin)
	if err != nil {
		return connect.NewResponse(&v1.FetchQuotaConfigResponse{Ok: false, Message: err.Error()}), nil
	}
	return connect.NewResponse(&v1.FetchQuotaConfigResponse{
		Ok: true, Message: "已从远端获取换算配置",
		QuotaPerUnit: status.QuotaPerUnit, UsdExchangeRate: status.USDExchangeRate, QuotaDisplayType: status.QuotaDisplayType,
	}), nil
}
