package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/sandbox"
)

const (
	// executeCommandMaxStreamEncodedBytes is the maximum JSON-encoded byte size
	// allocated to stdout or stderr after JSON escaping. 24 KiB each ensures
	// encoded stdout (<=24 KiB) + encoded stderr (<=24 KiB) + metadata strictly
	// stays under agent.go's MaxToolOutputBytes (64 KiB), preventing generic
	// truncation from corrupting structured JSON even under worst-case control
	// character expansion (e.g. \x00 -> ) or HTML escaping (<, >, &).
	executeCommandMaxStreamEncodedBytes = 24 * 1024
	truncationMarker                    = "\n...[FrostAgent output truncated]...\n"
)

// CommandToolOutput is the structured JSON returned to the LLM.
type CommandToolOutput struct {
	Stdout                    string `json:"stdout"`
	Stderr                    string `json:"stderr"`
	ExitCode                  *int   `json:"exit_code"`
	TimedOut                  bool   `json:"timed_out"`
	StdoutTruncated           bool   `json:"stdout_truncated"`
	StderrTruncated           bool   `json:"stderr_truncated"`
	FrostAgentStdoutTruncated bool   `json:"frostagent_stdout_truncated,omitempty"`
	FrostAgentStderrTruncated bool   `json:"frostagent_stderr_truncated,omitempty"`
	DurationMs                int64  `json:"duration_ms"`
}

// ExecuteCommandTool creates a Tool that executes shell commands inside an
// isolated SandboxBackend runtime. It requires a request-local SessionID
// and fails closed without falling back to the host machine.
func ExecuteCommandTool(backend sandbox.Backend) Tool {
	return Tool{
		name: "execute_command",
		description: "在隔离的 sandbox runtime 中执行 shell 命令。文件系统在当前 FrostAgent 会话内保持状态，" +
			"但每次调用都是新的 shell 进程。命令不会在 FrostAgent 宿主机上执行。",
		parameter: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "要在隔离 sandbox 中执行的 shell 命令",
				},
				"cwd": map[string]any{
					"type":        "string",
					"description": "sandbox 内工作目录，默认 /sandbox",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "超时秒数，默认 30，最大 120",
				},
			},
			"required": []string{"command"},
		},
		executeContext: func(ctx context.Context, args string) (string, error) {
			if backend == nil {
				return "", errors.New("sandbox backend is not configured or unavailable")
			}

			runCtx, ok := llm.RunContextFromContext(ctx)
			if !ok || strings.TrimSpace(runCtx.SessionID) == "" {
				return "", errors.New("execute_command requires an active request-local SessionID; host execution is strictly forbidden")
			}

			var params struct {
				Command string   `json:"command"`
				Cwd     string   `json:"cwd"`
				Timeout *float64 `json:"timeout"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("参数解析失败: %w", err)
			}

			if strings.TrimSpace(params.Command) == "" {
				return "", errors.New("command 参数不能为空")
			}
			command := params.Command

			cwd := params.Cwd
			if strings.TrimSpace(cwd) == "" {
				cwd = "/sandbox"
			}

			timeoutSec := 30.0
			if params.Timeout != nil {
				timeoutSec = *params.Timeout
			}

			if timeoutSec <= 0 {
				return "", errors.New("timeout 必须大于 0 秒")
			}
			if timeoutSec > 120 {
				return "", errors.New("timeout 不能超过 120 秒")
			}

			timeout := time.Duration(timeoutSec * float64(time.Second))

			req := sandbox.ExecRequest{
				SessionID: runCtx.SessionID,
				Command:   command,
				Cwd:       cwd,
				Timeout:   timeout,
			}

			result, err := backend.Exec(ctx, req)
			if err != nil {
				return "", fmt.Errorf("sandbox 执行失败: %w", err)
			}

			stdout, stdoutTrunc := boundStream(result.Stdout, executeCommandMaxStreamEncodedBytes)
			stderr, stderrTrunc := boundStream(result.Stderr, executeCommandMaxStreamEncodedBytes)

			toolOutput := CommandToolOutput{
				Stdout:                    stdout,
				Stderr:                    stderr,
				ExitCode:                  result.ExitCode,
				TimedOut:                  result.TimedOut,
				StdoutTruncated:           result.StdoutTruncated,
				StderrTruncated:           result.StderrTruncated,
				FrostAgentStdoutTruncated: stdoutTrunc,
				FrostAgentStderrTruncated: stderrTrunc,
				DurationMs:                result.Duration.Milliseconds(),
			}

			jsonBytes, err := json.Marshal(toolOutput)
			if err != nil {
				return "", fmt.Errorf("序列化工具输出失败: %w", err)
			}

			return string(jsonBytes), nil
		},
	}
}

// boundStream ensures stream output, when JSON-encoded, does not exceed
// maxEncodedBytes. If truncation is needed, it keeps both head and tail sections
// separated by a marker to retain critical compiler/runtime error diagnostics
// at the tail, while guaranteeing that the marshaled JSON will never exceed
// the budget regardless of escape expansion (e.g. control chars or HTML escaping).
func boundStream(content string, maxEncodedBytes int) (string, bool) {
	if jsonEncodedContentLen(content) <= maxEncodedBytes {
		return content, false
	}

	markerLen := jsonEncodedContentLen(truncationMarker)
	budget := maxEncodedBytes - markerLen
	if budget <= 0 {
		return truncationMarker, true
	}

	halfBudget := budget / 2
	head := findPrefixUnderBudget(content, halfBudget)
	tail := findSuffixUnderBudget(content, halfBudget)

	return head + truncationMarker + tail, true
}

func jsonEncodedContentLen(s string) int {
	b, err := json.Marshal(s)
	if err != nil {
		return len(s) * 6
	}
	if len(b) >= 2 {
		return len(b) - 2
	}
	return len(b)
}

func findPrefixUnderBudget(content string, maxEncodedBudget int) string {
	if maxEncodedBudget <= 0 {
		return ""
	}
	low := 0
	high := len(content)
	bestCut := 0

	for low <= high {
		mid := (low + high) / 2
		cut := mid
		for cut > 0 && !utf8.RuneStart(content[cut]) {
			cut--
		}
		prefix := content[:cut]
		if jsonEncodedContentLen(prefix) <= maxEncodedBudget {
			bestCut = cut
			low = mid + 1
		} else {
			high = cut - 1
		}
	}
	return content[:bestCut]
}

func findSuffixUnderBudget(content string, maxEncodedBudget int) string {
	if maxEncodedBudget <= 0 {
		return ""
	}
	low := 0
	high := len(content)
	bestStart := len(content)

	for low <= high {
		mid := (low + high) / 2
		start := mid
		for start < len(content) && !utf8.RuneStart(content[start]) {
			start++
		}
		suffix := content[start:]
		if jsonEncodedContentLen(suffix) <= maxEncodedBudget {
			bestStart = start
			high = mid - 1
		} else {
			low = start + 1
		}
	}
	return content[bestStart:]
}
