package settings

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	"UPSTREAM_ENDPOINT":      {"上游 API 端点 URL，支持 OpenAI 兼容服务", false, true},
	"UPSTREAM_API_KEY":       {"上游 API 认证密钥", true, true},
	"CODER_API_KEY":          {"Coder API 密钥", true, true},
	"LISTEN_ADDR":            {"HTTP 监听地址", false, true},
	"WS_LISTEN_ADDR":         {"WebSocket 监听地址", false, true},
	"SYSTEM_PROMPT":          {"系统提示词", false, false},
	"MODEL_NAME":             {"模型名称", false, true},
	"VISUAL_MODEL_NAME":      {"视觉模型名称（留空则采用默认模型）", false, true},
	"MAX_CONTEXT_MESSAGES":   {"最多保留的消息数", false, false},
	"MAX_CONTEXT_CHARS":      {"近似字符上限", false, false},
	"WS_ALLOWED_ORIGINS":     {"允许的 WebSocket Origin", false, true},
	"ENABLE_AT_IN_GROUP_MSG": {"是否开启群聊回复前艾特", false, false},
}

// maskSecret hides all but the last 4 characters of a value.
func maskSecret(v string) string {
	if len(v) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(v)-4) + v[len(v)-4:]
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
		displayVal := val
		if meta.IsSecret && val != "" {
			displayVal = maskSecret(val)
		}
		vars = append(vars, &v1.EnvVar{
			Key:      key,
			Value:    displayVal,
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
	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
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

	f, err := os.Create(tmpPath)
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
	return os.WriteFile(dst, data, 0644)
}

// Ensure path/filepath is used.
var _ = filepath.Base
