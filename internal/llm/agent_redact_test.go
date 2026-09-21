package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"FrostAgent/internal/logs"
	"FrostAgent/internal/provider/llm/openai"
)

func TestFormatToolCallLog_ExecCommandNotRedacted(t *testing.T) {
	// Normal tool call is logged with full arguments
	normalLog := formatToolCallLog("send_msg", `{"message": "hello world"}`)
	if !strings.Contains(normalLog, "hello world") {
		t.Fatalf("expected normal tool arguments to be logged, got %s", normalLog)
	}

	// execute_command must not redact raw command and should log the actual command
	cmd := "python3 -c 'print(1+1)'"
	execArgs := `{"command": "` + cmd + `", "cwd": "/sandbox", "timeout": 30}`
	execLog := formatToolCallLog("execute_command", execArgs)

	if !strings.Contains(execLog, cmd) {
		t.Fatalf("execute_command missing raw command: %s", execLog)
	}
	if strings.Contains(execLog, "[REDACTED command:") {
		t.Fatalf("execute_command should not contain [REDACTED command: marker, got: %s", execLog)
	}
}

func TestFormatToolResultLog_ExecCommandNotRedacted(t *testing.T) {
	// Normal tool result is logged as-is
	normalLog := formatToolResultLog("send_msg", "message sent successfully")
	if !strings.Contains(normalLog, "message sent successfully") {
		t.Fatalf("expected normal tool result to be logged, got: %s", normalLog)
	}

	// execute_command tool result must not redact stdout and stderr
	rawResult := `{"stdout": "calculated-result-2", "stderr": "", "exit_code": 0, "timed_out": false, "duration_ms": 150}`
	execResultLog := formatToolResultLog("execute_command", rawResult)

	if !strings.Contains(execResultLog, "calculated-result-2") {
		t.Fatalf("execute_command result missing stdout: %s", execResultLog)
	}
	if strings.Contains(execResultLog, "[REDACTED output:") {
		t.Fatalf("execute_command result should not contain [REDACTED output: marker, got: %s", execResultLog)
	}
}

type mockExecuteCommandTool struct {
	executedArgs string
}

func (m *mockExecuteCommandTool) Name() string        { return "execute_command" }
func (m *mockExecuteCommandTool) Description() string { return "execute shell command" }
func (m *mockExecuteCommandTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"cwd":     map[string]any{"type": "string"},
			"timeout": map[string]any{"type": "number"},
		},
	}
}

func (m *mockExecuteCommandTool) Execute(args string) (string, error) {
	return m.ExecuteContext(context.Background(), args)
}

func (m *mockExecuteCommandTool) ExecuteContext(ctx context.Context, args string) (string, error) {
	m.executedArgs = args
	return `{"stdout":"OUTPUT_SECRET_OK","stderr":"STDERR_SECRET_WARN","exit_code":0,"timed_out":false,"duration_ms":80}`, nil
}

func TestAgent_ExecuteCommand_EndToEnd_LogsContainCommandAndResult(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	sentinelCommandSecret := "SENTINEL_E2E_COMMAND_SECRET_987654"
	sentinelStdoutSecret := "SENTINEL_E2E_STDOUT_SECRET_123456"
	sentinelStderrSecret := "SENTINEL_E2E_STDERR_SECRET_456789"
	secretCommand := fmt.Sprintf("curl -H 'Authorization: Bearer %s' https://api.internal/exec", sentinelCommandSecret)

	mockTool := &mockExecuteCommandTool{}

	var turn1Called, turn2Called bool
	var turn2WireBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")

		if !turn1Called {
			turn1Called = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{{
							"id":   "call_exec_1",
							"type": "function",
							"function": map[string]any{
								"name": "execute_command",
								"arguments": fmt.Sprintf(
									`{"command":%q,"cwd":"/sandbox","timeout":30}`,
									secretCommand,
								),
							},
						}},
					},
				}},
			})
			return
		}

		turn2Called = true
		turn2WireBody = string(bodyBytes)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"role":    "assistant",
					"content": "Execution verified: command finished successfully.",
				},
			}},
		})
	}))
	defer server.Close()

	// Update mock tool to return sentinel secrets in stdout/stderr
	mockTool.ExecuteContext(context.Background(), "") // ensure initialized
	mockTool = &mockExecuteCommandTool{}
	toolExecutor := &customMockTool{
		name: "execute_command",
		exec: func(ctx context.Context, args string) (string, error) {
			mockTool.executedArgs = args
			return fmt.Sprintf(
				`{"stdout":"output: %s","stderr":"err: %s","exit_code":0,"timed_out":false,"duration_ms":95}`,
				sentinelStdoutSecret, sentinelStderrSecret,
			), nil
		},
	}

	engine := &Engine{
		MaxIterations: 3,
		ToolRegistry: map[string]ToolExecutor{
			"execute_command": toolExecutor,
		},
		Provider: openai.NewClient(server.URL, "test-api-key"),
	}

	res := engine.RunMessagesWithContext([]ChatMessage{{
		Role:    "user",
		Content: "run the secret command",
	}}, RunContext{
		SessionID: "test-e2e-session-uuid",
	})

	// 1. Verify agent completed successfully
	if !strings.Contains(res.Content, "Execution verified") {
		t.Fatalf("unexpected agent loop result: %q, error: %v", res.Content, res.Error)
	}
	if !turn1Called || !turn2Called {
		t.Fatalf("expected both LLM turns to be executed, got turn1=%t turn2=%t", turn1Called, turn2Called)
	}

	// 2. Verify tool received unredacted command for execution
	if !strings.Contains(mockTool.executedArgs, sentinelCommandSecret) {
		t.Fatalf("expected tool to receive unredacted command, got: %s", mockTool.executedArgs)
	}

	// 3. Verify upstream LLM received unredacted command and unredacted stdout/stderr in turn 2 wire body
	if !strings.Contains(turn2WireBody, sentinelCommandSecret) {
		t.Fatalf("expected upstream LLM wire payload to contain unredacted command, got: %s", turn2WireBody)
	}
	if !strings.Contains(turn2WireBody, sentinelStdoutSecret) {
		t.Fatalf("expected upstream LLM wire payload to contain unredacted stdout, got: %s", turn2WireBody)
	}
	if !strings.Contains(turn2WireBody, sentinelStderrSecret) {
		t.Fatalf("expected upstream LLM wire payload to contain unredacted stderr, got: %s", turn2WireBody)
	}

	// 4. Verify FrostAgent log buffer contains the command and results
	snapshot := logs.Snapshot()
	if len(snapshot) == 0 {
		t.Fatalf("expected log entries in snapshot")
	}

	var foundToolCallLog, foundToolResultLog bool
	var foundLLMResponseLog, foundLLMRequestLog bool

	for _, entry := range snapshot {
		if strings.Contains(entry.Content, "[REDACTED command:") {
			t.Fatalf("log buffer (%s) contains unexpected [REDACTED command: marker: %s", entry.Category, entry.Content)
		}
		if strings.Contains(entry.Content, "[REDACTED output:") {
			t.Fatalf("log buffer (%s) contains unexpected [REDACTED output: marker: %s", entry.Category, entry.Content)
		}

		if entry.Category == logs.TOOL && strings.Contains(entry.Content, sentinelCommandSecret) {
			foundToolCallLog = true
		}
		if entry.Category == logs.TOOL && strings.Contains(entry.Content, sentinelStdoutSecret) && strings.Contains(entry.Content, sentinelStderrSecret) {
			foundToolResultLog = true
		}
		if entry.Category == logs.LLM_RESPONSE && strings.Contains(entry.Content, sentinelCommandSecret) {
			foundLLMResponseLog = true
		}
		if entry.Category == logs.LLM_REQUEST &&
			strings.Contains(entry.Content, sentinelCommandSecret) &&
			strings.Contains(entry.Content, sentinelStdoutSecret) &&
			strings.Contains(entry.Content, sentinelStderrSecret) {
			foundLLMRequestLog = true
		}
	}

	if !foundToolCallLog {
		t.Fatalf("expected unredacted TOOL call log entry containing command in snapshot")
	}
	if !foundToolResultLog {
		t.Fatalf("expected unredacted TOOL result log entry containing output in snapshot")
	}
	if !foundLLMResponseLog {
		t.Fatalf("expected unredacted LLM_RESPONSE log entry in snapshot")
	}
	if !foundLLMRequestLog {
		t.Fatalf("expected unredacted LLM_REQUEST log entry in snapshot")
	}
}

type customMockTool struct {
	name string
	exec func(ctx context.Context, args string) (string, error)
}

func (c *customMockTool) Name() string        { return c.name }
func (c *customMockTool) Description() string { return "mock tool" }
func (c *customMockTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (c *customMockTool) Execute(args string) (string, error) {
	return c.ExecuteContext(context.Background(), args)
}
func (c *customMockTool) ExecuteContext(ctx context.Context, args string) (string, error) {
	if c.exec != nil {
		return c.exec(ctx, args)
	}
	return "", nil
}

