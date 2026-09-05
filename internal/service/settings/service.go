package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/joho/godotenv"

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
	envPath string
	mu      sync.Mutex
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

// formatEnvEntry formats key=value for .env with secure dotenv-compatible serialization
// guaranteed to round-trip correctly with godotenv v1.5.1.
//
// godotenv v1.5.1 has known parser quirks:
// 1. In double quotes, closing quote checks `src[i-1] == '\\'` without backslash parity,
//    causing any value ending in backslash (e.g. C:\temp\) to fail with "unterminated quoted value".
// 2. Trailing quote trimming in double quotes strips escaped quotes (\").
//
// To guarantee round-trip fidelity:
// - Simple values (no newlines, no $, no leading/trailing space, no leading quote, no inline comments)
//   are formatted unquoted (key=value), which natively preserves backslashes and quotes without escaping pitfalls.
// - Values starting with a quote (without single quotes or newlines, not ending in \) are single-quoted (key='value').
// - Values requiring quotes (empty string, leading/trailing space, $, newlines) are double-quoted with
//   proper escape sequences (\, \n, \r, \", \$).
//
// Before returning, godotenv.Unmarshal is executed on the formatted line. If the value cannot
// be safely parsed back to its exact original form, an error is returned to prevent persisting corrupt values.
func formatEnvEntry(key, value string) (string, error) {
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("value contains null byte (NUL) which cannot be represented in environment")
	}

	hasLeadingOrTrailingSpace := strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") ||
		strings.HasSuffix(value, " ") || strings.HasSuffix(value, "\t")
	hasNewline := strings.ContainsAny(value, "\r\n")
	hasDollar := strings.ContainsRune(value, '$')
	startsQuote := strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'")
	hasInlineComment := strings.Contains(value, " #") || strings.Contains(value, "\t#")

	canBeUnquoted := value != "" && !hasNewline && !hasLeadingOrTrailingSpace && !hasDollar && !startsQuote && !hasInlineComment
	canBeSingleQuoted := value != "" && !hasNewline && !strings.ContainsRune(value, '\'') && !strings.HasSuffix(value, "\\")

	var formatted string
	if canBeUnquoted {
		formatted = key + "=" + value
	} else if canBeSingleQuoted && startsQuote {
		formatted = key + "='" + value + "'"
	} else {
		var b strings.Builder
		b.WriteString(key)
		b.WriteString(`="`)
		for _, r := range value {
			switch r {
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '"':
				b.WriteString(`\"`)
			case '$':
				b.WriteString(`\$`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		formatted = b.String()
	}

	// Pre-write verification against godotenv parser
	parsed, err := godotenv.Unmarshal(formatted)
	if err != nil {
		return "", fmt.Errorf("value cannot be safely represented in .env: %w", err)
	}
	if parsedVal, ok := parsed[key]; !ok || parsedVal != value {
		return "", fmt.Errorf("value cannot be safely round-tripped in .env (got %q, want %q)", parsed[key], value)
	}
	if len(parsed) != 1 {
		return "", fmt.Errorf("value causes unexpected additional keys in .env: %v", parsed)
	}

	return formatted, nil
}

// envStatement represents a single statement in a .env file.
type envStatement struct {
	raw   string
	isKey bool
	key   string
}

// parseEnvStatements parses raw .env content into statements, handling comments, blank lines,
// 'export KEY=value', 'KEY: value', and quoted multiline values.
func parseEnvStatements(content string) []envStatement {
	var stmts []envStatement
	idx := 0
	n := len(content)

	for idx < n {
		start := idx

		lineEnd := strings.IndexByte(content[idx:], '\n')
		var nextIdx int
		var firstLine string
		if lineEnd == -1 {
			firstLine = content[idx:]
			nextIdx = n
		} else {
			nextIdx = idx + lineEnd + 1
			firstLine = content[idx:nextIdx]
		}

		trimmed := strings.TrimLeft(firstLine, " \t")
		if trimmed == "" || trimmed == "\n" || trimmed == "\r\n" || strings.HasPrefix(trimmed, "#") {
			stmts = append(stmts, envStatement{
				raw:   content[start:nextIdx],
				isKey: false,
			})
			idx = nextIdx
			continue
		}

		lineText := trimmed
		if strings.HasPrefix(lineText, "export ") || strings.HasPrefix(lineText, "export\t") {
			lineText = strings.TrimLeft(lineText[6:], " \t")
		}

		sepIdx := -1
		eqIdx := strings.IndexByte(lineText, '=')
		colonIdx := strings.IndexByte(lineText, ':')
		if eqIdx != -1 && colonIdx != -1 {
			sepIdx = min(eqIdx, colonIdx)
		} else if eqIdx != -1 {
			sepIdx = eqIdx
		} else {
			sepIdx = colonIdx
		}

		if sepIdx == -1 {
			stmts = append(stmts, envStatement{
				raw:   content[start:nextIdx],
				isKey: false,
			})
			idx = nextIdx
			continue
		}

		keyName := strings.TrimSpace(lineText[:sepIdx])
		if keyName == "" {
			stmts = append(stmts, envStatement{
				raw:   content[start:nextIdx],
				isKey: false,
			})
			idx = nextIdx
			continue
		}

		sepChar := lineText[sepIdx]
		firstLineSepPos := strings.IndexByte(firstLine, sepChar)
		valPart := firstLine[firstLineSepPos+1:]
		trimmedValPart := strings.TrimLeft(valPart, " \t")
		valOffset := firstLineSepPos + 1 + (len(valPart) - len(trimmedValPart))
		valAbsStart := start + valOffset

		if valAbsStart < nextIdx && (content[valAbsStart] == '"' || content[valAbsStart] == '\'') {
			quote := content[valAbsStart]
			pos := valAbsStart + 1
			closingQuotePos := -1
			for pos < n {
				if content[pos] == quote {
					bsCount := 0
					for k := pos - 1; k >= valAbsStart && content[k] == '\\'; k-- {
						bsCount++
					}
					if bsCount%2 == 0 {
						closingQuotePos = pos
						break
					}
				}
				pos++
			}

			if closingQuotePos != -1 {
				afterQuoteNewline := strings.IndexByte(content[closingQuotePos:], '\n')
				if afterQuoteNewline == -1 {
					nextIdx = n
				} else {
					nextIdx = closingQuotePos + afterQuoteNewline + 1
				}
			} else {
				nextIdx = n
			}
		}

		stmts = append(stmts, envStatement{
			raw:   content[start:nextIdx],
			isKey: true,
			key:   keyName,
		})
		idx = nextIdx
	}

	return stmts
}

// mutateEnvStatements updates the first occurrence of key with formatted and eliminates
// any duplicate occurrences of the same key. If key was not present, it is appended.
func mutateEnvStatements(stmts []envStatement, key, formatted string) []envStatement {
	found := false
	var result []envStatement
	for _, stmt := range stmts {
		if stmt.isKey && stmt.key == key {
			if !found {
				result = append(result, envStatement{
					raw:   formatted + "\n",
					isKey: true,
					key:   key,
				})
				found = true
			}
			continue
		}
		result = append(result, stmt)
	}
	if !found {
		result = append(result, envStatement{
			raw:   formatted + "\n",
			isKey: true,
			key:   key,
		})
	}
	return result
}

// removeKeyFromStatements removes all occurrences of key from stmts.
func removeKeyFromStatements(stmts []envStatement, key string) []envStatement {
	var result []envStatement
	for _, stmt := range stmts {
		if stmt.isKey && stmt.key == key {
			continue
		}
		result = append(result, stmt)
	}
	return result
}

// renderEnvStatements renders statements into bytes, ensuring each intermediate statement
// ends with a newline so consecutive statements do not collide.
func renderEnvStatements(stmts []envStatement) []byte {
	var b strings.Builder
	for i, s := range stmts {
		b.WriteString(s.raw)
		if i < len(stmts)-1 && !strings.HasSuffix(s.raw, "\n") {
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

// atomicWriteEnv updates or appends a key=value line in the .env file atomically
// using statement-aware dotenv semantics.
func (s *Service) atomicWriteEnv(key, value string) error {
	content, err := os.ReadFile(s.envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .env: %w", err)
	}

	formatted, err := formatEnvEntry(key, value)
	if err != nil {
		return err
	}

	stmts := parseEnvStatements(string(content))
	stmts = mutateEnvStatements(stmts, key, formatted)

	return writeEnvAtomic(s.envPath, renderEnvStatements(stmts))
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

	stmts := parseEnvStatements(string(content))
	stmts = removeKeyFromStatements(stmts, key)

	return writeEnvAtomic(s.envPath, renderEnvStatements(stmts))
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
