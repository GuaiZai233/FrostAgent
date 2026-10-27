package settings

import (
	"context"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"maps"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/joho/godotenv"

	v1 "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1"
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
	"LISTEN_ADDR":                          {"HTTP 监听地址", false, true, false},
	"WS_LISTEN_ADDR":                       {"WebSocket 监听地址", false, true, false},
	"HTTP_ALLOWED_ORIGINS":                 {"管理面允许的跨域 Origin，多个值以英文逗号分隔", false, true, false},
	"WS_ALLOWED_ORIGINS":                   {"允许的 WebSocket Origin", false, true, false},
	"SECURITY_CONTROL_MODE":                {"全局安全控制模式", false, false, false},
	"SYSTEM_PROMPT":                        {"系统提示词", false, false, true},
	"MAX_CONTEXT_MESSAGES":                 {"最多保留的消息数", false, false, false},
	"ENABLE_AT_IN_GROUP_MSG":               {"是否开启群聊回复前艾特", false, false, false},
	"GROUP_REPLY_ON_MENTION":               {"群聊被@或名称/别名提及时触发对话回复（false 则群聊消息不回复）", false, false, false},
	"ENABLE_POKE":                          {"允许发送戳一戳行为", false, false, false},
	"POKE_CUSTOM_TEXT":                     {"一旦Bot被戳，发送给LLM的附加消息。", false, false, false},
	"POKE_COOLDOWN_SECONDS":                {"戳一戳冷却时间（秒），每个群/私聊独立，同群所有人共享；默认 30 秒", false, false, false},
	"BOT_NAME":                             {"机器人主名称，用于群聊文本唤醒", false, false, false},
	"BOT_ALIASES":                          {"机器人文本唤醒别名，多个名称以英文逗号分隔", false, false, false},
	"ADMIN_QQ_IDS":                         {"允许使用管理员工具的 QQ 号，多个号码以英文逗号分隔", false, false, false},
	"ADMIN_COMMAND_PREFIX":                 {"管理员消息指令前缀（默认 /，如 execute）", false, false, false},
	"ENABLE_RUNTIME_STATUS_COMMAND":        {"输出 FrostAgent 默认运行信息（#fa，默认开启）", false, false, false},
	"ENABLE_REPLY_IN_GROUP_MSG":            {"群聊回复时是否引用原消息", false, false, false},
	"ENABLE_QUOTE_TRACE":                   {"是否追溯收到消息的引用链（默认开启）", false, false, false},
	"QUOTE_TRACE_DEPTH":                    {"引用追溯深度（1~10，默认 5）", false, false, false},
	"BOT_LOOP_USER_IDS":                    {"管理员标记的 Bot 用户 ID，逗号分隔；可用 platform:ID 限定平台", false, false, false},
	"BOT_LOOP_MAX_TURNS":                   {"每个标记 Bot 在未标记用户明确唤醒之间的最多对话轮数", false, false, false},
	"BOT_LOOP_MODE":                        {"超限处理：hard 静默丢弃，soft 注入静默建议", false, false, false},
	"PROACTIVE_REPLY_PROBABILITY":          {"主动回复触发概率（0.01~1.00，0 为关闭）", false, false, false},
	"ENABLE_PROACTIVE_REPLY":               {"是否启用主动回复", false, false, false},
	"PROACTIVE_REPLY_GROUP_WHITELIST":      {"主动回复允许的群号白名单，多个群号以英文逗号分隔", false, false, false},
	"ENABLE_PROACTIVE_REPLY_WHITELIST":     {"是否启用主动回复群聊白名单模式", false, false, false},
	"STICKER_ACTIVITY":                     {"表情包使用积极度（0 禁用，1~10 对应每 50~5 个轮次）", false, false, false},
	"STICKER_ACTIVITY_MODE":                {"表情包积极度计数模式（bot_turns / all_messages）", false, false, false},
	"GROUP_COMPACT_BUFFER_SIZE":            {"群聊 running compact 每批原消息数量", false, true, false},
	"GROUP_COMPACT_MAX_BUFFER_SIZE":        {"群聊 running compact 最大缓冲消息数量", false, false, false},
	"GROUP_COMPACT_MIN_INTERVAL":           {"同群 running compact 最小触发间隔（如 30s）", false, true, false},
	"GROUP_RAW_CONTEXT_MAX_CHARS":          {"群聊未压缩原消息临时上下文的最大字符数（默认 12000）", false, false, false},
	"MEMORY_EXTRACT_BATCH_MIN":             {"自动记忆提取的最小累计轮数", false, false, false},
	"MEMORY_EXTRACT_BATCH_MAX":             {"自动记忆提取的最大累计轮数", false, false, false},
	"ONEBOT_ACTION_TIMEOUT":                {"OneBot API 动作确认超时时间（默认 10s）", false, false, false},
	"BILLING_ENABLED":                      {"是否启用 Alcyone 计费", false, true, false},
	"ALCYONE_BASE_URL":                     {"Alcyone 计费服务地址", false, true, false},
	"ALCYONE_SERVICE_TOKEN":                {"Alcyone 计费服务通信 Token", true, true, false},
	"ALCYONE_TIMEOUT":                      {"计费请求超时时间", false, true, false},
	"SANDBOX_ENABLED":                      {"是否启用隔离命令执行", false, false, false},
	"SANDBOX_BASE_URL":                     {"Sandbox Gateway 地址", false, true, false},
	"SANDBOX_AUTH_TOKEN":                   {"Sandbox Gateway 认证 Token", true, true, false},
	"SANDBOX_SESSION_NAMESPACE":            {"Sandbox 会话基础命名空间", false, true, false},
	"MCP_CONTROL_TOKEN":                    {"MCP 管理面认证 Token", true, true, false},
	"ADMIN_TOKEN":                          {"MCP 管理面兼容认证 Token", true, true, false},
	"MCP_ENFORCE_LOCAL_TOKEN":              {"本机 MCP 管理是否也强制 Token", false, true, false},
	"BILLING_MAX_OUTPUT_TOKENS":            {"计费预扣款最大预留输出 Token", false, true, false},
	"BILLING_SAFETY_MULTIPLIER":            {"计费预扣款输入 Token 安全倍率", false, true, false},
	"BILLING_PROMPT_PRICE_PER_MILLION":     {"每百万输入 Token 的计费价格", false, false, false},
	"BILLING_COMPLETION_PRICE_PER_MILLION": {"每百万输出 Token 的计费价格", false, false, false},
	"ENABLE_EMBEDDING":                     {"启用记忆向量检索", false, false, false},
	"MEMORY_REFLECTION_TIMEOUT":            {"记忆反思独立超时时间", false, true, false},
	"SECURITY_GATEWAY_TIMEOUT":             {"安全审查网关独立超时时间", false, true, false},
	"SECURITY_CLASSIFIER_TIMEOUT":          {"安全审查分类器独立超时时间", false, true, false},
	"AGENT_MAX_ITERATIONS":                 {"智能体单次处理允许的最大迭代轮数（默认 35）", false, true, false},
	"ACTIONSCAT_ENABLED":                   {"是否启用 ActionsCat（默认开启，关闭后隐藏全部工具并拒绝调用）", false, false, false},
	"ACTIONSCAT_ENDPOINT":                  {"ActionsCat 核心服务地址", false, true, false},
	"ACTIONSCAT_MANAGEMENT_TOKEN":          {"ActionsCat 管理 API 认证 Token", true, true, false},
	"ACTIONSCAT_DISPATCH_TOKEN":            {"ActionsCat 事件调度 Token（可选，默认复用管理 Token）", true, true, false},
}

// IsManagedKey reports whether a key already belongs to the structured
// application settings catalog rather than additional process environment.
func IsManagedKey(key string) bool {
	_, ok := knownEnvVars[strings.ToUpper(key)]
	return ok
}

// ExportableValueKeys lists classified non-secret setting values. Unknown
// keys remain blank in setting.json until explicitly classified.
func ExportableValueKeys() map[string]bool {
	keys := make(map[string]bool)
	for key, entry := range knownEnvVars {
		if !entry.IsSecret && key != "DIALOGUE_PATH" && key != "BRAIN_PATH" &&
			key != "ONEBOT_WS_PATH" && key != "ASTRBOT_WS_PATH" {
			keys[key] = true
		}
	}
	return keys
}

// SandboxManager propagates atomic Control Plane sandbox configuration updates.
type SandboxManager interface {
	ApplySnapshot(cfg sandbox.Config)
	SetEnabled(enabled bool)
	RefreshEnabled(load func() bool)
	Get() sandbox.Config
}

var (
	processInitialEnv     map[string]string
	processInitialEnvOnce sync.Once
)

func init() {
	CaptureInitialEnv()
}

// CaptureInitialEnv snapshots environment values before a dotenv file is loaded.
func CaptureInitialEnv() {
	processInitialEnvOnce.Do(func() {
		processInitialEnv = make(map[string]string)
		for _, env := range os.Environ() {
			if key, value, ok := strings.Cut(env, "="); ok {
				processInitialEnv[key] = value
			}
		}
	})
}

// Service implements frostagent.v1.SettingsServiceHandler.
type Service struct {
	storageBackend    string
	validateEmbedding func(context.Context) error
	envPath           string
	config, global    *instanceconfig.Store
	sandboxManager    SandboxManager
	externalOverrides map[string]string
	mu                sync.Mutex
}

func NewScoped(c, g *instanceconfig.Store, sandboxManager SandboxManager) *Service {
	return &Service{config: c, global: g, sandboxManager: sandboxManager}
}

func (s *Service) SetEmbeddingStorage(backend string, validate func(context.Context) error) {
	s.storageBackend = backend
	s.validateEmbedding = validate
}

func (s *Service) validateEmbeddingUpdate(ctx context.Context, key, value string) error {
	if key == "ENABLE_EMBEDDING" && value == "true" && s.validateEmbedding != nil {
		return s.validateEmbedding(ctx)
	}
	return nil
}

// SetSandboxConfigManager wires the shared sandbox configuration manager.
func (s *Service) SetSandboxConfigManager(manager SandboxManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxManager = manager
}

// SetExternalOverrideForTest explicitly registers a process-level override.
func (s *Service) SetExternalOverrideForTest(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.externalOverrides == nil {
		s.externalOverrides = make(map[string]string)
	}
	s.externalOverrides[key] = value
}

func (s *Service) store(k string) *instanceconfig.Store {
	if instanceconfig.GlobalKeys[k] && s.global != nil {
		return s.global
	}
	if instanceconfig.SharedKeys[k] && s.config == nil && s.global != nil {
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
	s := &Service{envPath: envPath, externalOverrides: make(map[string]string)}
	if err := s.hardenPermissions(); err != nil {
		return nil, fmt.Errorf("harden .env permissions: %w", err)
	}
	s.initExternalOverrides()
	return s, nil
}

func (s *Service) initExternalOverrides() {
	var fileEnv map[string]string
	if data, err := os.ReadFile(s.envPath); err == nil {
		fileEnv, _ = godotenv.Unmarshal(string(data))
	}
	maps.Copy(s.externalOverrides, processInitialEnv)
	for _, env := range os.Environ() {
		if key, value, ok := strings.Cut(env, "="); ok {
			fileValue, inFile := fileEnv[key]
			if !inFile || value != fileValue {
				s.externalOverrides[key] = value
			}
		}
	}
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
		inherited := false
		if s.config != nil {
			if key == "DIALOGUE_PATH" || key == "BRAIN_PATH" || key == "ONEBOT_WS_PATH" || key == "ASTRBOT_WS_PATH" {
				continue
			}
			targetStore := s.store(key)
			if targetStore != nil {
				val = targetStore.Get(key)
			}
			if instanceconfig.SharedKeys[key] && strings.TrimSpace(val) == "" && s.global != nil {
				val = s.global.Get(key)
				inherited = strings.TrimSpace(val) != ""
			}
		} else if s.global != nil {
			val = s.global.Get(key)
		}
		vars = append(vars, &v1.EnvVar{
			Key:         key,
			Value:       val,
			IsSecret:    meta.IsSecret,
			ResetHint:   s.resetHint(key),
			IsInherited: inherited,
		})
	}
	return connect.NewResponse(&v1.ListEnvVarsResponse{EnvVars: vars, StorageBackend: s.storageBackend}), nil
}

// UpdateEnvVar updates a single env var in the .env file.
func (s *Service) UpdateEnvVar(
	ctx context.Context,
	req *connect.Request[v1.UpdateEnvVarRequest],
) (*connect.Response[v1.UpdateEnvVarResponse], error) {
	key := strings.TrimSpace(req.Msg.GetKey())
	value := req.Msg.GetValue()

	if err := validateUpdate(key, value); err != nil {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{Error: err.Error()}), nil
	}
	if err := s.validateEmbeddingUpdate(ctx, key, value); err != nil {
		return connect.NewResponse(&v1.UpdateEnvVarResponse{Error: err.Error()}), nil
	}

	if s.config != nil || s.global != nil {
		targetStore := s.store(key)
		if targetStore != nil {
			err := targetStore.Update(key, value, false)
			res := &v1.UpdateEnvVarResponse{Success: err == nil}
			if err != nil {
				res.Error = err.Error()
			} else if key == "SANDBOX_ENABLED" && s.sandboxManager != nil {
				s.sandboxManager.RefreshEnabled(func() bool {
					return sandbox.ParseBool(s.global.Get("SANDBOX_ENABLED"), false)
				})
			}
			return connect.NewResponse(res), nil
		}
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
	delete(s.externalOverrides, key)
	if key == "SANDBOX_ENABLED" && s.sandboxManager != nil {
		s.sandboxManager.SetEnabled(sandbox.ParseBool(value, false))
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

	if s.config != nil || s.global != nil {
		targetStore := s.store(key)
		if targetStore != nil {
			err := targetStore.Update(key, "", true)
			res := &v1.DeleteEnvVarResponse{Success: err == nil}
			if err != nil {
				res.Error = err.Error()
			} else if key == "SANDBOX_ENABLED" && s.sandboxManager != nil {
				s.sandboxManager.RefreshEnabled(func() bool {
					return sandbox.ParseBool(s.global.Get("SANDBOX_ENABLED"), false)
				})
			}
			return connect.NewResponse(res), nil
		}
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
	delete(s.externalOverrides, key)
	if key == "SANDBOX_ENABLED" && s.sandboxManager != nil {
		s.sandboxManager.SetEnabled(false)
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

// UpdateRawEnvFile overwrites the selected environment file.
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
	newEnv, err := godotenv.Unmarshal(content)
	if err != nil {
		return connect.NewResponse(&v1.UpdateRawEnvFileResponse{
			Success: false,
			Error:   fmt.Sprintf("parse .env content: %v", err),
		}), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldEnv map[string]string
	if oldData, readErr := os.ReadFile(s.envPath); readErr == nil {
		oldEnv, _ = godotenv.Unmarshal(string(oldData))
	}

	if err := writeEnvAtomic(s.envPath, []byte(content)); err != nil {
		return connect.NewResponse(&v1.UpdateRawEnvFileResponse{
			Success: false,
			Error:   err.Error(),
		}), nil
	}
	for key := range oldEnv {
		if _, exists := newEnv[key]; !exists {
			if _, overridden := s.externalOverrides[key]; !overridden {
				_ = os.Unsetenv(key)
			}
		}
	}
	for key, value := range newEnv {
		if _, overridden := s.externalOverrides[key]; !overridden {
			_ = os.Setenv(key, value)
		}
	}
	if s.sandboxManager != nil {
		s.sandboxManager.ApplySnapshot(s.computeEffectiveSandboxConfig(newEnv))
	}

	return connect.NewResponse(&v1.UpdateRawEnvFileResponse{Success: true}), nil
}

func (s *Service) computeEffectiveSandboxConfig(newEnv map[string]string) sandbox.Config {
	effective := make(map[string]string)
	for _, key := range []string{
		"SANDBOX_ENABLED",
		"SANDBOX_BASE_URL",
		"SANDBOX_AUTH_TOKEN",
		"SANDBOX_SESSION_NAMESPACE",
	} {
		if value, overridden := s.externalOverrides[key]; overridden {
			effective[key] = value
		} else if value, exists := newEnv[key]; exists {
			effective[key] = value
		} else {
			effective[key] = os.Getenv(key)
		}
	}
	return sandbox.LoadConfigFromMap(effective)
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

// UpdateEnvVars validates the whole draft before writing. The instance gateway
// applies the resulting runtime and listener changes once after this RPC.
func (s *Service) UpdateEnvVars(ctx context.Context, req *connect.Request[v1.UpdateEnvVarsRequest]) (*connect.Response[v1.UpdateEnvVarsResponse], error) {
	type change struct {
		key, value, previous string
		clear, existed       bool
		store                *instanceconfig.Store
	}
	changes := make([]change, 0, len(req.Msg.Updates)+len(req.Msg.ClearKeys))
	seen := make(map[string]bool)
	failure := func(err error) (*connect.Response[v1.UpdateEnvVarsResponse], error) {
		return connect.NewResponse(&v1.UpdateEnvVarsResponse{Error: err.Error()}), nil
	}
	if len(req.Msg.Updates)+len(req.Msg.ClearKeys) > len(knownEnvVars) {
		return failure(errors.New("too many setting changes"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	add := func(key, value string, clear bool) error {
		key = strings.TrimSpace(key)
		if _, ok := knownEnvVars[key]; !ok {
			return fmt.Errorf("key %q is not in the allowed environment variables list", key)
		}
		if seen[key] {
			return fmt.Errorf("duplicate setting %q", key)
		}
		if !clear {
			if err := validateUpdate(key, value); err != nil {
				return err
			}
			if err := s.validateEmbeddingUpdate(ctx, key, value); err != nil {
				return err
			}
		}
		store := s.store(key)
		if store == nil {
			return errors.New("batch updates require scoped settings")
		}
		previous, existed := store.Snapshot()[key]
		changes = append(changes, change{key: key, value: value, clear: clear, store: store, previous: previous, existed: existed})
		seen[key] = true
		return nil
	}
	for _, update := range req.Msg.Updates {
		if err := add(update.GetKey(), update.GetValue(), false); err != nil {
			return failure(err)
		}
	}
	for _, key := range req.Msg.ClearKeys {
		if err := add(key, "", true); err != nil {
			return failure(err)
		}
	}
	databaseChanges := make([]instanceconfig.DatabaseChange, 0, len(changes))
	hasDatabase := false
	for _, change := range changes {
		hasDatabase = hasDatabase || change.store.IsDatabase()
		databaseChanges = append(databaseChanges, instanceconfig.DatabaseChange{Store: change.store, Key: change.key, Value: change.value, Clear: change.clear})
	}
	if hasDatabase {
		if err := instanceconfig.ApplyDatabaseChanges(ctx, databaseChanges); err != nil {
			return failure(err)
		}
	} else {
		for index, change := range changes {
			err := ctx.Err()
			if err == nil {
				err = change.store.Update(change.key, change.value, change.clear)
			}
			if err != nil {
				for _, previous := range changes[:index] {
					err = errors.Join(err, previous.store.Update(previous.key, previous.previous, !previous.existed))
				}
				return failure(err)
			}
		}
	}
	if seen["SANDBOX_ENABLED"] && s.sandboxManager != nil {
		s.sandboxManager.RefreshEnabled(func() bool { return sandbox.ParseBool(s.store("SANDBOX_ENABLED").Get("SANDBOX_ENABLED"), false) })
	}
	return connect.NewResponse(&v1.UpdateEnvVarsResponse{Success: true}), nil
}

func validateUpdate(key, value string) error {
	if key == "" {
		return errors.New("key is required")
	}

	entry, ok := knownEnvVars[key]
	if !ok {
		return fmt.Errorf("key %q is not in the allowed environment variables list", key)
	}
	if key == "ENABLE_EMBEDDING" && value != "true" && value != "false" {
		return errors.New("启用 Embedding 必须为 true 或 false")
	}

	if key == "ENABLE_POKE" && value != "" && value != "true" && value != "false" {
		return errors.New("允许发送戳一戳行为必须为 true 或 false")
	}
	if key == "ENABLE_RUNTIME_STATUS_COMMAND" && value != "" && value != "true" && value != "false" {
		return errors.New("输出 FrostAgent 默认运行信息必须为 true 或 false")
	}
	if key == "POKE_COOLDOWN_SECONDS" && value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 0 || seconds > 86400 || strconv.Itoa(seconds) != value {
			return errors.New("戳一戳冷却时间必须是 0 到 86400 的整数秒")
		}
	}
	if key == "MAX_CONTEXT_MESSAGES" {
		count, err := strconv.Atoi(value)
		if err != nil || count < 4 {
			return errors.New("保留消息条数必须是不小于 4 的整数")
		}
	}
	if key == "QUOTE_TRACE_DEPTH" {
		depth, err := strconv.Atoi(value)
		if err != nil || depth < 1 || depth > 10 || strconv.Itoa(depth) != value {
			return errors.New("引用追溯深度必须是 1 到 10 的整数")
		}
	}
	if key == "STICKER_ACTIVITY" {
		level, err := strconv.Atoi(value)
		if err != nil || level < 0 || level > 10 || strconv.Itoa(level) != value {
			return errors.New("表情包使用积极度必须是 0 到 10 的整数")
		}
	}

	if key == "STICKER_ACTIVITY_MODE" && value != "bot_turns" && value != "all_messages" {
		return errors.New("表情包积极度计数模式必须是 bot_turns 或 all_messages")
	}
	if value != "" {
		switch key {
		case "BILLING_PROMPT_PRICE_PER_MILLION", "BILLING_COMPLETION_PRICE_PER_MILLION":
			price, err := strconv.ParseInt(value, 10, 64)
			if err != nil || price < 0 {
				return errors.New("每百万 Token 价格必须是非负整数，100 单位＝1 雪花＝0.01 元")
			}
		case "BILLING_SAFETY_MULTIPLIER":
			multiplier, err := strconv.ParseFloat(value, 64)
			if err != nil || multiplier <= 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
				return errors.New("输入 Token 安全倍率必须是大于 0 的有限数值")
			}
		}
	}

	if strings.ContainsRune(value, 0) {
		return errors.New("value contains null byte (NUL) which cannot be represented in environment")
	}

	if !entry.AllowMultiline && strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("key %q does not allow multiline values or newlines", key)
	}

	return nil
}
