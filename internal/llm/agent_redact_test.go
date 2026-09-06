package llm

import (
	"strings"
	"testing"
)

func TestFormatToolCallLog_Redaction(t *testing.T) {
	// Normal tool call is logged with full arguments
	normalLog := formatToolCallLog("send_msg", `{"message": "hello world"}`)
	if !strings.Contains(normalLog, "hello world") {
		t.Fatalf("expected normal tool arguments to be logged, got %s", normalLog)
	}

	// execute_command must redact raw command and never log authorization tokens or command text
	secretCmd := "curl -H 'Authorization: Bearer secret-api-token-12345' https://api.internal/data"
	execArgs := `{"command": "` + secretCmd + `", "cwd": "/sandbox", "timeout": 30}`
	execLog := formatToolCallLog("execute_command", execArgs)

	if strings.Contains(execLog, secretCmd) {
		t.Fatalf("execute_command logged raw command: %s", execLog)
	}
	if strings.Contains(execLog, "secret-api-token-12345") {
		t.Fatalf("execute_command logged secret token: %s", execLog)
	}
	if !strings.Contains(execLog, "[REDACTED command:") {
		t.Fatalf("execute_command missing [REDACTED command: marker, got: %s", execLog)
	}
	if !strings.Contains(execLog, "sha256_prefix=") {
		t.Fatalf("execute_command missing sha256_prefix, got: %s", execLog)
	}
	if !strings.Contains(execLog, "len=") {
		t.Fatalf("execute_command missing len, got: %s", execLog)
	}
}

func TestFormatToolResultLog_Redaction(t *testing.T) {
	// Normal tool result is logged as-is
	normalLog := formatToolResultLog("send_msg", "message sent successfully")
	if !strings.Contains(normalLog, "message sent successfully") {
		t.Fatalf("expected normal tool result to be logged, got: %s", normalLog)
	}

	// execute_command tool result must redact stdout and stderr
	rawResult := `{"stdout": "super-secret-output-data", "stderr": "fatal: leaked key in error", "exit_code": 0, "timed_out": false, "duration_ms": 150}`
	execResultLog := formatToolResultLog("execute_command", rawResult)

	if strings.Contains(execResultLog, "super-secret-output-data") {
		t.Fatalf("execute_command result leaked stdout: %s", execResultLog)
	}
	if strings.Contains(execResultLog, "leaked key in error") {
		t.Fatalf("execute_command result leaked stderr: %s", execResultLog)
	}
	if !strings.Contains(execResultLog, "[REDACTED output:") {
		t.Fatalf("execute_command result missing REDACTED marker: %s", execResultLog)
	}
	if !strings.Contains(execResultLog, "exit_code=0") {
		t.Fatalf("execute_command result missing exit_code metadata: %s", execResultLog)
	}
	if !strings.Contains(execResultLog, "stdout_len=") {
		t.Fatalf("execute_command result missing stdout_len metadata: %s", execResultLog)
	}
}
