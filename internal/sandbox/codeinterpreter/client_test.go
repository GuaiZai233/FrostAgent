package codeinterpreter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"FrostAgent/internal/logs"
	"FrostAgent/internal/sandbox"
	"FrostAgent/internal/sandbox/codeinterpreter"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func handleDefaultSession(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/api/v1/sessions" {
		var body struct {
			UserUUID string `json:"user_uuid"`
			Profile  string `json:"profile"`
			Network  string `json:"network"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_uuid": body.UserUUID,
			"profile":   body.Profile,
			"network":   body.Network,
			"status":    "ready",
		})
		return true
	}
	return false
}

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
		if handleDefaultSession(w, r) {
			return
		}
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
			if handleDefaultSession(w, r) {
				return
			}
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
			if handleDefaultSession(w, r) {
				return
			}
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
			if handleDefaultSession(w, r) {
				return
			}
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
			if handleDefaultSession(w, r) {
				return
			}
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

	t.Run("404 Not Found with generic route error returns error", func(t *testing.T) {
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
			t.Fatalf("expected error for generic 404 release, got nil")
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

	t.Run("401 Unauthorized returns backend error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"invalid token"}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-unauth")
		if err == nil {
			t.Fatalf("expected error on 401 release")
		}
	})

	t.Run("200 OK is idempotent success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"released"}`))
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "tok",
			SessionNamespace: "ns",
		}, codeinterpreter.WithHTTPClient(server.Client()))

		err := client.Release(context.Background(), "session-200")
		if err != nil {
			t.Fatalf("expected nil error on 200 release, got: %v", err)
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
				if handleDefaultSession(w, r) {
					return
				}
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
		if handleDefaultSession(w, r) {
			return
		}
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
		if handleDefaultSession(w, r) {
			return
		}
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
		if handleDefaultSession(w, r) {
			return
		}
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

func TestExec_QueryParamsParity(t *testing.T) {
	var capturedQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handleDefaultSession(w, r) {
			return
		}
		capturedQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"stdout": "ok",
			"exit_code": 0,
			"timed_out": false
		}`))
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()),
		codeinterpreter.WithProfile("custom-profile"),
		codeinterpreter.WithNetwork("isolated"))

	_, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess-query-test",
		Command:   "echo ok",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	if !uuidRegex.MatchString(capturedQuery.Get("user_uuid")) {
		t.Fatalf("expected valid UUID for user_uuid, got: %s", capturedQuery.Get("user_uuid"))
	}
	if capturedQuery.Get("profile") != "custom-profile" {
		t.Fatalf("expected profile custom-profile, got: %s", capturedQuery.Get("profile"))
	}
	if capturedQuery.Get("network") != "isolated" {
		t.Fatalf("expected network isolated, got: %s", capturedQuery.Get("network"))
	}
}

func TestCreateSession_SuccessAndExecInheritance(t *testing.T) {
	var sessionCreated bool
	var execProfile string
	var execNetwork string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sessionCreated = true
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			execProfile = r.URL.Query().Get("profile")
			execNetwork = r.URL.Query().Get("network")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stdout": "session exec ok",
				"exit_code": 0,
				"timed_out": false
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	sessInfo, err := client.CreateSession(context.Background(), "session-inherit", sandbox.ProfileActionRuntime, "none", nil)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if !sessionCreated {
		t.Fatalf("expected session creation request to server")
	}
	if sessInfo.Profile != sandbox.ProfileActionRuntime || sessInfo.Network != "none" || sessInfo.Status != "ready" {
		t.Fatalf("unexpected SessionInfo: %+v", sessInfo)
	}

	res, err := client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "session-inherit",
		Command:   "whoami",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if res.Stdout != "session exec ok" {
		t.Fatalf("unexpected stdout: %s", res.Stdout)
	}
	if execProfile != sandbox.ProfileActionRuntime {
		t.Fatalf("expected exec to inherit profile %q, got: %q", sandbox.ProfileActionRuntime, execProfile)
	}
	if execNetwork != "none" {
		t.Fatalf("expected exec to inherit network none, got: %q", execNetwork)
	}
}

func TestCreateSession_ConformanceFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Missing required network in response
		_, _ = w.Write([]byte(`{
			"user_uuid": "valid-uuid",
			"profile": "minimal",
			"status": "ready"
		}`))
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	_, err := client.CreateSession(context.Background(), "sess-fail", "minimal", "none", nil)
	if err == nil {
		t.Fatalf("expected conformance failure error, got nil")
	}
}

func TestRelease_CleansSessionMetadata(t *testing.T) {
	var execProfile string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/shell/exec":
			execProfile = r.URL.Query().Get("profile")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stdout": "ok",
				"exit_code": 0,
				"timed_out": false
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	_, err := client.CreateSession(context.Background(), "sess-clean", sandbox.ProfileActionRuntime, "none", nil)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = client.Release(context.Background(), "sess-clean")
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// After release, subsequent Exec on same SessionID uses default profile (minimal), not action-runtime
	_, err = client.Exec(context.Background(), sandbox.ExecRequest{
		SessionID: "sess-clean",
		Command:   "echo 1",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec failed: %v", err)
	}
	if execProfile != sandbox.ProfileMinimal {
		t.Fatalf("expected profile to reset to %q after release, got: %q", sandbox.ProfileMinimal, execProfile)
	}
}

func TestRelease_FailurePreservesSessionMetadata(t *testing.T) {
	var releaseAttempts int
	var releaseAttemptsMu sync.Mutex
	var capturedExecProfile string
	var capturedExecNetwork string
	var execMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			var body struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": body.UserUUID,
				"profile":   body.Profile,
				"network":   body.Network,
				"status":    "ready",
			})
		case "/api/v1/release":
			releaseAttemptsMu.Lock()
			releaseAttempts++
			attempt := releaseAttempts
			releaseAttemptsMu.Unlock()

			if attempt == 1 {
				// 2. Release 第一次返回 500
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"temporary release failure"}`))
				return
			}
			// 4. Release 第二次成功
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"released"}`))
		case "/api/v1/shell/exec":
			execMu.Lock()
			capturedExecProfile = r.URL.Query().Get("profile")
			capturedExecNetwork = r.URL.Query().Get("network")
			execMu.Unlock()

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"stdout": "ok",
				"exit_code": 0,
				"timed_out": false
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "tok",
		SessionNamespace: "ns",
	}, codeinterpreter.WithHTTPClient(server.Client()))

	ctx := context.Background()
	sessionID := "sess-failover"

	// 1. CreateSession(action-runtime, isolated)
	sessInfo, err := client.CreateSession(ctx, sessionID, sandbox.ProfileActionRuntime, "isolated", nil)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sessInfo.Profile != sandbox.ProfileActionRuntime || sessInfo.Network != "isolated" {
		t.Fatalf("unexpected SessionInfo: %+v", sessInfo)
	}

	// 2. Release 第一次返回 500
	err = client.Release(ctx, sessionID)
	if err == nil {
		t.Fatalf("expected error on first release (HTTP 500), got nil")
	}

	// 3. 再 Exec，同一 session query 仍必须是 action-runtime/isolated
	_, err = client.Exec(ctx, sandbox.ExecRequest{
		SessionID: sessionID,
		Command:   "echo test-1",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec failed after release error: %v", err)
	}
	execMu.Lock()
	prof1 := capturedExecProfile
	net1 := capturedExecNetwork
	execMu.Unlock()
	if prof1 != sandbox.ProfileActionRuntime {
		t.Fatalf("expected exec to preserve profile %q after failed release, got: %q", sandbox.ProfileActionRuntime, prof1)
	}
	if net1 != "isolated" {
		t.Fatalf("expected exec to preserve network %q after failed release, got: %q", "isolated", net1)
	}

	// 4. Release 第二次成功
	err = client.Release(ctx, sessionID)
	if err != nil {
		t.Fatalf("expected second release to succeed, got: %v", err)
	}

	// 5. 之后 metadata 才清除 (next Exec resets to client default minimal/none)
	_, err = client.Exec(ctx, sandbox.ExecRequest{
		SessionID: sessionID,
		Command:   "echo test-2",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Exec failed after successful release: %v", err)
	}
	execMu.Lock()
	prof2 := capturedExecProfile
	net2 := capturedExecNetwork
	execMu.Unlock()
	if prof2 != sandbox.ProfileMinimal {
		t.Fatalf("expected profile to reset to %q after successful release, got: %q", sandbox.ProfileMinimal, prof2)
	}
	if net2 != "none" {
		t.Fatalf("expected network to reset to %q after successful release, got: %q", "none", net2)
	}
}

func TestLifecycleGate_AntiOrphanGuarantee(t *testing.T) {
	ctx := context.Background()
	const sessionID = "sess-orphan-test"

	var mu sync.Mutex
	activeSessions := make(map[string]bool)
	sessionsStarted := make(chan struct{})
	releaseBlock := make(chan struct{})
	var startOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			var req struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			// Signal that /sessions HTTP request has reached the gateway
			startOnce.Do(func() {
				close(sessionsStarted)
			})

			// Block until test releases the gate
			<-releaseBlock

			mu.Lock()
			activeSessions[req.UserUUID] = true
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": req.UserUUID,
				"profile":   req.Profile,
				"network":   req.Network,
				"status":    "ready",
			})
		case "/api/v1/release":
			userUUID := r.URL.Query().Get("user_uuid")
			mu.Lock()
			delete(activeSessions, userUUID)
			mu.Unlock()

			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "secret",
		SessionNamespace: "test-ns",
	})
	userUUID := client.SessionIDToUUID(sessionID)

	var ensureErr error
	var releaseErr error
	var wg sync.WaitGroup

	// Step 1: Start EnsureSession in goroutine; it blocks in /api/v1/sessions
	wg.Add(1)
	go func() {
		defer wg.Done()
		ensureErr = client.EnsureSession(ctx, sessionID)
	}()

	// Wait until EnsureSession is actively in-flight in /api/v1/sessions
	select {
	case <-sessionsStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for EnsureSession to reach gateway")
	}

	// Step 2: Concurrent Release is called while /sessions is in-flight.
	// The lifecycle gate serializes Release after provisioning.
	wg.Add(1)
	go func() {
		defer wg.Done()
		releaseErr = client.Release(ctx, sessionID)
	}()

	// Give Release time to queue behind EnsureSession's gate lock
	time.Sleep(50 * time.Millisecond)

	// Step 3: Unblock /sessions response. EnsureSession completes remote provisioning,
	// and then Release immediately runs, releasing the newly created remote session!
	close(releaseBlock)

	wg.Wait()

	if releaseErr != nil {
		t.Fatalf("Release failed: %v", releaseErr)
	}
	_ = ensureErr

	// Step 4: Verify Anti-Orphan Guarantee:
	// - Zero local session metadata remains
	// - Zero active remote session exists on the gateway
	if client.HasSession(sessionID) {
		t.Fatalf("anti-orphan violation: local metadata still exists for session %q", sessionID)
	}

	mu.Lock()
	active := activeSessions[userUUID]
	mu.Unlock()
	if active {
		t.Fatalf("anti-orphan violation: active remote session %q still alive on gateway after Release", userUUID)
	}
}

func TestExec_GatewayRestartEvictionRecovery(t *testing.T) {
	ctx := context.Background()
	const sessionID = "sess-restart-recovery"

	var mu sync.Mutex
	activeSessions := make(map[string]bool)
	sessionCreateCount := 0
	execCount := 0
	forceGeneric404 := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/sessions":
			var req struct {
				UserUUID string `json:"user_uuid"`
				Profile  string `json:"profile"`
				Network  string `json:"network"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)

			mu.Lock()
			sessionCreateCount++
			activeSessions[req.UserUUID] = true
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_uuid": req.UserUUID,
				"profile":   req.Profile,
				"network":   req.Network,
				"status":    "ready",
			})
		case "/api/v1/shell/exec":
			userUUID := r.URL.Query().Get("user_uuid")
			profile := r.URL.Query().Get("profile")
			network := r.URL.Query().Get("network")

			mu.Lock()
			execCount++
			isActive := activeSessions[userUUID]
			generic404 := forceGeneric404
			mu.Unlock()

			if generic404 {
				// Generic router 404 (endpoint or route missing)
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":"Not Found"}`))
				return
			}

			if !isActive {
				// Machine-readable session missing/evicted response
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":    "session_not_found",
					"message": "session evicted or gateway restarted",
				})
				return
			}

			// Validate policy preservation during recovery
			if profile != sandbox.ProfileActionRuntime || network != "isolated" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"policy mismatch"}`))
				return
			}

			exitCode := 0
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    "recovered successfully\n",
				"stderr":    "",
				"exit_code": &exitCode,
				"timed_out": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := codeinterpreter.New(sandbox.Config{
		BaseURL:          server.URL,
		AuthToken:        "secret",
		SessionNamespace: "test-ns",
	}, codeinterpreter.WithProfile(sandbox.ProfileActionRuntime), codeinterpreter.WithNetwork("isolated"))

	// 1. Initial Exec: creates session on gateway and succeeds
	res1, err := client.Exec(ctx, sandbox.ExecRequest{
		SessionID: sessionID,
		Command:   "echo test-1",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("first Exec failed: %v", err)
	}
	if strings.TrimSpace(res1.Stdout) != "recovered successfully" {
		t.Fatalf("unexpected stdout: %q", res1.Stdout)
	}

	mu.Lock()
	if sessionCreateCount != 1 {
		t.Fatalf("expected 1 session create, got %d", sessionCreateCount)
	}
	if execCount != 1 {
		t.Fatalf("expected 1 exec, got %d", execCount)
	}
	mu.Unlock()

	// 2. Simulate Gateway Restart / Eviction:
	// Clear gateway active sessions without touching client local state
	mu.Lock()
	activeSessions = make(map[string]bool)
	mu.Unlock()

	// Client still has cached metadata in c.sessions:
	if !client.HasSession(sessionID) {
		t.Fatal("expected client to still have cached session metadata before second Exec")
	}

	// 3. Second Exec: hits 404 session_not_found on attempt 0 -> automatically purges stale metadata,
	// re-provisions session with preserved profile/network, and retries command successfully!
	res2, err := client.Exec(ctx, sandbox.ExecRequest{
		SessionID: sessionID,
		Command:   "echo test-2",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("second Exec with auto-recovery failed: %v", err)
	}
	if strings.TrimSpace(res2.Stdout) != "recovered successfully" {
		t.Fatalf("unexpected stdout on recovered exec: %q", res2.Stdout)
	}

	mu.Lock()
	if sessionCreateCount != 2 {
		t.Fatalf("expected 2 session creates (1 initial + 1 recovery), got %d", sessionCreateCount)
	}
	// execCount should be 3: 1 from initial + 1 from failed attempt 0 + 1 from successful retry attempt 1
	if execCount != 3 {
		t.Fatalf("expected 3 exec requests total, got %d", execCount)
	}
	mu.Unlock()

	if !client.HasSession(sessionID) {
		t.Fatal("expected client to have valid re-provisioned session metadata")
	}

	// 4. Verify Generic 404 does NOT trigger recovery and fails closed immediately
	mu.Lock()
	forceGeneric404 = true
	mu.Unlock()

	_, err = client.Exec(ctx, sandbox.ExecRequest{
		SessionID: sessionID,
		Command:   "echo test-generic-404",
		Timeout:   5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected generic 404 to fail closed, got nil error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected error to mention 404, got: %v", err)
	}
}

func TestEnsureSession_UnknownOutcomeReconciliation(t *testing.T) {
	ctx := context.Background()

	t.Run("ConnectionHijackClose_ReconcilesRemoteSession", func(t *testing.T) {
		var mu sync.Mutex
		activeSessions := make(map[string]bool)
		var releaseCalled bool

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/sessions":
				var req struct {
					UserUUID string `json:"user_uuid"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				activeSessions[req.UserUUID] = true
				mu.Unlock()

				// Hijack connection and abruptly close it (simulating network reset / crash after side effect)
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Fatalf("server does not support hijacking")
				}
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			case "/api/v1/release":
				userUUID := r.URL.Query().Get("user_uuid")
				mu.Lock()
				delete(activeSessions, userUUID)
				releaseCalled = true
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "secret",
			SessionNamespace: "test-ns",
		})
		sessionID := "sess-unknown-hijack"
		userUUID := client.SessionIDToUUID(sessionID)

		err := client.EnsureSession(ctx, sessionID)
		if err == nil {
			t.Fatal("expected EnsureSession to fail on connection drop, got nil")
		}
		if !strings.Contains(err.Error(), "creation outcome unknown") {
			t.Fatalf("expected error to mention creation outcome unknown, got: %v", err)
		}

		mu.Lock()
		active := activeSessions[userUUID]
		wasReleased := releaseCalled
		mu.Unlock()

		if !wasReleased {
			t.Fatal("expected reconciliation release to be called on unknown creation outcome")
		}
		if active {
			t.Fatalf("anti-orphan violation: active remote session %q still alive on gateway", userUUID)
		}
		if client.HasSession(sessionID) {
			t.Fatal("expected local session metadata to be empty")
		}
	})

	t.Run("Gateway500AfterSideEffect_ReconcilesRemoteSession", func(t *testing.T) {
		var mu sync.Mutex
		activeSessions := make(map[string]bool)
		var releaseCalled bool

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/sessions":
				var req struct {
					UserUUID string `json:"user_uuid"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				activeSessions[req.UserUUID] = true
				mu.Unlock()

				// Returns 500 after having created the session
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"gateway internal error during init"}`))
			case "/api/v1/release":
				userUUID := r.URL.Query().Get("user_uuid")
				mu.Lock()
				delete(activeSessions, userUUID)
				releaseCalled = true
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "secret",
			SessionNamespace: "test-ns",
		})
		sessionID := "sess-unknown-500"
		userUUID := client.SessionIDToUUID(sessionID)

		err := client.EnsureSession(ctx, sessionID)
		if err == nil {
			t.Fatal("expected EnsureSession to fail on 500, got nil")
		}
		if !strings.Contains(err.Error(), "creation outcome unknown") {
			t.Fatalf("expected error to mention creation outcome unknown, got: %v", err)
		}

		mu.Lock()
		active := activeSessions[userUUID]
		wasReleased := releaseCalled
		mu.Unlock()

		if !wasReleased {
			t.Fatal("expected reconciliation release to be called on 500 unknown outcome")
		}
		if active {
			t.Fatalf("anti-orphan violation: active remote session %q still alive on gateway", userUUID)
		}
		if client.HasSession(sessionID) {
			t.Fatal("expected local session metadata to be empty")
		}
	})

	t.Run("Malformed200Response_ReconcilesRemoteSession", func(t *testing.T) {
		var mu sync.Mutex
		activeSessions := make(map[string]bool)
		var releaseCalled bool

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/sessions":
				var req struct {
					UserUUID string `json:"user_uuid"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				mu.Lock()
				activeSessions[req.UserUUID] = true
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{corrupted-json`))
			case "/api/v1/release":
				userUUID := r.URL.Query().Get("user_uuid")
				mu.Lock()
				delete(activeSessions, userUUID)
				releaseCalled = true
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "secret",
			SessionNamespace: "test-ns",
		})
		sessionID := "sess-unknown-malformed"
		userUUID := client.SessionIDToUUID(sessionID)

		err := client.EnsureSession(ctx, sessionID)
		if err == nil {
			t.Fatal("expected EnsureSession to fail on malformed JSON, got nil")
		}
		if !strings.Contains(err.Error(), "creation outcome unknown") {
			t.Fatalf("expected error to mention creation outcome unknown, got: %v", err)
		}

		mu.Lock()
		active := activeSessions[userUUID]
		wasReleased := releaseCalled
		mu.Unlock()

		if !wasReleased {
			t.Fatal("expected reconciliation release to be called on malformed 200")
		}
		if active {
			t.Fatalf("anti-orphan violation: active remote session %q still alive on gateway", userUUID)
		}
		if client.HasSession(sessionID) {
			t.Fatal("expected local session metadata to be empty")
		}
	})

	t.Run("Explicit400Rejection_DoesNotTriggerRelease", func(t *testing.T) {
		var releaseCalled bool

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/sessions":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid profile requested"}`))
			case "/api/v1/release":
				releaseCalled = true
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := codeinterpreter.New(sandbox.Config{
			BaseURL:          server.URL,
			AuthToken:        "secret",
			SessionNamespace: "test-ns",
		})
		sessionID := "sess-known-400"

		err := client.EnsureSession(ctx, sessionID)
		if err == nil {
			t.Fatal("expected EnsureSession to fail on 400, got nil")
		}
		if strings.Contains(err.Error(), "creation outcome unknown") {
			t.Fatalf("400 should be an explicit rejection, not creation outcome unknown: %v", err)
		}

		if releaseCalled {
			t.Fatal("reconciliation release should NOT be called on clean 400 rejection")
		}
		if client.HasSession(sessionID) {
			t.Fatal("expected local session metadata to be empty")
		}
	})
}
