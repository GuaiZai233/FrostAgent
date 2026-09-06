package settings

import (
	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/instanceconfig"
	"connectrpc.com/connect"
	"context"
	"fmt"
)

// envEntry defines metadata for a known environment variable.
type envEntry struct {
	Description     string
	IsSecret        bool
	RequiresRestart bool
}

// knownEnvVars is the registry of all env keys the settings page manages.
var knownEnvVars = map[string]envEntry{
	"LISTEN_ADDR":                 {"HTTP 监听地址", false, true},
	"WS_LISTEN_ADDR":              {"WebSocket 监听地址", false, true},
	"SYSTEM_PROMPT":               {"系统提示词", false, false},
	"DIALOGUE_PATH":               {"示例对话 YAML 文件路径（用于少样本人设提示词引导）", false, true},
	"MAX_CONTEXT_MESSAGES":        {"最多保留的消息数", false, false},
	"MAX_CONTEXT_CHARS":           {"近似字符上限", false, false},
	"WS_ALLOWED_ORIGINS":          {"允许的 WebSocket Origin", false, true},
	"ENABLE_AT_IN_GROUP_MSG":      {"是否开启群聊回复前艾特", false, false},
	"GROUP_REPLY_ON_MENTION":      {"群聊被@或名称/别名提及时触发对话回复（false 则群聊消息不回复）", false, false},
	"BOT_NAME":                    {"机器人主名称，用于群聊文本唤醒", false, false},
	"BOT_ALIASES":                 {"机器人文本唤醒别名，多个名称以英文逗号分隔", false, false},
	"ADMIN_QQ_IDS":                {"允许使用管理员工具的 QQ 号，多个号码以英文逗号分隔", false, false},
	"ENABLE_REPLY_IN_GROUP_MSG":   {"群聊回复时是否引用原消息", false, false},
	"GROUP_COMPACT_BUFFER_SIZE":   {"群聊 running compact 每批原消息数量", false, true},
	"GROUP_COMPACT_MIN_INTERVAL":  {"同群 running compact 最小触发间隔（如 30s）", false, true},
	"GROUP_RAW_CONTEXT_MAX_CHARS": {"群聊未压缩原消息临时上下文的最大字符数（默认 12000）", false, false},
	"MEMORY_EXTRACT_BATCH_MIN":    {"自动记忆提取的最小累计轮数", false, false},
	"MEMORY_EXTRACT_BATCH_MAX":    {"自动记忆提取的最大累计轮数", false, false},
	"ENABLE_ONEBOT_ADAPTER":       {"是否启用 OneBot WebSocket 适配器", false, true},
	"ONEBOT_WS_PATH":              {"OneBot WebSocket 监听路径 (默认 /ws/frostagent)", false, true},
	"ENABLE_ASTRBOT_ADAPTER":      {"是否启用 AstrBot WebSocket 适配器", false, true},
	"ASTRBOT_WS_PATH":             {"AstrBot WebSocket 监听路径 (默认 /ws/astrbot)", false, true},
}

type Service struct{ config, global *instanceconfig.Store }

func New(path string) *Service                      { c, _ := instanceconfig.Open(path, false); return NewScoped(c, nil) }
func NewScoped(c, g *instanceconfig.Store) *Service { return &Service{c, g} }
func (s *Service) store(k string) *instanceconfig.Store {
	if instanceconfig.GlobalKeys[k] && s.global != nil {
		return s.global
	}
	return s.config
}
func (s *Service) ListEnvVars(ctx context.Context, req *connect.Request[v1.ListEnvVarsRequest]) (*connect.Response[v1.ListEnvVarsResponse], error) {
	values := s.config.Snapshot()
	for k := range knownEnvVars {
		if !instanceconfig.GlobalKeys[k] && k != "DIALOGUE_PATH" && k != "ONEBOT_WS_PATH" && k != "ASTRBOT_WS_PATH" {
			if _, ok := values[k]; !ok {
				values[k] = ""
			}
		}
	}
	if s.global != nil {
		for k := range instanceconfig.GlobalKeys {
			values[k] = s.global.Get(k)
		}
	}
	vars := []*v1.EnvVar{}
	for k, v := range values {
		vars = append(vars, &v1.EnvVar{Key: k, Value: v, IsSecret: knownEnvVars[k].IsSecret})
	}
	return connect.NewResponse(&v1.ListEnvVarsResponse{EnvVars: vars}), nil
}
func (s *Service) UpdateEnvVar(ctx context.Context, req *connect.Request[v1.UpdateEnvVarRequest]) (*connect.Response[v1.UpdateEnvVarResponse], error) {
	err := s.store(req.Msg.Key).Update(req.Msg.Key, req.Msg.Value, false)
	res := &v1.UpdateEnvVarResponse{Success: err == nil}
	if err != nil {
		res.Error = err.Error()
	}
	return connect.NewResponse(res), nil
}
func (s *Service) DeleteEnvVar(ctx context.Context, req *connect.Request[v1.DeleteEnvVarRequest]) (*connect.Response[v1.DeleteEnvVarResponse], error) {
	err := s.store(req.Msg.Key).Update(req.Msg.Key, "", true)
	res := &v1.DeleteEnvVarResponse{Success: err == nil}
	if err != nil {
		res.Error = err.Error()
	}
	return connect.NewResponse(res), nil
}
func (s *Service) GetRawEnvFile(ctx context.Context, req *connect.Request[v1.GetRawEnvFileRequest]) (*connect.Response[v1.GetRawEnvFileResponse], error) {
	raw, err := s.config.Raw()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read env: %w", err))
	}
	return connect.NewResponse(&v1.GetRawEnvFileResponse{Content: raw}), nil
}
func (s *Service) UpdateRawEnvFile(ctx context.Context, req *connect.Request[v1.UpdateRawEnvFileRequest]) (*connect.Response[v1.UpdateRawEnvFileResponse], error) {
	err := s.config.Replace(req.Msg.Content)
	res := &v1.UpdateRawEnvFileResponse{Success: err == nil}
	if err != nil {
		res.Error = err.Error()
	}
	return connect.NewResponse(res), nil
}
