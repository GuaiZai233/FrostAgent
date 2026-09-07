package codeinterpreter

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"FrostAgent/internal/logs"
	"FrostAgent/internal/sandbox"
)

var bearerTokenPattern = regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.\:\=\+\/]+`)

const (
	// maxResponseBodyBytes bounds gateway response bodies. Sized to 16 MiB to safely
	// accommodate up to 1 MiB per stream (Worker limit) under worst-case JSON control
	// character escaping (e.g. control char expansion => ~12 MiB total).
	maxResponseBodyBytes = 16 << 20 // 16 MiB
	maxErrorBodyBytes    = 4096     // 4 KiB limit on error bodies
	httpTimeoutEnvelope  = 10 * time.Second
)

// Default fixed project namespace UUID bytes (RFC 4122 namespace).
// Used as the seed for deterministic UUIDv5 derivation.
var defaultProjectNamespace = [16]byte{
	0x9b, 0x98, 0x6a, 0x76, 0x6c, 0x17, 0x48, 0xf8,
	0xb3, 0xd4, 0x72, 0x2a, 0x46, 0x69, 0xf9, 0x39,
}

// Client implements sandbox.Backend via the code-interpreter Gateway HTTP API.
type Client struct {
	baseURL          string
	authToken        string
	sessionNamespace string
	httpClient       *http.Client
	projectNamespace [16]byte
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client (useful for testing with httptest).
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithProjectNamespace sets a custom fixed project namespace UUID.
func WithProjectNamespace(ns [16]byte) Option {
	return func(c *Client) {
		c.projectNamespace = ns
	}
}

// New creates a new code-interpreter sandbox Backend client.
func New(cfg sandbox.Config, opts ...Option) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	c := &Client{
		baseURL:          baseURL,
		authToken:        cfg.AuthToken,
		sessionNamespace: cfg.SessionNamespace,
		httpClient: &http.Client{
			Timeout: cfg.ClientTimeout,
		},
		projectNamespace: defaultProjectNamespace,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// SessionIDToUUID derives a deterministic RFC4122 v5 UUID from the FrostAgent session ID
// and configured session namespace.
func (c *Client) SessionIDToUUID(sessionID string) string {
	// fixed project namespace + SANDBOX_SESSION_NAMESPACE + "\x00" + SessionID
	// -> SHA-1 namespace UUID -> RFC4122 version/variant bits -> UUID string
	h := sha1.New()
	h.Write(c.projectNamespace[:])
	h.Write([]byte(c.sessionNamespace))
	h.Write([]byte{0x00})
	h.Write([]byte(sessionID))
	sum := h.Sum(nil)

	// RFC 4122 UUIDv5 bits
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // variant RFC 4122

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		sum[0:4],
		sum[4:6],
		sum[6:8],
		sum[8:10],
		sum[10:16],
	)
}

// Health checks if the code-interpreter gateway is reachable and authenticated.
func (c *Client) Health(ctx context.Context) error {
	statusURL, err := url.Parse(c.baseURL + "/api/v1/status")
	if err != nil {
		return fmt.Errorf("invalid status URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to build health request: %w", err)
	}
	req.Header.Set("X-Auth-Token", c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sandbox gateway health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody := readBoundedString(resp.Body, maxErrorBodyBytes)
		return fmt.Errorf("sandbox gateway status check returned HTTP %d: %s", resp.StatusCode, sanitizeError(errBody, c.authToken))
	}

	return nil
}

// Release terminates and cleans up the worker instance for the session.
func (c *Client) Release(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("cannot release sandbox without sessionID")
	}

	userUUID := c.SessionIDToUUID(sessionID)
	releaseURL, err := url.Parse(c.baseURL + "/api/v1/release")
	if err != nil {
		return fmt.Errorf("invalid release URL: %w", err)
	}
	q := releaseURL.Query()
	q.Set("user_uuid", userUUID)
	releaseURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, releaseURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to build release request: %w", err)
	}
	req.Header.Set("X-Auth-Token", c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sandbox gateway release request failed: %w", err)
	}
	defer resp.Body.Close()

	// 204 No Content -> success
	// 404 Not Found specifically for "no active session" -> idempotent success
	// Generic 404s (e.g. wrong base URL or reverse proxy 404) and HTTP 200 are rejected.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	errBody := readBoundedString(resp.Body, maxErrorBodyBytes)
	if resp.StatusCode == http.StatusNotFound {
		lowerErr := strings.ToLower(errBody)
		if strings.Contains(lowerErr, "no active session") || strings.Contains(lowerErr, "session not found") {
			return nil
		}
		return fmt.Errorf("sandbox gateway release returned unexpected 404 (endpoint or route not found): %s", sanitizeError(errBody, c.authToken))
	}

	return fmt.Errorf("sandbox gateway release returned HTTP %d: %s", resp.StatusCode, sanitizeError(errBody, c.authToken))
}

type gatewayExecRequest struct {
	Command string  `json:"command"`
	Cwd     string  `json:"cwd"`
	Timeout float64 `json:"timeout"`
}

type gatewayExecResponse struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        *int   `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMs      int64  `json:"duration_ms"`
}

// Exec executes a command inside the isolated sandbox for the given session.
func (c *Client) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return sandbox.ExecResult{}, errors.New("sandbox execution requires non-empty SessionID")
	}
	if strings.TrimSpace(req.Command) == "" {
		return sandbox.ExecResult{}, errors.New("command cannot be empty")
	}
	if utf8.RuneCountInString(req.Command) > sandbox.MaxCommandLength {
		return sandbox.ExecResult{}, fmt.Errorf("command exceeds maximum allowed length (%d characters)", sandbox.MaxCommandLength)
	}
	if req.Cwd != "" && utf8.RuneCountInString(req.Cwd) > sandbox.MaxCwdLength {
		return sandbox.ExecResult{}, fmt.Errorf("cwd exceeds maximum allowed length (%d characters)", sandbox.MaxCwdLength)
	}
	if req.Timeout <= 0 {
		return sandbox.ExecResult{}, errors.New("timeout must be greater than 0")
	}
	if req.Timeout > sandbox.MaxExecutionTimeout {
		return sandbox.ExecResult{}, fmt.Errorf("timeout exceeds maximum allowed duration (%s)", sandbox.MaxExecutionTimeout)
	}

	userUUID := c.SessionIDToUUID(req.SessionID)
	execURL, err := url.Parse(c.baseURL + "/api/v1/shell/exec")
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("invalid exec URL: %w", err)
	}
	q := execURL.Query()
	q.Set("user_uuid", userUUID)
	execURL.RawQuery = q.Encode()

	timeoutSec := req.Timeout.Seconds()
	if timeoutSec <= 0 {
		timeoutSec = 1.0
	}

	payload := gatewayExecRequest{
		Command: req.Command,
		Cwd:     req.Cwd,
		Timeout: timeoutSec,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("failed to marshal exec payload: %w", err)
	}

	// Give the HTTP request a safety margin over the command execution timeout
	// so that Worker timeout cleanup has time to return a clean timed_out response.
	httpTimeout := req.Timeout + httpTimeoutEnvelope
	execCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, execURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("failed to create exec request: %w", err)
	}
	httpReq.Header.Set("X-Auth-Token", c.authToken)
	httpReq.Header.Set("Content-Type", "application/json")

	// Audit logging: command length, command sha256 hash, session uuid short hash.
	// Raw command and auth token are never logged.
	cmdHash := sha256.Sum256([]byte(req.Command))
	uuidShort := userUUID
	if len(uuidShort) > 8 {
		uuidShort = uuidShort[:8]
	}
	startTime := time.Now()

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		logs.Warn(logs.SYSTEM, fmt.Sprintf(
			"沙箱命令执行网络失败 [session: %s, cmd_len: %d, cmd_hash: %s]: %v",
			uuidShort, len(req.Command), hex.EncodeToString(cmdHash[:4]), err,
		))
		return sandbox.ExecResult{}, fmt.Errorf("sandbox execution request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody := readBoundedString(resp.Body, maxErrorBodyBytes)
		safeErr := sanitizeGatewayExecError(resp.StatusCode, errBody, req, c.authToken)
		logs.Warn(logs.SYSTEM, fmt.Sprintf(
			"沙箱网关返回错误状态 [status: %d, session: %s, cmd_len: %d]: %s",
			resp.StatusCode, uuidShort, len(req.Command), safeErr,
		))
		return sandbox.ExecResult{}, fmt.Errorf(
			"sandbox gateway returned HTTP %d: %s",
			resp.StatusCode,
			safeErr,
		)
	}

	limitedReader := io.LimitReader(resp.Body, maxResponseBodyBytes)
	var gatewayResp gatewayExecResponse
	if err := json.NewDecoder(limitedReader).Decode(&gatewayResp); err != nil {
		return sandbox.ExecResult{}, fmt.Errorf("failed to decode sandbox gateway response: %w", err)
	}

	// Validate execution-state invariants according to adapter contract:
	// 1. Completed: exit_code != nil && timed_out == false
	// 2. Timed out: exit_code == nil && timed_out == true
	// Reject impossible or incomplete combinations as gateway protocol failures.
	if gatewayResp.TimedOut {
		if gatewayResp.ExitCode != nil {
			return sandbox.ExecResult{}, fmt.Errorf(
				"malformed gateway response: timed_out is true but exit_code is non-nil (%d)",
				*gatewayResp.ExitCode,
			)
		}
	} else {
		if gatewayResp.ExitCode == nil {
			return sandbox.ExecResult{}, errors.New(
				"malformed gateway response: exit_code is null but timed_out is false",
			)
		}
	}

	execDuration := time.Since(startTime)
	if gatewayResp.DurationMs > 0 {
		execDuration = time.Duration(gatewayResp.DurationMs) * time.Millisecond
	}

	exitCodeStr := "null"
	if gatewayResp.ExitCode != nil {
		exitCodeStr = fmt.Sprintf("%d", *gatewayResp.ExitCode)
	}

	logs.Info(logs.SYSTEM, fmt.Sprintf(
		"✓ 沙箱命令执行完成 [session: %s, exit_code: %s, timed_out: %t, duration: %s]",
		uuidShort, exitCodeStr, gatewayResp.TimedOut, execDuration,
	))

	return sandbox.ExecResult{
		Stdout:          gatewayResp.Stdout,
		Stderr:          gatewayResp.Stderr,
		ExitCode:        gatewayResp.ExitCode,
		TimedOut:        gatewayResp.TimedOut,
		StdoutTruncated: gatewayResp.StdoutTruncated,
		StderrTruncated: gatewayResp.StderrTruncated,
		Duration:        execDuration,
	}, nil
}

func readBoundedString(r io.Reader, limit int64) string {
	lr := io.LimitReader(r, limit)
	b, err := io.ReadAll(lr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func sanitizeError(msg, token string) string {
	trimmed := strings.TrimSpace(msg)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, "<") || strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return "[HTML error response suppressed]"
	}
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "[REDACTED]")
	}
	msg = bearerTokenPattern.ReplaceAllString(msg, "Bearer [REDACTED]")
	// Also prevent control character leaks or overlong strings
	if len(msg) > 512 {
		msg = msg[:512] + "..."
	}
	return msg
}

// sanitizeGatewayExecError extracts a safe, redacted summary from non-200 Gateway response bodies.
// It treats error bodies as untrusted: drops FastAPI/Pydantic validation "input" fields,
// scrubs known sensitive tokens (auth token, command, cwd, bearer credentials), suppresses HTML error pages, and
// falls back to safe HTTP status text for unparseable bodies.
func sanitizeGatewayExecError(statusCode int, rawBody string, req sandbox.ExecRequest, authToken string) string {
	trimmed := strings.TrimSpace(rawBody)
	if trimmed == "" {
		statusText := http.StatusText(statusCode)
		if statusText == "" {
			return fmt.Sprintf("HTTP %d", statusCode)
		}
		return statusText
	}

	scrub := func(s string) string {
		if authToken != "" {
			s = strings.ReplaceAll(s, authToken, "[REDACTED]")
		}
		if req.Command != "" {
			s = strings.ReplaceAll(s, req.Command, "[REDACTED command]")
		}
		if req.Cwd != "" && req.Cwd != "/sandbox" {
			s = strings.ReplaceAll(s, req.Cwd, "[REDACTED cwd]")
		}
		s = bearerTokenPattern.ReplaceAllString(s, "Bearer [REDACTED]")
		if len(s) > 512 {
			s = s[:512] + "..."
		}
		return s
	}

	// 1. Suppress HTML / XML error pages (e.g. 502/504 Bad Gateway from proxies)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, "<") || strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		statusText := http.StatusText(statusCode)
		if statusText == "" {
			return fmt.Sprintf("HTTP %d", statusCode)
		}
		return statusText
	}

	// 2. Try to parse as FastAPI/Pydantic validation error:
	// {"detail": [{"loc": [...], "msg": "...", "type": "..."}]}
	// "input" is deliberately omitted from this struct so it is discarded by the JSON decoder.
	var pydanticErr struct {
		Detail []struct {
			Loc  []any  `json:"loc"`
			Msg  string `json:"msg"`
			Type string `json:"type"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(trimmed), &pydanticErr); err == nil && len(pydanticErr.Detail) > 0 {
		var items []string
		for _, d := range pydanticErr.Detail {
			var locStrs []string
			for _, loc := range d.Loc {
				locStrs = append(locStrs, fmt.Sprint(loc))
			}
			locStr := strings.Join(locStrs, ".")
			if locStr == "" {
				locStr = "request"
			}
			msg := scrub(d.Msg)
			typeStr := scrub(d.Type)
			if typeStr != "" {
				items = append(items, fmt.Sprintf("%s: %s (type=%s)", locStr, msg, typeStr))
			} else {
				items = append(items, fmt.Sprintf("%s: %s", locStr, msg))
			}
		}
		return "validation error: " + strings.Join(items, "; ")
	}

	// For 422 Unprocessable Entity, if it didn't match the standard Pydantic error list,
	// do NOT trust raw unvalidated content that might reflect user input.
	if statusCode == http.StatusUnprocessableEntity {
		var simpleErr struct {
			Detail *string `json:"detail"`
		}
		if err := json.Unmarshal([]byte(trimmed), &simpleErr); err == nil && simpleErr.Detail != nil && *simpleErr.Detail != "" {
			return "validation error: " + scrub(*simpleErr.Detail)
		}
		return "validation error (unprocessable entity)"
	}

	// 3. Try to parse as simple JSON error: {"detail": "..."} or {"message": "..."} or {"error": "..."}
	var simpleErr struct {
		Detail  *string `json:"detail"`
		Message *string `json:"message"`
		Error   *string `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &simpleErr); err == nil {
		var msg string
		if simpleErr.Detail != nil && *simpleErr.Detail != "" {
			msg = *simpleErr.Detail
		} else if simpleErr.Message != nil && *simpleErr.Message != "" {
			msg = *simpleErr.Message
		} else if simpleErr.Error != nil && *simpleErr.Error != "" {
			msg = *simpleErr.Error
		}
		if msg != "" {
			return scrub(msg)
		}
	}

	// 4. Fallback for plain text: scrub and bound length
	return scrub(trimmed)
}
