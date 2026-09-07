package codeinterpreter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"FrostAgent/internal/logs"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/sandbox/codeinterpreter"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestDeterministicSessionMapping(t *testing.T) {
	clientA1 := codeinterpreter.New(sandbox.Config{
		SessionNamespace: "frostagent-a",
	})
	clientA2 := codeinterpreter.New(sandbox.Config{
		SessionNamespace: "frostagent-a",
	})
	clientB := codeinterpreter.New(sandbox.Config{
		SessionNamespace: "frostagent-b",
	})

	uuid1 := clientA1.SessionIDToUUID("private:123")
	uuid2 := clientA2.SessionIDToUUID("private:123")
	if uuid1 != uuid2 {
		t.Fatalf("expected same namespace + session ID to produce identical UUID, got %q vs %q", uuid1, uuid2)
	}

	if !uuidRegex.MatchString(uuid1) {
		t.Fatalf("UUID %q does not match RFC4122 v5 format", uuid1)
	}

	uuidDiffSession := clientA1.SessionIDToUUID("private:456")
	if uuid1 == uuidDiffSession {
		t.Fatalf("expected different session IDs to produce different UUIDs, got same: %q", uuid1)
	}

	uuidDiffNamespace := clientB.SessionIDToUUID("private:123")
	if uuid1 == uuidDiffNamespace {
		t.Fatalf("expected different namespaces to produce different UUIDs, got same: %q", uuid1)
	}
}

func TestHealth(t *testing.T) {
	t.Run("success 200 with valid token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/status" {
				t.Errorf("expected /api/v1/status, got %s", r.URL.Path)
			}
			if r.Header.Get("X-Auth-Token") != "expected-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"total_workers":2,"busy_workers":0,"is_initializing":false}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:   server.URL,
			AuthToken: "expected-token",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		if err := client.Health(context.Background()); err != nil {
			t.Fatalf("expected Health() to succeed, got %v", err)
		}
	})

	t.Run("non-200 returns error without leaking token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("gateway overloaded with token secret-token-value"))
		}))
		defer server.Close()

		token := "secret-token-value"
		client := codeinterpreter.New(sandbox.Config{
			BaseURL:   server.URL,
			AuthToken: token,
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Health(context.Background())
		if err == nil {
			t.Fatalf("expected Health() to fail on 503")
		}
		if strings.Contains(err.Error(), token) {
			t.Fatalf("error message leaked auth token: %s", err.Error())
		}
	})
}

func TestExecRequest_PayloadAndRouting(t *testing.T) {
	var capturedMethod, capturedPath, capturedQuery, capturedToken string
	var capturedBody struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
		Timeout int    `json:"timeout"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path
		capturedQuery = r.URL.Query().Get("user_uuid")
		capturedToken = r.Header.Get("X-Auth-Token")

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"stdout": "output",
			"stderr": "",
			"exit_code": 0,
			"timed_out": false,
			"stdout_truncated": false,
			"stderr_truncated": false,
			"duration_ms": 50
		}`))
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "test-token",
		SessionNamespace: "frostagent",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	res, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "group:98765",
		Command:   "echo hello",
		Cwd:       "/sandbox/work",
		Timeout:   15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec() failed: %v", err)
	}

	if capturedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", capturedMethod)
	}
	if capturedPath != "/api/v1/shell/exec" {
		t.Errorf("expected /api/v1/shell/exec, got %s", capturedPath)
	}
	if !uuidRegex.MatchString(capturedQuery) {
		t.Errorf("expected valid user_uuid, got %q", capturedQuery)
	}
	if capturedToken != "test-token" {
		t.Errorf("expected test-token, got %s", capturedToken)
	}
	if capturedBody.Command != "echo hello" || capturedBody.Cwd != "/sandbox/work" || capturedBody.Timeout != 15 {
		t.Errorf("payload mismatch: %+v", capturedBody)
	}
	if res.Stdout != "output" || res.ExitCode == nil || *res.ExitCode != 0 {
		t.Errorf("result mismatch: %+v", res)
	}
}

func TestExec_SuccessAndFailureSemantics(t *testing.T) {
	t.Run("command failure exit code 7 returns nil error and exit_code 7", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stdout": "",
				"stderr": "syntax error",
				"exit_code": 7,
				"timed_out": false,
				"stdout_truncated": false,
				"stderr_truncated": false,
				"duration_ms": 12
			}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		res, err := client.Exec(context.Background(), sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "exit 7",
			Timeout:   5 * time.Second,
		})
		if err != nil {
			t.Fatalf("expected nil Go error on command exit 7, got %v", err)
		}
		if res.ExitCode == nil || *res.ExitCode != 7 {
			t.Fatalf("expected ExitCode 7, got %v", res.ExitCode)
		}
		if res.TimedOut {
			t.Fatalf("expected TimedOut false")
		}
	})

	t.Run("command timeout returns nil error and timed_out true", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stdout": "partial",
				"stderr": "",
				"exit_code": null,
				"timed_out": true,
				"stdout_truncated": false,
				"stderr_truncated": false,
				"duration_ms": 5000
			}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		res, err := client.Exec(context.Background(), sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "sleep 10",
			Timeout:   5 * time.Second,
		})
		if err != nil {
			t.Fatalf("expected nil Go error on timeout, got %v", err)
		}
		if !res.TimedOut {
			t.Fatalf("expected TimedOut true")
		}
		if res.ExitCode != nil {
			t.Fatalf("expected ExitCode nil on timeout, got %v", *res.ExitCode)
		}
	})
}

func TestExec_GatewayErrors(t *testing.T) {
	statusCodes := []int{
		http.StatusBadRequest,          // 400
		http.StatusUnauthorized,        // 401
		http.StatusForbidden,           // 403
		http.StatusUnprocessableEntity, // 422
		http.StatusInternalServerError, // 500
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
	}

	for _, code := range statusCodes {
		t.Run(fmt.Sprintf("HTTP %d returns infrastructure error", code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(fmt.Sprintf("error with status %d and secret-token", code)))
			}))
			defer server.Close()

			token := "secret-token"
			client := codeinterpreter.New(sandbox.Config{
				BaseURL:          server.URL,
				AuthToken:        token,
				SessionNamespace: "ns",
			}, codeinterpreter.WithHTTPClient(server.Client()))

			_, err := client.Exec(context.Background(), sandbox.ExecRequest{
				SessionID: "sess-err",
				Command:   "ls",
				Timeout:   5 * time.Second,
			})
			if err == nil {
				t.Fatalf("expected error for HTTP %d, got nil", code)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%d", code)) {
				t.Errorf("expected error to mention status code %d: %v", code, err)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("error leaked secret token: %s", err.Error())
			}
		})
	}
}

func TestExec_MalformedAndOversizedResponse(t *testing.T) {
	t.Run("malformed JSON response returns protocol error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{not valid json`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		_, err := client.Exec(context.Background(), sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "ls",
			Timeout:   5 * time.Second,
		})
		if err == nil {
			t.Fatalf("expected error on malformed JSON")
		}
	})

	t.Run("oversized response body is bounded", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Send an infinite or huge stream that exceeds 16MB
			w.Header().Set("Content-Type", "application/json")
			// Create a string prefix that opens json
			_, _ = w.Write([]byte(`{"stdout":"`))
			chunk := strings.Repeat("A", 1024*1024)
			for i := 0; i < 20; i++ {
				_, _ = w.Write([]byte(chunk))
			}
			_, _ = w.Write([]byte(`","stderr":"","exit_code":0}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		_, err := client.Exec(context.Background(), sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "cat bigfile",
			Timeout:   5 * time.Second,
		})
		// When reader is limited to 10MB, the unclosed JSON string fails to parse
		if err == nil {
			t.Fatalf("expected error on oversized stream truncation")
		}
	})
}

func TestExec_NetworkAndContextFailures(t *testing.T) {
	t.Run("connection refused returns network error", func(t *testing.T) {
		// Pick an unused local port
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skip("cannot listen on local port")
		}
		addr := l.Addr().String()
		_ = l.Close() // Immediately close to guarantee connection refused

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          "http://" + addr,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		})

		_, err = client.Exec(context.Background(), sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "ls",
			Timeout:   5 * time.Second,
		})
		if err == nil {
			t.Fatalf("expected network failure error, got nil")
		}
	})

	t.Run("caller context cancellation aborts request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Sleep longer than context deadline
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := client.Exec(ctx, sandbox.ExecRequest{
			SessionID: "sess1",
			Command:   "sleep 1",
			Timeout:   5 * time.Second,
		})
		if err == nil {
			t.Fatalf("expected context cancellation error, got nil")
		}
	})
}

func TestRelease(t *testing.T) {
	t.Run("204 No Content is success", func(t *testing.T) {
		var capturedUUID string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.URL.Path != "/api/v1/release" {
				t.Errorf("expected /api/v1/release, got %s", r.URL.Path)
			}
			capturedUUID = r.URL.Query().Get("user_uuid")
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-abc")
		if err != nil {
			t.Fatalf("expected nil error on 204, got %v", err)
		}
		if !uuidRegex.MatchString(capturedUUID) {
			t.Fatalf("expected valid UUID in release query, got %q", capturedUUID)
		}
	})

	t.Run("404 Not Found is idempotent success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"No active session found for user"}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-gone")
		if err != nil {
			t.Fatalf("expected nil error for idempotent 404, got %v", err)
		}
	})

	t.Run("404 Not Found with generic route error returns backend error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-gone")
		if err == nil {
			t.Fatalf("expected error for generic 404 route error, got nil")
		}
	})

	t.Run("500 returns backend error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal cleanup failure"))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-fail")
		if err == nil {
			t.Fatalf("expected error on 500 release")
		}
	})

	t.Run("200 OK is rejected to prevent masking misconfigurations", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-200")
		if err == nil {
			t.Fatalf("expected error on 200 release, got nil")
		}
	})
}

func TestExec_SemanticExecutionStateInvariants(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		expectError bool
		checkResult func(t *testing.T, res sandbox.ExecResult)
	}{
		{
			name:        "empty JSON object missing exit_code and timed_out false",
			body:        `{}`,
			expectError: true,
		},
		{
			name:        "exit_code null and timed_out false",
			body:        `{"stdout":"","stderr":"","exit_code":null,"timed_out":false}`,
			expectError: true,
		},
		{
			name:        "exit_code non-nil and timed_out true",
			body:        `{"stdout":"","stderr":"","exit_code":0,"timed_out":true}`,
			expectError: true,
		},
		{
			name:        "valid completed command",
			body:        `{"stdout":"hello","stderr":"","exit_code":0,"timed_out":false,"duration_ms":42}`,
			expectError: false,
			checkResult: func(t *testing.T, res sandbox.ExecResult) {
				if res.TimedOut {
					t.Errorf("expected timed_out false")
				}
				if res.ExitCode == nil || *res.ExitCode != 0 {
					t.Errorf("expected exit_code 0, got %v", res.ExitCode)
				}
				if res.Stdout != "hello" {
					t.Errorf("expected stdout 'hello', got %q", res.Stdout)
				}
			},
		},
		{
			name:        "valid timed out command",
			body:        `{"stdout":"partial","stderr":"","exit_code":null,"timed_out":true,"duration_ms":1000}`,
			expectError: false,
			checkResult: func(t *testing.T, res sandbox.ExecResult) {
				if !res.TimedOut {
					t.Errorf("expected timed_out true")
				}
				if res.ExitCode != nil {
					t.Errorf("expected exit_code nil, got %v", res.ExitCode)
				}
				if res.Stdout != "partial" {
					t.Errorf("expected stdout 'partial', got %q", res.Stdout)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := codeinterpreter.New(sandbox.Config{
				BaseURL:          server.URL,
				AuthToken:        "tok",
				SessionNamespace: "ns",
			}, codeinterpreter.WithHTTPClient(server.Client()))

			res, err := client.Exec(context.Background(), sandbox.ExecRequest{
				SessionID: "sess1",
				Command:   "ls",
				Timeout:   5 * time.Second,
			})
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.checkResult != nil {
					tc.checkResult(t, res)
				}
			}
		})
	}
}

func TestExec_FloatTimeoutSemantics(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"stdout":"done","stderr":"","exit_code":0,"timed_out":false}`))
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	_, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess1",
		Command:   "sleep 5.5",
		Timeout:   5500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}

	var parsed struct {
		Timeout float64 `json:"timeout"`
	}
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if parsed.Timeout != 5.5 {
		t.Fatalf("expected timeout 5.5, got %v", parsed.Timeout)
	}
}

func TestExec_EscapeHeavyNearLimitResponse(t *testing.T) {
	// Worker limit is 1 MiB per stream. Under worst-case control character escaping
	// ( = 6 bytes in JSON), 1 MiB expands to ~6 MiB JSON per stream.
	// Two streams (stdout + stderr) expand to ~12 MiB total JSON.
	// We verify that the 16 MiB client limit allows this response to decode cleanly.
	rawEscapeStream := strings.Repeat("\\u0000", 1024*1024)
	payload := `{"stdout":"` + rawEscapeStream + `","stderr":"` + rawEscapeStream + `","exit_code":0,"timed_out":false}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	res, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess1",
		Command:   "cat binary_control_chars",
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected 12 MiB escape-heavy response to decode successfully, got error: %v", err)
	}
	if res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %v", res.ExitCode)
	}
	if len(res.Stdout) != 1024*1024 {
		t.Fatalf("expected stdout length 1048576, got %d", len(res.Stdout))
	}
}

func TestExec_Gateway422_RedactsSentinelSecretInInputField(t *testing.T) {
	logs.Init(100)
	logs.Clear()

	sentinelSecret := "SENTINEL_GATEWAY_422_SECRET_XYZ987"
	secretCommand := "curl -H 'Authorization: Bearer " + sentinelSecret + "' https://api.internal/data"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"detail": []map[string]any{
				{
					"type":  "string_too_long",
					"loc":   []any{"body", "command"},
					"msg":   "String should have at most 65536 characters",
					"input": secretCommand,
				},
			},
		})
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	_, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess-422-leak-test",
		Command:   secretCommand,
		Timeout:   5 * time.Second,
	})

	if err == nil {
		t.Fatalf("expected error for 422 Gateway response, got nil")
	}

	// 1. Returned error must NOT contain the sentinel secret
	if strings.Contains(err.Error(), sentinelSecret) {
		t.Fatalf("returned error leaked sentinel secret: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "body.command") {
		t.Fatalf("expected error to mention 422 and body.command, got: %s", err.Error())
	}

	// 2. Log buffer must NOT contain the sentinel secret in ANY entry
	snapshot := logs.Snapshot()
	var foundSystemWarn bool
	for _, entry := range snapshot {
		if strings.Contains(entry.Content, sentinelSecret) {
			t.Fatalf("log entry (%s) leaked sentinel secret: %s", entry.Category, entry.Content)
		}
		if entry.Category == logs.SYSTEM && strings.Contains(entry.Content, "422") && strings.Contains(entry.Content, "body.command") {
			foundSystemWarn = true
		}
	}
	if !foundSystemWarn {
		t.Fatalf("expected SYSTEM log warning for 422 validation error in snapshot")
	}
}

func TestExec_LocalValidation_RejectsOversizedCommand(t *testing.T) {
	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          "http://127.0.0.1:3874",
		AuthToken:        "tok",
		SessionNamespace: "ns",
	})

	oversizedCmd := strings.Repeat("x", sandbox.MaxCommandLength+1)
	_, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess-val",
		Command:   oversizedCmd,
		Timeout:   5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "command exceeds maximum allowed length") {
		t.Fatalf("expected error for oversized command, got: %v", err)
	}
}

func TestExec_LocalValidation_RejectsOversizedCwd(t *testing.T) {
	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          "http://127.0.0.1:3874",
		AuthToken:        "tok",
		SessionNamespace: "ns",
	})

	oversizedCwd := "/" + strings.Repeat("y", sandbox.MaxCwdLength+1)
	_, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess-val",
		Command:   "ls",
		Cwd:       oversizedCwd,
		Timeout:   5 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "cwd exceeds maximum allowed length") {
		t.Fatalf("expected error for oversized cwd, got: %v", err)
	}
}
