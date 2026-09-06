package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/tools"
)

type fakeSandboxBackend struct {
	mu          sync.Mutex
	calls       []sandbox.ExecRequest
	execFunc    func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error)
	releaseFunc func(ctx context.Context, sessionID string) error
	healthFunc  func(ctx context.Context) error
}

func (f *fakeSandboxBackend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()

	if f.execFunc != nil {
		return f.execFunc(ctx, req)
	}
	zero := 0
	return sandbox.ExecResult{
		Stdout:   "default output",
		ExitCode: &zero,
		Duration: 10 * time.Millisecond,
	}, nil
}

func (f *fakeSandboxBackend) Release(ctx context.Context, sessionID string) error {
	if f.releaseFunc != nil {
		return f.releaseFunc(ctx, sessionID)
	}
	return nil
}

func (f *fakeSandboxBackend) Health(ctx context.Context) error {
	if f.healthFunc != nil {
		return f.healthFunc(ctx)
	}
	return nil
}

func (f *fakeSandboxBackend) getCalls() []sandbox.ExecRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]sandbox.ExecRequest, len(f.calls))
	copy(copied, f.calls)
	return copied
}

func TestExecuteCommandTool_SchemaAndDefaults(t *testing.T) {
	fake := &fakeSandboxBackend{}
	tool := tools.ExecuteCommandTool(fake)

	// 1. Tool schema
	if tool.Name() != "execute_command" {
		t.Fatalf("expected tool name execute_command, got %s", tool.Name())
	}
	params := tool.Parameters()
	if params == nil || params["type"] != "object" {
		t.Fatalf("expected object parameters schema, got %+v", params)
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "command" {
		t.Fatalf("expected required: ['command'], got %+v", params["required"])
	}

	// 2. Command required (empty command)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "sess-test"})
	_, err := tool.ExecuteContext(ctx, `{"command": ""}`)
	if err == nil {
		t.Fatalf("expected error on empty command")
	}

	_, err = tool.ExecuteContext(ctx, `{}`)
	if err == nil {
		t.Fatalf("expected error on missing command")
	}

	// 3 & 4. Defaults: cwd=/sandbox, timeout=30
	resStr, err := tool.ExecuteContext(ctx, `{"command": "whoami"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := fake.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call to backend, got %d", len(calls))
	}
	if calls[0].Cwd != "/sandbox" {
		t.Errorf("expected default cwd /sandbox, got %s", calls[0].Cwd)
	}
	if calls[0].Timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", calls[0].Timeout)
	}

	// 14. Tool result is valid JSON
	var parsed tools.CommandToolOutput
	if err := json.Unmarshal([]byte(resStr), &parsed); err != nil {
		t.Fatalf("failed to unmarshal tool result JSON: %v", err)
	}
}

func TestExecuteCommandTool_TimeoutValidation(t *testing.T) {
	fake := &fakeSandboxBackend{}
	tool := tools.ExecuteCommandTool(fake)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "sess-test"})

	// 5. timeout <= 0 rejected
	_, err := tool.ExecuteContext(ctx, `{"command": "ls", "timeout": 0}`)
	if err == nil {
		t.Fatalf("expected error on timeout = 0")
	}
	_, err = tool.ExecuteContext(ctx, `{"command": "ls", "timeout": -5}`)
	if err == nil {
		t.Fatalf("expected error on timeout < 0")
	}

	// 6. timeout > 120 rejected
	_, err = tool.ExecuteContext(ctx, `{"command": "ls", "timeout": 121}`)
	if err == nil {
		t.Fatalf("expected error on timeout > 120")
	}

	// valid timeout within (0, 120]
	_, err = tool.ExecuteContext(ctx, `{"command": "ls", "timeout": 120}`)
	if err != nil {
		t.Fatalf("expected timeout 120 to succeed, got %v", err)
	}
}

func TestExecuteCommandTool_SessionIsolationAndContext(t *testing.T) {
	fake := &fakeSandboxBackend{}
	tool := tools.ExecuteCommandTool(fake)

	// 7. No RunContext -> fail closed
	_, err := tool.ExecuteContext(context.Background(), `{"command": "pwd"}`)
	if err == nil {
		t.Fatalf("expected fail closed when context has no RunContext")
	}

	// 8. RunContext present but SessionID empty -> fail closed
	ctxEmptySession := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: ""})
	_, err = tool.ExecuteContext(ctxEmptySession, `{"command": "pwd"}`)
	if err == nil {
		t.Fatalf("expected fail closed when SessionID is empty")
	}

	// 9. SessionID passed correctly to backend
	ctxValid := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "group:alpha:12345"})
	_, err = tool.ExecuteContext(ctxValid, `{"command": "pwd"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := fake.getCalls()
	if len(calls) == 0 || calls[len(calls)-1].SessionID != "group:alpha:12345" {
		t.Fatalf("expected SessionID to reach backend, got %+v", calls)
	}
}

func TestExecuteCommandTool_FailureAndTimeoutSemantics(t *testing.T) {
	// 10. HTTP/backend infrastructure error does NOT fallback to host
	infraErrFake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{}, errors.New("gateway connection refused")
		},
	}
	toolInfra := tools.ExecuteCommandTool(infraErrFake)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "sess-1"})
	_, err := toolInfra.ExecuteContext(ctx, `{"command": "ls"}`)
	if err == nil || !strings.Contains(err.Error(), "gateway connection refused") {
		t.Fatalf("expected infrastructure error to be returned, got %v", err)
	}

	// 11. Command exit_code != 0 returns valid tool result with nil Go error
	code7 := 7
	cmdFailFake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{
				Stdout:   "",
				Stderr:   "fatal: not a git repository",
				ExitCode: &code7,
				Duration: 50 * time.Millisecond,
			}, nil
		},
	}
	toolFail := tools.ExecuteCommandTool(cmdFailFake)
	resStr, err := toolFail.ExecuteContext(ctx, `{"command": "git status"}`)
	if err != nil {
		t.Fatalf("expected nil error on command exit 7, got %v", err)
	}
	var resObj tools.CommandToolOutput
	if err := json.Unmarshal([]byte(resStr), &resObj); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if resObj.ExitCode == nil || *resObj.ExitCode != 7 {
		t.Errorf("expected exit_code 7, got %v", resObj.ExitCode)
	}
	if resObj.Stderr != "fatal: not a git repository" {
		t.Errorf("expected stderr preserved, got %s", resObj.Stderr)
	}

	// 12. timed_out=true correctly returned
	timeoutFake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{
				Stdout:   "working...",
				Stderr:   "",
				ExitCode: nil,
				TimedOut: true,
				Duration: 30 * time.Second,
			}, nil
		},
	}
	toolTimeout := tools.ExecuteCommandTool(timeoutFake)
	timeResStr, err := toolTimeout.ExecuteContext(ctx, `{"command": "sleep 100"}`)
	if err != nil {
		t.Fatalf("expected nil error on timeout, got %v", err)
	}
	var timeResObj tools.CommandToolOutput
	if err := json.Unmarshal([]byte(timeResStr), &timeResObj); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if !timeResObj.TimedOut {
		t.Errorf("expected TimedOut true, got false")
	}
	if timeResObj.ExitCode != nil {
		t.Errorf("expected nil exit_code on timeout, got %v", *timeResObj.ExitCode)
	}

	// 13. stdout/stderr separated
	bothFake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			zero := 0
			return sandbox.ExecResult{
				Stdout:   "standard output",
				Stderr:   "standard error message",
				ExitCode: &zero,
				Duration: 10 * time.Millisecond,
			}, nil
		},
	}
	toolBoth := tools.ExecuteCommandTool(bothFake)
	bothResStr, err := toolBoth.ExecuteContext(ctx, `{"command": "both"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var bothResObj tools.CommandToolOutput
	if err := json.Unmarshal([]byte(bothResStr), &bothResObj); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if bothResObj.Stdout != "standard output" || bothResObj.Stderr != "standard error message" {
		t.Errorf("stdout/stderr mismatch: %+v", bothResObj)
	}
}

func TestExecuteCommandTool_LargeOutputTruncationAndJSONSafety(t *testing.T) {
	// 15. Large output: 100 KiB stdout, 100 KiB stderr
	// Must produce valid JSON strictly below 64 KiB without breaking JSON syntax.
	bigStdout := "HEAD_STDOUT_" + strings.Repeat("A", 100*1024) + "_TAIL_STDOUT"
	bigStderr := "HEAD_STDERR_" + strings.Repeat("B", 100*1024) + "_TAIL_STDERR_ERROR_HERE"
	zero := 0

	fake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{
				Stdout:          bigStdout,
				Stderr:          bigStderr,
				ExitCode:        &zero,
				StdoutTruncated: false,
				StderrTruncated: false,
				Duration:        100 * time.Millisecond,
			}, nil
		},
	}

	tool := tools.ExecuteCommandTool(fake)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "sess-big"})
	resStr, err := tool.ExecuteContext(ctx, `{"command": "generate_huge_log"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check total byte length is safely below FrostAgent's MaxToolOutputBytes (64 KiB = 65536)
	if len(resStr) >= llm.MaxToolOutputBytes {
		t.Fatalf("tool result size (%d bytes) must be less than MaxToolOutputBytes (%d)", len(resStr), llm.MaxToolOutputBytes)
	}

	// Must be valid JSON
	var parsed tools.CommandToolOutput
	if err := json.Unmarshal([]byte(resStr), &parsed); err != nil {
		t.Fatalf("failed to parse truncated output JSON: %v\nResult: %s", err, resStr)
	}

	// Verify FrostAgent truncation flags
	if !parsed.FrostAgentStdoutTruncated {
		t.Errorf("expected FrostAgentStdoutTruncated to be true")
	}
	if !parsed.FrostAgentStderrTruncated {
		t.Errorf("expected FrostAgentStderrTruncated to be true")
	}

	// Verify head and tail sections are preserved
	if !strings.HasPrefix(parsed.Stdout, "HEAD_STDOUT_") {
		t.Errorf("stdout head was lost: %s", parsed.Stdout[:50])
	}
	if !strings.HasSuffix(parsed.Stdout, "_TAIL_STDOUT") {
		t.Errorf("stdout tail was lost: %s", parsed.Stdout[len(parsed.Stdout)-50:])
	}
	if !strings.Contains(parsed.Stdout, "...[FrostAgent output truncated]...") {
		t.Errorf("stdout missing truncation marker")
	}

	if !strings.HasPrefix(parsed.Stderr, "HEAD_STDERR_") {
		t.Errorf("stderr head was lost: %s", parsed.Stderr[:50])
	}
	if !strings.HasSuffix(parsed.Stderr, "_TAIL_STDERR_ERROR_HERE") {
		t.Errorf("stderr tail was lost: %s", parsed.Stderr[len(parsed.Stderr)-50:])
	}
	if !strings.Contains(parsed.Stderr, "...[FrostAgent output truncated]...") {
		t.Errorf("stderr missing truncation marker")
	}
}

func TestExecuteCommandTool_EscapeHeavyOutputTruncationAndJSONSafety(t *testing.T) {
	// Regression test for Finding 3: escape-heavy output (control bytes \x00, \x1f, quotes, backslashes, <, >, &)
	// which expand up to 6x during json.Marshal.
	// Output must strictly stay below MaxToolOutputBytes (64 KiB) and remain 100% valid JSON.
	headStdout := "ESCAPE_HEAD_<>&_\"\\"
	tailStdout := "_ESCAPE_TAIL_<>&"
	escapeStdout := headStdout + strings.Repeat("\x00\"\\<>&", 20*1024) + tailStdout

	headStderr := "STDERR_HEAD_\x01\x1f"
	tailStderr := "_STDERR_TAIL_ERROR"
	escapeStderr := headStderr + strings.Repeat("\x01\x1f\"\\", 25*1024) + tailStderr
	zero := 0

	fake := &fakeSandboxBackend{
		execFunc: func(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{
				Stdout:          escapeStdout,
				Stderr:          escapeStderr,
				ExitCode:        &zero,
				StdoutTruncated: false,
				StderrTruncated: false,
				Duration:        120 * time.Millisecond,
			}, nil
		},
	}

	tool := tools.ExecuteCommandTool(fake)
	ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "sess-escape"})
	resStr, err := tool.ExecuteContext(ctx, `{"command": "echo escape_heavy"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result MUST be strictly below FrostAgent's MaxToolOutputBytes (64 KiB = 65536)
	if len(resStr) >= llm.MaxToolOutputBytes {
		t.Fatalf("escape-heavy tool result size (%d bytes) exceeded MaxToolOutputBytes (%d)", len(resStr), llm.MaxToolOutputBytes)
	}

	// Result MUST be valid JSON
	var parsed tools.CommandToolOutput
	if err := json.Unmarshal([]byte(resStr), &parsed); err != nil {
		t.Fatalf("failed to parse escape-heavy output JSON: %v\nResult: %s", err, resStr)
	}

	if !parsed.FrostAgentStdoutTruncated {
		t.Errorf("expected FrostAgentStdoutTruncated to be true for escape-heavy stdout")
	}
	if !parsed.FrostAgentStderrTruncated {
		t.Errorf("expected FrostAgentStderrTruncated to be true for escape-heavy stderr")
	}

	if !strings.HasPrefix(parsed.Stdout, headStdout) {
		t.Errorf("stdout head was lost: %s", parsed.Stdout[:50])
	}
	if !strings.HasSuffix(parsed.Stdout, tailStdout) {
		t.Errorf("stdout tail was lost: %s", parsed.Stdout[len(parsed.Stdout)-50:])
	}
	if !strings.Contains(parsed.Stdout, "...[FrostAgent output truncated]...") {
		t.Errorf("stdout missing truncation marker")
	}

	if !strings.HasPrefix(parsed.Stderr, headStderr) {
		t.Errorf("stderr head was lost")
	}
	if !strings.HasSuffix(parsed.Stderr, tailStderr) {
		t.Errorf("stderr tail was lost")
	}
	if !strings.Contains(parsed.Stderr, "...[FrostAgent output truncated]...") {
		t.Errorf("stderr missing truncation marker")
	}
}

func TestExecuteCommandTool_ConcurrentSessionIsolation(t *testing.T) {
	// 16. Concurrent session A/B do not mix SessionID
	fake := &fakeSandboxBackend{}
	tool := tools.ExecuteCommandTool(fake)

	var wg sync.WaitGroup
	concurrentCount := 30
	errChan := make(chan error, concurrentCount)

	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)
		sessionID := fmt.Sprintf("session-%03d", i)
		go func(sessID string) {
			defer wg.Done()
			ctx := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: sessID})
			cmd := fmt.Sprintf("echo for %s", sessID)
			res, err := tool.ExecuteContext(ctx, fmt.Sprintf(`{"command": %q}`, cmd))
			if err != nil {
				errChan <- fmt.Errorf("session %s failed: %w", sessID, err)
				return
			}
			if !strings.Contains(res, "default output") {
				errChan <- fmt.Errorf("unexpected output for %s: %s", sessID, res)
			}
		}(sessionID)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatalf("concurrent execution error: %v", err)
	}

	calls := fake.getCalls()
	if len(calls) != concurrentCount {
		t.Fatalf("expected %d calls, got %d", concurrentCount, len(calls))
	}

	seenSessions := make(map[string]bool)
	for _, call := range calls {
		expectedCmd := fmt.Sprintf("echo for %s", call.SessionID)
		if call.Command != expectedCmd {
			t.Errorf("SessionID / Command mismatch: session=%s, command=%s", call.SessionID, call.Command)
		}
		seenSessions[call.SessionID] = true
	}

	if len(seenSessions) != concurrentCount {
		t.Errorf("expected %d unique sessions, got %d", concurrentCount, len(seenSessions))
	}
}
