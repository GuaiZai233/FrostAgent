package botstatus

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"connectrpc.com/connect"
	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
	"github.com/GuaiZai233/FrostAgent/internal/balancequery"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
)

func (s *Service) SetToolEnabled(ctx context.Context, req *connect.Request[v1.SetToolEnabledRequest]) (*connect.Response[v1.SetToolEnabledResponse], error) {
	if _, ok := s.engine.ToolRegistry[req.Msg.Name]; !ok {
		// Retired names only support clearing an old disable; they cannot create
		// new switches or become callable tool registrations.
		if _, legacy := s.legacyToolSettings()[req.Msg.Name]; !legacy || !req.Msg.Enabled {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("工具未注册"))
		}
	}
	if s.engine.Scope == nil || s.engine.Config == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("实例配置不可用"))
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if req.Msg.Enabled && req.Msg.Name == balancequery.ToolName {
		if availability, ok := s.engine.ToolRegistry[req.Msg.Name].(interface{ IsEnabled() bool }); ok && !availability.IsEnabled() {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("工具启用失败：请先点击卡片后进行配置！"))
		}
	}
	var disabled []string
	if raw := s.engine.Config.Get(llm.DisabledToolsKey); raw != "" {
		if json.Unmarshal([]byte(raw), &disabled) != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("工具开关配置无法解析"))
		}
	}
	next := make([]string, 0, len(disabled)+1)
	for _, name := range disabled {
		if name != req.Msg.Name && !(req.Msg.Name == qqextend.ToolName && name == "qq") {
			next = append(next, name)
		}
	}
	if !req.Msg.Enabled {
		next = append(next, req.Msg.Name)
	}
	sort.Strings(next)
	raw, _ := json.Marshal(next)
	if err := s.engine.Config.Update(llm.DisabledToolsKey, string(raw), false); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("保存工具开关失败"))
	}
	return connect.NewResponse(&v1.SetToolEnabledResponse{}), nil
}

// legacyToolSettings only accepts retired names advertised by registered tools.
func (s *Service) legacyToolSettings() map[string]string {
	legacy := make(map[string]string)
	for _, tool := range s.engine.ToolRegistry {
		provider, ok := tool.(interface{ LegacyToolSettings() map[string]string })
		if !ok {
			continue
		}
		for name, description := range provider.LegacyToolSettings() {
			if _, registered := s.engine.ToolRegistry[name]; !registered {
				legacy[name] = description
			}
		}
	}
	return legacy
}
