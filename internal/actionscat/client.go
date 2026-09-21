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
	defaultBuildTimeout  = 180 * time.Second
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
	// ErrBuildUnknownResult is returned when a build request times out or encounters a transport error,
	// indicating the build outcome is indeterminate (the backend may still be compiling or already completed).
	ErrBuildUnknownResult = errors.New("actionscat: build request outcome is unknown (timeout or transport interruption; server may still be compiling)")
	// ErrBuildTimeoutUnknownResult is maintained for backward compatibility.
	ErrBuildTimeoutUnknownResult = ErrBuildUnknownResult
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

// IsRunnable reports whether the action is enabled and has both an active version and an active build.
func (a *Action) IsRunnable() bool {
	return a != nil && a.Enabled && a.ActiveVersionID != "" && a.ActiveBuildID != ""
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

// CreateActionReq defines the parameters for creating a new action in ActionsCat.
type CreateActionReq struct {
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	MaxConcurrency int    `json:"max_concurrency,omitempty"`
}

// BuildSpec describes how to build source code into an artifact bundle.
type BuildSpec struct {
	Language             string `json:"language,omitempty"`              // e.g. "go"
	ToolchainRequirement string `json:"toolchain_requirement,omitempty"` // e.g. ">= 1.25"
	Command              string `json:"command,omitempty"`               // e.g. "go build -o /out/entrypoint ."
	Network              bool   `json:"network,omitempty"`               // whether build requires public network access
}

// NetworkAllowRule specifies a host and port allowlist entry.
type NetworkAllowRule struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// NetworkPolicy describes network access permissions for a Run.
type NetworkPolicy struct {
	Mode  string             `json:"mode"`            // "none", "public", "allowlist", "isolated"
	Allow []NetworkAllowRule `json:"allow,omitempty"` // populated when mode is "allowlist"
}

// RuntimeSpec describes execution constraints and entrypoint for a Run.
type RuntimeSpec struct {
	Entrypoint     string        `json:"entrypoint,omitempty"`      // path to entrypoint executable within artifact (e.g. "entrypoint")
	Network        NetworkPolicy `json:"network,omitempty"`         // network access policy
	TimeoutSeconds int           `json:"timeout_seconds,omitempty"` // default execution timeout
	MemoryLimitMB  int           `json:"memory_limit_mb,omitempty"` // worker RAM limit
	CPULimit       float64       `json:"cpu_limit,omitempty"`       // worker CPU cores limit
}

// StateInjection declares a persistent state file to read and inject as an env var before Run.
type StateInjection struct {
	StatePath string `json:"state_path"`           // relative path inside action's state namespace, e.g. "html.json"
	EnvVar    string `json:"env_var"`              // target env var name, e.g. "PREVIOUS_HTML"
	Optional  bool   `json:"optional,omitempty"`   // if true, empty string if missing; if false, fail run if missing
}

// ActionVersion represents an immutable version snapshot of an Action.
type ActionVersion struct {
	ID                  string           `json:"id"`
	ActionID            string           `json:"action_id"`
	VersionNumber       int              `json:"version_number"`
	SourceDigest        string           `json:"source_digest"`
	SourcePath          string           `json:"source_path"`
	BuildSpec           BuildSpec        `json:"build_spec"`
	RuntimeSpec         RuntimeSpec      `json:"runtime_spec"`
	StateInjections     []StateInjection `json:"state_injections"`
	RuntimeCapabilities []string         `json:"runtime_capabilities"`
	CreatedAt           time.Time        `json:"created_at"`
}

// ArtifactBuild represents the output of building a specific ActionVersion.
type ArtifactBuild struct {
	ID               string     `json:"id"`
	ActionID         string     `json:"action_id"`
	VersionID        string     `json:"version_id"`
	BuildNumber      int        `json:"build_number"`
	Status           string     `json:"status"` // "succeeded", "failed", "timed_out", "building", "pending"
	BuilderProfile   string     `json:"builder_profile"`
	ToolchainVersion string     `json:"toolchain_version"`
	BuildCommand     string     `json:"build_command"`
	Stdout           string     `json:"stdout"`
	Stderr           string     `json:"stderr"`
	ExitCode         *int       `json:"exit_code,omitempty"`
	ArtifactDigest   string     `json:"artifact_digest"`
	ArtifactPath     string     `json:"artifact_path"`
	ArtifactSize     int64      `json:"artifact_size"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// BuildLogs represents stdout and stderr logs for an artifact build.
type BuildLogs struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// CreateVersionReq represents the payload for creating a new ActionVersion.
type CreateVersionReq struct {
	Files               map[string]string `json:"files"`
	Encodings           map[string]string `json:"encodings,omitempty"`
	BuildSpec           BuildSpec         `json:"build_spec"`
	RuntimeSpec         RuntimeSpec       `json:"runtime_spec"`
	StateInjections     []StateInjection  `json:"state_injections,omitempty"`
	RuntimeCapabilities []string          `json:"runtime_capabilities,omitempty"`
}

// SetActiveBuildReq is the payload for activating a build.
type SetActiveBuildReq struct {
	VersionID string `json:"version_id"`
	BuildID   string `json:"build_id"`
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
			// Note: timeout is managed per-request via context deadlines
			// (defaultTimeout for metadata, defaultBuildTimeout for synchronous compilation)
			// so that long-running builds are not truncated by a client-level limit.
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
	return c.doRequestWithTimeout(ctx, method, path, body, token, defaultTimeout)
}

func (c *Client) doRequestWithTimeout(ctx context.Context, method, path string, body any, token string, timeout time.Duration) ([]byte, int, error) {
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

	reqCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		if deadline, hasDeadline := ctx.Deadline(); !hasDeadline {
			reqCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		} else if time.Until(deadline) > timeout {
			reqCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	req, err := http.NewRequestWithContext(reqCtx, method, reqURL, reqBody)
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

// CreateAction creates a new action in ActionsCat.
func (c *Client) CreateAction(ctx context.Context, req CreateActionReq) (*Action, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("actionscat: action name cannot be empty")
	}
	data, _, err := c.doRequest(ctx, http.MethodPost, "/api/v1/actions", req, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var action Action
	if err := json.Unmarshal(data, &action); err != nil {
		return nil, fmt.Errorf("actionscat: parse action: %w", err)
	}
	return &action, nil
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

// CreateVersion creates a new immutable version snapshot for an action.
func (c *Client) CreateVersion(ctx context.Context, actionID string, req CreateVersionReq) (*ActionVersion, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	if len(req.Files) == 0 {
		return nil, errors.New("actionscat: files cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/versions", url.PathEscape(actionID))
	data, _, err := c.doRequest(ctx, http.MethodPost, path, req, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var ver ActionVersion
	if err := json.Unmarshal(data, &ver); err != nil {
		return nil, fmt.Errorf("actionscat: parse version: %w", err)
	}
	return &ver, nil
}

// ListVersions retrieves all versions for an action.
func (c *Client) ListVersions(ctx context.Context, actionID string) ([]ActionVersion, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/versions", url.PathEscape(actionID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var vers []ActionVersion
	if err := json.Unmarshal(data, &vers); err != nil {
		return nil, fmt.Errorf("actionscat: parse versions: %w", err)
	}
	return vers, nil
}

// GetVersion retrieves a specific version for an action.
func (c *Client) GetVersion(ctx context.Context, actionID, versionID string) (*ActionVersion, error) {
	if actionID == "" || versionID == "" {
		return nil, errors.New("actionscat: actionID and versionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/versions/%s", url.PathEscape(actionID), url.PathEscape(versionID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var ver ActionVersion
	if err := json.Unmarshal(data, &ver); err != nil {
		return nil, fmt.Errorf("actionscat: parse version: %w", err)
	}
	return &ver, nil
}

// BuildVersion requests compilation of a specific version into an artifact build.
// Note: build is a synchronous, long-running operation in ActionsCat that can take up to 120s+;
// this client uses a dedicated long timeout (defaultBuildTimeout = 180s) and does NOT retry on transport errors or timeout.
// Any transport interruption (deadline exceeded, connection drop, severed body) returns ErrBuildUnknownResult.
func (c *Client) BuildVersion(ctx context.Context, actionID, versionID string) (*ArtifactBuild, error) {
	if actionID == "" || versionID == "" {
		return nil, errors.New("actionscat: actionID and versionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/versions/%s/builds", url.PathEscape(actionID), url.PathEscape(versionID))
	data, statusCode, err := c.doRequestWithTimeout(ctx, http.MethodPost, path, nil, c.ManagementToken(), defaultBuildTimeout)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) || errors.Is(err, ErrInvalidURL) {
			return nil, err
		}
		if statusCode > 0 && !strings.Contains(err.Error(), "read response") {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrBuildUnknownResult, err)
	}
	var bld ArtifactBuild
	if err := json.Unmarshal(data, &bld); err != nil {
		return nil, fmt.Errorf("actionscat: parse build: %w", err)
	}
	return &bld, nil
}

// ListBuilds lists all artifact builds for an action, ordered by creation time descending.
func (c *Client) ListBuilds(ctx context.Context, actionID string) ([]ArtifactBuild, error) {
	if actionID == "" {
		return nil, errors.New("actionscat: actionID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/builds", url.PathEscape(actionID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var builds []ArtifactBuild
	if err := json.Unmarshal(data, &builds); err != nil {
		return nil, fmt.Errorf("actionscat: parse builds: %w", err)
	}
	return builds, nil
}

// GetBuild retrieves a specific artifact build for an action.
func (c *Client) GetBuild(ctx context.Context, actionID, buildID string) (*ArtifactBuild, error) {
	if actionID == "" || buildID == "" {
		return nil, errors.New("actionscat: actionID and buildID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/builds/%s", url.PathEscape(actionID), url.PathEscape(buildID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var bld ArtifactBuild
	if err := json.Unmarshal(data, &bld); err != nil {
		return nil, fmt.Errorf("actionscat: parse build: %w", err)
	}
	return &bld, nil
}

// GetBuildLogs retrieves stdout and stderr logs for a specific build.
func (c *Client) GetBuildLogs(ctx context.Context, actionID, buildID string) (*BuildLogs, error) {
	if actionID == "" || buildID == "" {
		return nil, errors.New("actionscat: actionID and buildID cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/builds/%s/logs", url.PathEscape(actionID), url.PathEscape(buildID))
	data, _, err := c.doRequest(ctx, http.MethodGet, path, nil, c.ManagementToken())
	if err != nil {
		return nil, err
	}
	var logs BuildLogs
	if err := json.Unmarshal(data, &logs); err != nil {
		return nil, fmt.Errorf("actionscat: parse build logs: %w", err)
	}
	return &logs, nil
}

// ActivateBuild marks a succeeded build as the active version/build for an action.
func (c *Client) ActivateBuild(ctx context.Context, actionID string, req SetActiveBuildReq) error {
	if actionID == "" {
		return errors.New("actionscat: actionID cannot be empty")
	}
	if req.VersionID == "" || req.BuildID == "" {
		return errors.New("actionscat: version_id and build_id cannot be empty")
	}
	path := fmt.Sprintf("/api/v1/actions/%s/active-build", url.PathEscape(actionID))
	_, _, err := c.doRequest(ctx, http.MethodPost, path, req, c.ManagementToken())
	return err
}
