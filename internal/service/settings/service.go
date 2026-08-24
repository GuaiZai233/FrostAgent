package settings

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
)

// envEntry defines metadata for a known environment variable.
type envEntry struct {
	Description     string
	IsSecret        bool
	RequiresRestart bool
}

// knownEnvVars is the registry of all env keys the settings page manages.
var knownEnvVars = map[string]envEntry{
	"UPSTREAM_ENDPOINT":           {"上游 API 端点 URL，支持 OpenAI 兼容服务", false, true},
	"UPSTREAM_API_KEY":            {"上游 API 认证密钥", true, true},
	"CODER_API_KEY":               {"Coder API 密钥", true, true},
	"ADMIN_API_TOKEN":             {"远程管理面访问 Token（非回环监听时必填）", true, true},
	"WS_ACCESS_TOKEN":             {"远程 WebSocket 访问 Token（非回环监听时必填）", true, true},
	"LISTEN_ADDR":                 {"HTTP 监听地址", false, true},
	"WS_LISTEN_ADDR":              {"WebSocket 监听地址", false, true},
	"HTTP_ALLOWED_ORIGINS":        {"管理面允许的跨域 Origin，多个值以英文逗号分隔", false, true},
	"SYSTEM_PROMPT":               {"系统提示词", false, false},
	"DIALOGUE_PATH":               {"示例对话 YAML 文件路径（用于少样本人设提示词引导）", false, true},
	"MODEL_NAME":                  {"模型名称", false, true},
	"VISUAL_MODEL_NAME":           {"视觉模型名称（留空则采用默认模型）", false, true},
	"MAX_CONTEXT_MESSAGES":        {"最多保留的消息数", false, false},
	"MAX_CONTEXT_CHARS":           {"近似字符上限", false, false},
	"WS_ALLOWED_ORIGINS":          {"允许的 WebSocket Origin", false, true},
	"ENABLE_AT_IN_GROUP_MSG":      {"是否开启群聊回复前艾特", false, false},
	"GROUP_REPLY_ON_MENTION":      {"群聊被@或名称/别名提及时触发对话回复（false 则群聊消息不回复）", false, false},
	"BOT_NAME":                    {"机器人主名称，用于群聊文本唤醒", false, false},
	"BOT_ALIASES":                 {"机器人文本唤醒别名，多个名称以英文逗号分隔", false, false},
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

// Service implements frostagent.v1.SettingsServiceHandler.
type Service struct {
	envPath string // path to .env file
}

// New creates a new SettingsService.
func New(envPath string) *Service {
	if envPath == "" {
		envPath = ".env"
	}
	return &Service{envPath: envPath}
}

// ListEnvVars returns all known env vars with their current values.
func (s *Service) ListEnvVars(
	ctx context.Context,
	req *connect.Request[v1.ListEnvVarsRequest],
) (*connect.Response[v1.ListEnvVarsResponse], error) {
	var vars []*v1.EnvVar
	for key, meta := range knownEnvVars {
		val := os.Getenv(key)
		vars = append(vars, &v1.EnvVar{
			Key:      key,
			Value:    val,
			IsSecret: meta.IsSecret,
		})
	}
	return connect.NewResponse(&v1.ListEnvVarsResponse{EnvVars: vars}), nil
}

// UpdateEnvVar updates a single env var in the .env file.
func (s *Service) UpdateEnvVar(
	ctx context.Context,
	req *connect.Request[v1.UpdateEnvVarRequest],
) (*connect.Response[v1.UpdateEnvVarResponse], error) {
	key := req.Msg.GetKey()
	value := req.Msg.GetValue()

	if key == "" {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   "key is required",
		}), nil
	}

	if err := s.atomicWriteEnv(key, value); err != nil {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	// Immediately set in-process so it takes effect for the current run.
	os.Setenv(key, value)

	return connect.NewResponse(&v1.UpdateEnvVarResponse{Success: true}), nil
}

// DeleteEnvVar removes a key from the .env file.
func (s *Service) DeleteEnvVar(
	ctx context.Context,
	req *connect.Request[v1.DeleteEnvVarRequest],
) (*connect.Response[v1.DeleteEnvVarResponse], error) {
	key := req.Msg.GetKey()
	if key == "" {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   "key is required",
		}), nil
	}

	if err := s.removeKeyFromEnv(key); err != nil {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	os.Unsetenv(key)

	return connect.NewResponse(&v1.DeleteEnvVarResponse{Success: true}), nil
}

// GetRawEnvFile returns the raw content of the .env file.
func (s *Service) GetRawEnvFile(
	ctx context.Context,
	req *connect.Request[v1.GetRawEnvFileRequest],
) (*connect.Response[v1.GetRawEnvFileResponse], error) {
	data, err := os.ReadFile(s.envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return connect.NewResponse(&v1.GetRawEnvFileResponse{Content: ""}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read .env: %w", err))
	}
	return connect.NewResponse(&v1.GetRawEnvFileResponse{Content: string(data)}), nil
}

// UpdateRawEnvFile overwrites the .env file with the given content.
func (s *Service) UpdateRawEnvFile(
	ctx context.Context,
	req *connect.Request[v1.UpdateRawEnvFileRequest],
) (*connect.Response[v1.UpdateRawEnvFileResponse], error) {
	content := req.Msg.GetContent()

	tmpPath := s.envPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return connect.NewResponse(&v1.UpdateRawEnvFileResponse{
			Success: false,
			Error:   fmt.Sprintf("write temp file: %v", err),
		}), nil
	}

	if err := os.Rename(tmpPath, s.envPath); err != nil {
		// Fallback: cross-device rename, copy instead
		if err := copyFile(tmpPath, s.envPath); err != nil {
			return connect.NewResponse(&v1.UpdateRawEnvFileResponse{
				Success: false,
				Error:   fmt.Sprintf("rename .env: %v", err),
			}), nil
		}
		os.Remove(tmpPath)
	}

	return connect.NewResponse(&v1.UpdateRawEnvFileResponse{Success: true}), nil
}

// atomicWriteEnv updates or appends a key=value line in the .env file atomically.
func (s *Service) atomicWriteEnv(key, value string) error {
	lines, err := readEnvLines(s.envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .env: %w", err)
	}

	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}

	return writeEnvAtomic(s.envPath, lines)
}

// removeKeyFromEnv removes a key from the .env file atomically.
func (s *Service) removeKeyFromEnv(key string) error {
	lines, err := readEnvLines(s.envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read .env: %w", err)
	}

	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+"=") || trimmed == key {
			continue
		}
		filtered = append(filtered, line)
	}

	return writeEnvAtomic(s.envPath, filtered)
}

// readEnvLines reads all lines from a file.
func readEnvLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// writeEnvAtomic writes lines to a temp file then renames.
func writeEnvAtomic(path string, lines []string) error {
	tmpPath := path + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}

	for _, line := range lines {
		fmt.Fprintln(f, line)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Cross-device fallback
		if err := copyFile(tmpPath, path); err != nil {
			return fmt.Errorf("rename .env: %w", err)
		}
		os.Remove(tmpPath)
	}
	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
