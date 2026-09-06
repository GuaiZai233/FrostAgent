package codeinterpreter_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/llm"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/sandbox/codeinterpreter"
	"FrostAgent/internal/tools"
)

func TestRealGatewayIntegration(t *testing.T) {
	authToken := strings.TrimSpace(os.Getenv("REAL_SANDBOX_AUTH_TOKEN"))
	if authToken == "" {
		t.Skip("skipping real gateway integration test because REAL_SANDBOX_AUTH_TOKEN is not set")
	}

	baseURL := os.Getenv("REAL_SANDBOX_BASE_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:13874"
	}

	cfg := sandbox.Config{
		Enabled:          true,
		BaseURL:          baseURL,
		AuthToken:        authToken,
		SessionNamespace: "frostagent-e2e",
		ClientTimeout:    135 * time.Second,
	}

	client := codeinterpreter.New(cfg)

	// Step 0: Health check
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("real gateway health check failed: %v", err)
	}

	tool := tools.ExecuteCommandTool(client)

	sessionA := "e2e:session:A"
	sessionB := "e2e:session:B"

	// Step 1: echo hello
	ctxA := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: sessionA})
	res1Str, err := tool.ExecuteContext(ctxA, `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("step 1 echo hello failed: %v", err)
	}
	if !strings.Contains(res1Str, `"stdout":"hello\n"`) && !strings.Contains(res1Str, `"stdout":"hello"`) {
		t.Fatalf("step 1 expected stdout hello, got: %s", res1Str)
	}
	if !strings.Contains(res1Str, `"exit_code":0`) {
		t.Fatalf("step 1 expected exit_code 0, got: %s", res1Str)
	}

	// Step 2: In session A: echo persistent > /sandbox/test.txt
	_, err = tool.ExecuteContext(ctxA, `{"command": "echo persistent > /sandbox/test.txt"}`)
	if err != nil {
		t.Fatalf("step 2 write persistent file failed: %v", err)
	}

	// In session A: cat /sandbox/test.txt
	res2Str, err := tool.ExecuteContext(ctxA, `{"command": "cat /sandbox/test.txt"}`)
	if err != nil {
		t.Fatalf("step 2 cat persistent file failed: %v", err)
	}
	if !strings.Contains(res2Str, "persistent") {
		t.Fatalf("step 2 expected persistent content in session A, got: %s", res2Str)
	}

	// Step 3: In session B: verify test -e /sandbox/test.txt fails (file should NOT exist)
	ctxB := llm.WithRunContext(context.Background(), llm.RunContext{SessionID: sessionB})
	res3Str, err := tool.ExecuteContext(ctxB, `{"command": "test -e /sandbox/test.txt"}`)
	if err != nil {
		t.Fatalf("step 3 test -e should return tool result with exit_code != 0, got Go err: %v", err)
	}
	if strings.Contains(res3Str, `"exit_code":0`) {
		t.Fatalf("step 3 session B should NOT see session A's file, but got exit_code 0: %s", res3Str)
	}

	// Step 4: exit 7 -> exit_code=7, err == nil
	res4Str, err := tool.ExecuteContext(ctxA, `{"command": "exit 7"}`)
	if err != nil {
		t.Fatalf("step 4 exit 7 returned unexpected Go error: %v", err)
	}
	if !strings.Contains(res4Str, `"exit_code":7`) {
		t.Fatalf("step 4 expected exit_code 7, got: %s", res4Str)
	}

	// Step 5: Gateway stopped/unavailable simulation (invalid port) -> fails closed
	deadClient := codeinterpreter.New(sandbox.Config{
		Enabled:          true,
		BaseURL:          "http://127.0.0.1:54321",
		AuthToken:        authToken,
		SessionNamespace: "frostagent-e2e",
		ClientTimeout:    2 * time.Second,
	})
	deadTool := tools.ExecuteCommandTool(deadClient)
	_, err = deadTool.ExecuteContext(ctxA, `{"command": "echo should_fail"}`)
	if err == nil {
		t.Fatalf("step 5 expected failure on unavailable gateway, got nil")
	}

	// Clean up sessions
	_ = client.Release(context.Background(), sessionA)
	_ = client.Release(context.Background(), sessionB)
}
