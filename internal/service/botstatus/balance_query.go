package botstatus

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
)

func balanceConfigResponse(cfg balancequery.Config) *connect.Response[v1.BalanceQueryConfigResponse] {
	return connect.NewResponse(&v1.BalanceQueryConfigResponse{Curl: cfg.Curl, ApiKeyConfigured: cfg.APIKey != ""})
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
	return balanceConfigResponse(cfg), nil
}

func (s *Service) SaveBalanceQueryConfig(ctx context.Context, req *connect.Request[v1.SaveBalanceQueryConfigRequest]) (*connect.Response[v1.BalanceQueryConfigResponse], error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg, err := s.balanceConfig()
	if err != nil {
		return nil, err
	}
	if req.Msg.ClearApiKey && req.Msg.ApiKey != "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("不能同时更新和清除 API key"))
	}
	cfg.Curl = req.Msg.Curl
	if req.Msg.ClearApiKey {
		cfg.APIKey = ""
	} else if req.Msg.ApiKey != "" {
		cfg.APIKey = req.Msg.ApiKey
	}
	if err := cfg.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	raw, _ := json.Marshal(cfg)
	if err := s.engine.Config.Update(balancequery.ConfigKey, string(raw), false); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("保存额度查询配置失败"))
	}
	return balanceConfigResponse(cfg), nil
}
