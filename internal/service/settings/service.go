package settings

import (
	"FrostAgent/internal/instanceconfig"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
)

// envEntry defines metadata for a known environment variable.
type envEntry struct {
	Description     string
	IsSecret        bool
	RequiresRestart bool
	AllowMultiline  bool
}

// knownEnvVars is the registry of all env keys the settings page manages.
var knownEnvVars = map[string]envEntry{
	"LISTEN_ADDR":                 {"HTTP 监听地址", false, true, false},
	"WS_LISTEN_ADDR":              {"WebSocket 监听地址", false, true, false},
	"HTTP_ALLOWED_ORIGINS":        {"管理面允许的跨域 Origin，多个值以英文逗号分隔", false, true, false},
	"WS_ALLOWED_ORIGINS":          {"允许的 WebSocket Origin", false, true, false},
	"SYSTEM_PROMPT":               {"系统提示词", false, false, true},
	"DIALOGUE_PATH":               {"示例对话 YAML 文件路径（用于少样本人设提示词引导）", false, true, false},
	"MAX_CONTEXT_MESSAGES":        {"最多保留的消息数", false, false, false},
	"MAX_CONTEXT_CHARS":           {"近似字符上限", false, false, false},
	"ENABLE_AT_IN_GROUP_MSG":      {"是否开启群聊回复前艾特", false, false, false},
	"GROUP_REPLY_ON_MENTION":      {"群聊被@或名称/别名提及时触发对话回复（false 则群聊消息不回复）", false, false, false},
	"BOT_NAME":                    {"机器人主名称，用于群聊文本唤醒", false, false, false},
	"BOT_ALIASES":                 {"机器人文本唤醒别名，多个名称以英文逗号分隔", false, false, false},
	"ADMIN_QQ_IDS":                {"允许使用管理员工具的 QQ 号，多个号码以英文逗号分隔", false, false, false},
	"ENABLE_REPLY_IN_GROUP_MSG":   {"群聊回复时是否引用原消息", false, false, false},
	"GROUP_COMPACT_BUFFER_SIZE":   {"群聊 running compact 每批原消息数量", false, true, false},
	"GROUP_COMPACT_MIN_INTERVAL":  {"同群 running compact 最小触发间隔（如 30s）", false, true, false},
	"GROUP_RAW_CONTEXT_MAX_CHARS": {"群聊未压缩原消息临时上下文的最大字符数（默认 12000）", false, false, false},
	"MEMORY_EXTRACT_BATCH_MIN":    {"自动记忆提取的最小累计轮数", false, false, false},
	"MEMORY_EXTRACT_BATCH_MAX":    {"自动记忆提取的最大累计轮数", false, false, false},
	"ENABLE_ONEBOT_ADAPTER":       {"是否启用 OneBot WebSocket 适配器", false, true, false},
	"ONEBOT_WS_PATH":              {"OneBot WebSocket 监听路径 (默认 /ws/frostagent)", false, true, false},
	"ENABLE_ASTRBOT_ADAPTER":      {"是否启用 AstrBot WebSocket 适配器", false, true, false},
	"ASTRBOT_WS_PATH":             {"AstrBot WebSocket 监听路径 (默认 /ws/astrbot)", false, true, false},
	"BILLING_ENABLED":             {"是否启用 Alcyone 计费", false, true, false},
	"ALCYONE_BASE_URL":            {"Alcyone 计费服务地址", false, true, false},
	"ALCYONE_SERVICE_TOKEN":       {"Alcyone 计费服务通信 Token", true, true, false},
	"ALCYONE_TIMEOUT":             {"计费请求超时时间", false, true, false},
	"BILLING_MAX_OUTPUT_TOKENS":   {"计费预扣款最大预留输出 Token", false, true, false},
	"BILLING_SAFETY_MULTIPLIER":   {"计费预扣款输入 Token 安全倍率", false, true, false},
	"MEMORY_REFLECTION_TIMEOUT":   {"记忆反思独立超时时间", false, true, false},
	"BRAIN_PATH":                  {"记忆存储 brain.json 路径", false, true, false},
	"UPSTREAM_API_KEY":            {"上游 API 认证密钥", true, true, false},
	"CODER_API_KEY":               {"Coder API 密钥", true, true, false},
}

// Service implements frostagent.v1.SettingsServiceHandler.
type Service struct {
	envPath        string
	config, global *instanceconfig.Store
	mu             sync.Mutex
}

func NewScoped(c, g *instanceconfig.Store) *Service { return &Service{config: c, global: g} }
func (s *Service) store(k string) *instanceconfig.Store {
	if instanceconfig.GlobalKeys[k] && s.global != nil {
		return s.global
	}
	return s.config
}

// New creates a new SettingsService and tightens permissions on existing .env.
// If tightening permissions on an existing file fails, New returns an error (fail-closed).
func New(envPath string) (*Service, error) {
	if envPath == "" {
		envPath = ".env"
	}
	s := &Service{envPath: envPath}
	if err := s.hardenPermissions(); err != nil {
		return nil, fmt.Errorf("harden .env permissions: %w", err)
	}
	return s, nil
}

func (s *Service) hardenPermissions() error {
	fi, err := os.Stat(s.envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory, not a regular file", s.envPath)
	}
	return os.Chmod(s.envPath, 0600)
}

// ListEnvVars returns all known env vars with their current values.
func (s *Service) ListEnvVars(
	ctx context.Context,
	req *connect.Request[v1.ListEnvVarsRequest],
) (*connect.Response[v1.ListEnvVarsResponse], error) {
	var vars []*v1.EnvVar
	for key, meta := range knownEnvVars {
		val := os.Getenv(key)
		if s.config != nil {
			if key == "DIALOGUE_PATH" || key == "BRAIN_PATH" || key == "ONEBOT_WS_PATH" || key == "ASTRBOT_WS_PATH" {
				continue
			}
			val = s.store(key).Get(key)
		}
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
	key := strings.TrimSpace(req.Msg.GetKey())
	value := req.Msg.GetValue()

	if key == "" {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   "key is required",
		}), nil
	}

	entry, ok := knownEnvVars[key]
	if !ok {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   fmt.Sprintf("key %q is not in the allowed environment variables list", key),
		}), nil
	}

	if strings.ContainsRune(value, 0) {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   "value contains null byte (NUL) which cannot be represented in environment",
		}), nil
	}

	if !entry.AllowMultiline && strings.ContainsAny(value, "\r\n") {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   fmt.Sprintf("key %q does not allow multiline values or newlines", key),
		}), nil
	}

	if s.config != nil {
		err := s.store(key).Update(key, value, false)
		res := &v1.UpdateEnvVarResponse{Success: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		return connect.NewResponse(res), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.atomicWriteEnv(key, value); err != nil {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	// Immediately set in-process so it takes effect for the current run.
	if err := os.Setenv(key, value); err != nil {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{
			Success: false,
			Error:   fmt.Sprintf("set in-process environment variable %q: %v", key, err),
		}), nil
	}

	return connect.NewResponse(&v1.UpdateEnvVarResponse{Success: true}), nil
}

// DeleteEnvVar removes a key from the .env file.
func (s *Service) DeleteEnvVar(
	ctx context.Context,
	req *connect.Request[v1.DeleteEnvVarRequest],
) (*connect.Response[v1.DeleteEnvVarResponse], error) {
	key := strings.TrimSpace(req.Msg.GetKey())
	if key == "" {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   "key is required",
		}), nil
	}

	if _, ok := knownEnvVars[key]; !ok {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   fmt.Sprintf("key %q is not in the allowed environment variables list", key),
		}), nil
	}

	if s.config != nil {
		err := s.store(key).Update(key, "", true)
		res := &v1.DeleteEnvVarResponse{Success: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		return connect.NewResponse(res), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.removeKeyFromEnv(key); err != nil {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	if err := os.Unsetenv(key); err != nil {
		return connect.NewResponse(&v1.DeleteEnvVarResponse{
			Success: false,
			Error:   fmt.Sprintf("unset in-process environment variable %q: %v", key, err),
		}), nil
	}

	return connect.NewResponse(&v1.DeleteEnvVarResponse{Success: true}), nil
}

// GetRawEnvFile returns the raw content of the .env file.
func (s *Service) GetRawEnvFile(
	ctx context.Context,
	req *connect.Request[v1.GetRawEnvFileRequest],
) (*connect.Response[v1.GetRawEnvFileResponse], error) {
	if s.config != nil {
		raw, err := s.config.Raw()
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&v1.GetRawEnvFileResponse{Content: raw}), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

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

	if s.config != nil {
		err := s.config.Replace(content)
		res := &v1.UpdateRawEnvFileResponse{Success: err == nil}
		if err != nil {
			res.Error = err.Error()
		}
		return connect.NewResponse(res), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := writeEnvAtomic(s.envPath, []byte(content)); err != nil {
		return connect.NewResponse(&v1.UpdateRawEnvFileResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}

	return connect.NewResponse(&v1.UpdateRawEnvFileResponse{Success: true}), nil
}

// formatEnvEntry shares the audited serialization with instance configuration stores.
var formatEnvEntry = instanceconfig.FormatEnvEntry

// atomicWriteEnv updates or appends a key=value line in the .env file atomically
// using statement-aware dotenv semantics.
func (s *Service) atomicWriteEnv(key, value string) error {
	content, err := os.ReadFile(s.envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .env: %w", err)
	}

	raw, err := instanceconfig.MutateEnv(string(content), key, value, false)
	if err != nil {
		return err
	}
	return writeEnvAtomic(s.envPath, []byte(raw))
}

// removeKeyFromEnv removes all occurrences of a key from the .env file atomically.
func (s *Service) removeKeyFromEnv(key string) error {
	content, err := os.ReadFile(s.envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read .env: %w", err)
	}

	raw, err := instanceconfig.MutateEnv(string(content), key, "", true)
	if err != nil {
		return err
	}
	return writeEnvAtomic(s.envPath, []byte(raw))
}

// commitEnvFile atomically renames tmpPath to targetPath.
// On POSIX systems, rename failure is treated as fatal to preserve atomic guarantees and
// avoid truncating the destination. On Windows development environments, best-effort
// copy is used if rename fails due to platform-specific file locking.
func commitEnvFile(tmpPath, targetPath string) error {
	if err := os.Rename(tmpPath, targetPath); err != nil {
		if runtime.GOOS == "windows" {
			if copyErr := copyFile(tmpPath, targetPath); copyErr != nil {
				return fmt.Errorf("rename .env: %w (windows fallback copy failed: %v)", err, copyErr)
			}
			return nil
		}
		return fmt.Errorf("atomic rename .env: %w", err)
	}
	return nil
}

// writeEnvAtomic writes data to a unique temp file in the same directory,
// enforces 0600 on the temp file prior to commit, and commits via atomic rename.
func writeEnvAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".env.tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	// Enforce 0600 on the temp file before writing and committing
	if err := tmpFile.Chmod(0600); err != nil {
		return fmt.Errorf("chmod temp file 0600: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	return commitEnvFile(tmpPath, path)
}

// copyFile copies a file from src to dst. Used only as a fallback on Windows development environments.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}
