package actionscat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout       = 15 * time.Second
	defaultMaxBodyBytes  = 10 * 1024 * 1024 // 10 MiB
	maxWaitDuration      = 30 * time.Second
	defaultPollInterval  = 600 * time.Millisecond
)

var (
	// ErrNotConfigured indicates that ACTIONSCAT_ENDPOINT is unset or empty.
	ErrNotConfigured = errors.New("actionscat: endpoint is not configured")
	// ErrInvalidURL indicates that ACTIONSCAT_ENDPOINT is not a valid HTTP/HTTPS URL.
	ErrInvalidURL = errors.New("actionscat: invalid endpoint url")
	// ErrNotFound indicates that the requested action or run was not found.
	ErrNotFound = errors.New("actionscat: resource not found")
)

// Action represents an ActionsCat managed automation action.
type Action struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	ActiveVersionID string    `json:"active_version_id,omitempty"`
	ActiveBuildID   string    `json:"active_build_id,omitempty"`
	MaxConcurrency  int       `json:"max_concurrency"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Run represents an execution run of an action in ActionsCat.
type Run struct {
	ID              string            `json:"id"`
	ActionID        string            `json:"action_id"`
	ActionVersionID string            `json:"action_version_id"`
	ArtifactBuildID string            `json:"artifact_build_id"`
	TriggerType     string            `json:"trigger_type"`
	TriggerMetadata map[string]string `json:"trigger_metadata,omitempty"`
	PlannedEnv      map[string]string `json:"planned_env,omitempty"`
	Status          string            `json:"status"`
	ExitCode        *int              `json:"exit_code,omitempty"`
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
	DurationMs      int64             `json:"duration_ms"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	StartedAt       *time.Time        `json:"started_at,omitempty"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
}

// IsTerminal returns true if the Run has finished execution.
func (r *Run) IsTerminal() bool {
	switch r.Status {
	case "succeeded", "failed", "timed_out", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

// ManualRunReq is the payload for triggering a manual run.
type ManualRunReq struct {
	ExtraEnv        map[string]string `json:"extra_env,omitempty"`
	TriggerMetadata map[string]string `json:"trigger_metadata,omitempty"`
}

// RunLogs contains stdout and stderr from a run.
type RunLogs struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// HealthStatus indicates ActionsCat health status.
type HealthStatus struct {
	Status string `json:"status"`
}

// StatusResponse is the summarized connection status.
type StatusResponse struct {
	Configured    bool   `json:"configured"`
	Endpoint      string `json:"endpoint"`
	Healthy       bool   `json:"healthy"`
	Authenticated bool   `json:"authenticated"`
	Error         string `json:"error,omitempty"`
}

// HTTPClient defines the minimal interface for issuing HTTP requests.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client interacts with the ActionsCat management and dispatch APIs.
type Client struct {
	getenv     func(string) string
	httpClient HTTPClient
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default HTTP client (useful for tests).
func WithHTTPClient(c HTTPClient) Option {
	return func(cl *Client) {
		cl.httpClient = c
	}
}

// New creates a new ActionsCat client configured with a dynamic environment getter.
func New(getenv func(string) string, opts ...Option) *Client {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	c := &Client{
		getenv: getenv,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Endpoint returns the configured ActionsCat endpoint or empty string.
func (c *Client) Endpoint() string {
	return strings.TrimRight(strings.TrimSpace(c.getenv("ACTIONSCAT_ENDPOINT")), "/")
}

// ManagementToken returns the configured Bearer token.
func (c *Client) ManagementToken() string {
	return strings.TrimSpace(c.getenv("ACTIONSCAT_MANAGEMENT_TOKEN"))
}

// DispatchToken returns the configured dispatch token (falling back to management token).
func (c *Client) DispatchToken() string {
	token := strings.TrimSpace(c.getenv("ACTIONSCAT_DISPATCH_TOKEN"))
	if token == "" {
		token = c.ManagementToken()
	}
	return token
}

// IsConfigured returns true if an endpoint has been specified.
func (c *Client) IsConfigured() bool {
	return c.Endpoint() != ""
}

func (c *Client) validateURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, ErrNotConfigured
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be http or https, got %q", ErrInvalidURL, parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: host cannot be empty", ErrInvalidURL)
	}
	return parsed, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, token string) ([]byte, int, error) {
	endpoint := c.Endpoint()
	if endpoint == "" {
		return nil, 0, ErrNotConfigured
	}
	base, err := c.validateURL(endpoint)
	if err != nil {
		return nil, 0, err
	}

	reqURL := strings.TrimRight(base.String(), "/") + path

	var reqBody io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("actionscat: marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("actionscat: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("actionscat: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("actionscat: read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return respBytes, resp.StatusCode, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errMsg := strings.TrimSpace(string(respBytes))
		var errObj struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBytes, &errObj) == nil && errObj.Error != "" {
			errMsg = errObj.Error
		}
		return respBytes, resp.StatusCode, fmt.Errorf("actionscat: server error (status %d): %s", resp.StatusCode, errMsg)
	}

	return respBytes, resp.StatusCode, nil
}

// Health checks if ActionsCat is reachable and reports healthy.
func (c *Client) Health(ctx context.Context) (*HealthStatus, error) {
	data, _, err := c.doRequest(ctx, http.MethodGet, "/healthz", nil, "")
	if err != nil {
		return nil, err
	}
	var hs HealthStatus
	if err := json.Unmarshal(data, &hs); err != nil {
		// ActionsCat healthz might return string "ok" or JSON {"status":"ok"}
		str := strings.TrimSpace(string(data))
		if str == "ok" || strings.Contains(str, "ok") {
			return &HealthStatus{Status: "ok"}, nil
		}
		return nil, fmt.Errorf("actionscat: parse healthz response: %w", err)
	}
	return &hs, nil
}

// Status returns a high-level overview of the ActionsCat connection status,
// distinguishing between anonymous reachability (/healthz) and management API readiness.
func (c *Client) Status(ctx context.Context) StatusResponse {
	endpoint := c.Endpoint()
	if endpoint == "" {
		return StatusResponse{
			Configured:    false,
			Endpoint:      "",
			Healthy:       false,
			Authenticated: false,
			Error:         "未配置 ACTIONSCAT_ENDPOINT",
		}
	}
	hs, err := c.Health(ctx)
	if err != nil {
		return StatusResponse{
			Configured:    true,
			Endpoint:      endpoint,
			Healthy:       false,
			Authenticated: false,
			Error:         err.Error(),
		}
	}
	if hs.Status != "ok" {
		return StatusResponse{
			Configured:    true,
			Endpoint:      endpoint,
			Healthy:       false,
			Authenticated: false,
			Error:         "健康检查状态异常",
		}
	}

	token := c.ManagementToken()
	if token == "" {
		return StatusResponse{
			Configured:    true,
			Endpoint:      endpoint,
			Healthy:       true,
			Authenticated: false,
			Error:         "未配置 ACTIONSCAT_MANAGEMENT_TOKEN",
		}
	}

	if _, _, err := c.doRequest(ctx, http.MethodGet, "/api/v1/actions?limit=1", nil, token); err != nil {
		return StatusResponse{
			Configured:    true,
			Endpoint:      endpoint,
			Healthy:       true,
			Authenticated: false,
			Error:         fmt.Sprintf("管理凭据认证失败: %v", err),
		}
	}

	return StatusResponse{
		Configured:    true,
		Endpoint:      endpoint,
		Healthy:       true,
		Authenticated: true,
	}
}

// ListActions retrieves all actions from ActionsCat.
func (c *Client) ListActions(ctx context.Context) ([]Action, error) {
	data, _, err := c.doRequest(ctx, http.MethodGet, "/api/v1/actions", nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		return nil, fmt.Errorf("actionscat: parse actions: %w", err)
	}
	return actions, nil
}

// GetAction retrieves a single action by its ID.
func (c *Client) GetAction(ctx context.Context, actionID string) (*Action, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s", url.PathEscape(actionID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var action Action
	if err := json.Unmarshal(data, &action); err != nil {
		return nil, fmt.Errorf("actionscat: parse action: %w", err)
	}
	return &action, nil
}

// TriggerRun starts a new run for the given action.
func (c *Client) TriggerRun(ctx context.Context, actionID string, req ManualRunReq) (*Run, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/runs", url.PathEscape(actionID))
	data, _, err := c.doRequest(ctx, http.MethodPost, path, req, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("actionscat: parse run: %w", err)
	}
	return &run, nil
}

// TriggerRunAndWait triggers a run and polls until completion or timeout.
func (c *Client) TriggerRunAndWait(ctx context.Context, actionID string, req ManualRunReq, waitTimeout time.Duration) (*Run, error) {
	if waitTimeout <= 0 || waitTimeout > maxWaitDuration {
		waitTimeout = maxWaitDuration
	}
	run, err := c.TriggerRun(ctx, actionID, req)
	if err != nil {
		return nil, err
	}
	if run.IsTerminal() {
		return run, nil
	}

	deadline := time.Now().Add(waitTimeout)
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case now := <-ticker.C:
			if now.After(deadline) {
				return run, nil
			}
			latest, err := c.GetRun(ctx, actionID, run.ID)
			if err == nil {
				run = latest
				if run.IsTerminal() {
					return run, nil
				}
			}
		}
	}
}

// ListRuns lists runs for an action with pagination.
func (c *Client) ListRuns(ctx context.Context, actionID string, limit, offset int) ([]Run, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	path := fmt.Sprintf("/api/v1/actions/%s/runs?limit=%s&offset=%s",
		url.PathEscape(actionID),
		strconv.Itoa(limit),
		strconv.Itoa(offset),
	)
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var runs []Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("actionscat: parse runs: %w", err)
	}
	return runs, nil
}

// GetRun retrieves details of a specific run.
func (c *Client) GetRun(ctx context.Context, actionID, runID string) (*Run, error) {
	if actionID == "" || runID == "" {
		return nil, errors.New("actionscat: actionID and runID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/runs/%s", url.PathEscape(actionID), url.PathEscape(runID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("actionscat: parse run: %w", err)
	}
	return &run, nil
}

// GetRunLogs retrieves stdout and stderr for a specific run.
func (c *Client) GetRunLogs(ctx context.Context, actionID, runID string) (*RunLogs, error) {
	if actionID == "" || runID == "" {
		return nil, errors.New("actionscat: actionID and runID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/runs/%s/logs", url.PathEscape(actionID), url.PathEscape(runID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var logs RunLogs
	if err := json.Unmarshal(data, &logs); err != nil {
		return nil, fmt.Errorf("actionscat: parse logs: %w", err)
	}
	return &logs, nil
}

// Dispatch sends an event payload to ActionsCat's event dispatch ingress.
func (c *Client) Dispatch(ctx context.Context, event any) error {
	_, _, err := c.doRequest(ctx, http.MethodPost, "/api/v1/dispatch", event, c.DispatchToken())
	return err
}
