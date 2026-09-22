package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Canonical sandbox profile identifiers.
const (
	ProfileGoBuilder     = "go-builder"
	ProfileActionRuntime = "action-runtime"
	ProfileMinimal       = "minimal"
)

// ReadinessStatus classifies the state of a sandbox gateway.
type ReadinessStatus string

const (
	// StatusReady indicates the gateway is reachable, authenticated, implements
	// the required session/exec/release contract, and supports all requested profiles.
	StatusReady ReadinessStatus = "ready"

	// StatusUnprobed indicates the sandbox gateway is configured, but active
	// diagnostic probing has not been executed yet.
	StatusUnprobed ReadinessStatus = "unprobed"

	// StatusEndpointUnreachable indicates the gateway endpoint could not be contacted
	// (e.g. network down, connection refused, DNS failure, timeout, or upstream 5xx error).
	StatusEndpointUnreachable ReadinessStatus = "endpoint_unreachable"

	// StatusAuthFailure indicates the gateway is reachable but rejected the provided credentials.
	StatusAuthFailure ReadinessStatus = "auth_failure"

	// StatusAPIContractMissing indicates the gateway responded but does not implement the
	// required ActionsCat/FrostAgent sandbox API contract (e.g. 404 on /api/v1/sessions or /shell/exec, unexpected status on /release, or conformance failure).
	StatusAPIContractMissing ReadinessStatus = "api_contract_missing"

	// StatusProfileUnsupported indicates the gateway implements the contract but rejected
	// one or more required execution profiles (e.g. go-builder or action-runtime) or missing toolchains.
	StatusProfileUnsupported ReadinessStatus = "profile_unsupported"
)

// ReadinessReport contains detailed diagnostic results for a sandbox gateway.
type ReadinessReport struct {
	Status            ReadinessStatus `json:"status"`
	Endpoint          string          `json:"endpoint"`
	Healthy           bool            `json:"healthy"`
	Authenticated     bool            `json:"authenticated"`
	ContractSupported bool            `json:"contract_supported"`
	ProfilesSupported map[string]bool `json:"profiles_supported"`
	Detail            string          `json:"detail,omitempty"`
	CheckedAt         time.Time       `json:"checked_at"`
}

// SessionResponse captures the required conformance fields of a created sandbox session.
type SessionResponse struct {
	UserUUID string `json:"user_uuid"`
	Profile  string `json:"profile"`
	Network  string `json:"network,omitempty"`
	Status   string `json:"status"`
}

func newProbeUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // RFC 4122 Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 Variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ValidateSessionResponse validates that the gateway returned a compliant session object
// adhering to the ActionsCat session contract.
func ValidateSessionResponse(body []byte, expectedUUID, expectedProfile, expectedNetwork string) (*SessionResponse, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty session response body")
	}
	var resp SessionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("malformed JSON session response: %w", err)
	}
	if strings.TrimSpace(resp.UserUUID) == "" || strings.TrimSpace(resp.Profile) == "" || strings.TrimSpace(resp.Status) == "" {
		return nil, fmt.Errorf("missing required fields in session response (user_uuid, profile, status)")
	}
	if resp.UserUUID != expectedUUID {
		return nil, fmt.Errorf("user_uuid mismatch: expected %q, got %q", expectedUUID, resp.UserUUID)
	}
	if resp.Profile != expectedProfile {
		return nil, fmt.Errorf("profile mismatch: expected %q, got %q", expectedProfile, resp.Profile)
	}
	if expectedNetwork != "" {
		if resp.Network == "" {
			return nil, fmt.Errorf("missing network policy in session response: expected %q", expectedNetwork)
		}
		if resp.Network != expectedNetwork {
			return nil, fmt.Errorf("network policy mismatch: expected %q, got %q", expectedNetwork, resp.Network)
		}
	}
	if resp.Status != "ready" && resp.Status != "created" {
		return nil, fmt.Errorf("session status not ready/created: got %q", resp.Status)
	}
	return &resp, nil
}

type probeExecReq struct {
	Command string  `json:"command"`
	Cwd     string  `json:"cwd,omitempty"`
	Timeout float64 `json:"timeout"`
}

type probeExecResp struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode *int   `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}

func probeShellExec(ctx context.Context, client *http.Client, endpoint, authToken, userUUID, command, profile, network string) (*probeExecResp, int, string, error, error) {
	params := url.Values{}
	params.Set("user_uuid", userUUID)
	if profile != "" {
		params.Set("profile", profile)
	}
	if network != "" {
		params.Set("network", network)
	}
	execURL := fmt.Sprintf("%s/api/v1/shell/exec?%s", endpoint, params.Encode())
	bodyData, _ := json.Marshal(probeExecReq{
		Command: command,
		Cwd:     "/sandbox",
		Timeout: 10.0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, execURL, bytes.NewReader(bodyData))
	if err != nil {
		return nil, 0, "", nil, fmt.Errorf("failed to build shell/exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("X-Auth-Token", authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, resp.StatusCode, "", err, nil
	}
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, bodyStr, nil, nil
	}

	var res probeExecResp
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, resp.StatusCode, bodyStr, nil, fmt.Errorf("malformed JSON in shell/exec response: %w", err)
	}
	if res.ExitCode == nil {
		return nil, resp.StatusCode, bodyStr, nil, fmt.Errorf("shell/exec response missing exit_code")
	}

	return &res, http.StatusOK, bodyStr, nil, nil
}

func probeRelease(ctx context.Context, client *http.Client, endpoint, authToken, userUUID string) (int, string, error) {
	relURL := fmt.Sprintf("%s/api/v1/release?user_uuid=%s", endpoint, url.QueryEscape(userUUID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("failed to build release request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("X-Auth-Token", authToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return resp.StatusCode, "", err
	}
	bodyStr := strings.TrimSpace(string(body))

	// 200 OK, 204 No Content, and 404 Not Found (idempotent release) are all release success in production.
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, "", nil
	}
	return resp.StatusCode, bodyStr, nil
}

func releaseProbeSession(client *http.Client, endpoint, authToken, userUUID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _ = probeRelease(ctx, client, endpoint, authToken, userUUID)
}

// CheckReadiness performs active multi-phase probing of a sandbox gateway to distinguish:
// 1. endpoint unreachable / 5xx server error
// 2. auth failure
// 3. API contract missing (/status, /sessions or /shell/exec 404, unexpected /release status, or conformance failure)
// 4. profile unsupported (e.g. go-builder toolchain missing, action-runtime rejected)
func CheckReadiness(ctx context.Context, endpoint, authToken string, profiles ...string) *ReadinessReport {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	return CheckReadinessWithClient(ctx, client, endpoint, authToken, profiles...)
}

// CheckReadinessWithClient performs active probing using the supplied HTTP client.
func CheckReadinessWithClient(ctx context.Context, client *http.Client, endpoint, authToken string, profiles ...string) *ReadinessReport {
	now := time.Now().UTC()
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            "gateway endpoint URL is empty",
			CheckedAt:         now,
		}
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("invalid gateway URL %q: scheme must be http or https", endpoint),
			CheckedAt:         now,
		}
	}

	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	// 1. Probe /api/v1/status (Reachable & Auth)
	statusURL := endpoint + "/api/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("failed to build status request: %v", err),
			CheckedAt:         now,
		}
	}
	if authToken != "" {
		req.Header.Set("X-Auth-Token", authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("endpoint unreachable: %v", err),
			CheckedAt:         now,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &ReadinessReport{
			Status:            StatusAuthFailure,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("authentication failed: gateway returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway internal server error on /api/v1/status (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           false,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d on /api/v1/status: %s", resp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	// 2. Probe Full Lifecycle Contract (/sessions + /shell/exec + /release)
	contractProbeUUID := newProbeUUID()
	var contractReleased bool
	defer func() {
		if !contractReleased {
			releaseProbeSession(client, endpoint, authToken, contractProbeUUID)
		}
	}()

	sessionsURL := endpoint + "/api/v1/sessions"
	probePayload := map[string]any{
		"user_uuid": contractProbeUUID,
		"profile":   ProfileMinimal,
		"network":   "none",
	}
	probeBody, _ := json.Marshal(probePayload)

	probeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionsURL, bytes.NewReader(probeBody))
	if err != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("failed to build sessions probe request: %v", err),
			CheckedAt:         now,
		}
	}
	probeReq.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		probeReq.Header.Set("X-Auth-Token", authToken)
	}

	probeResp, err := client.Do(probeReq)
	if err != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("transport error probing /api/v1/sessions: %v", err),
			CheckedAt:         now,
		}
	}
	defer probeResp.Body.Close()

	if probeResp.StatusCode == http.StatusNotFound {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            "gateway returned 404 on /api/v1/sessions: compatible ActionsCat/FrostAgent session contract missing",
			CheckedAt:         now,
		}
	}

	if probeResp.StatusCode == http.StatusUnauthorized || probeResp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(probeResp.Body, 512))
		return &ReadinessReport{
			Status:            StatusAuthFailure,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("authentication failed on /api/v1/sessions (HTTP %d): %s", probeResp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	if probeResp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(probeResp.Body, 512))
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway internal server error on /api/v1/sessions (HTTP %d): %s", probeResp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	if probeResp.StatusCode != http.StatusOK && probeResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(probeResp.Body, 512))
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d on /api/v1/sessions: %s", probeResp.StatusCode, strings.TrimSpace(string(body))),
			CheckedAt:         now,
		}
	}

	// Validate Session Conformance
	sessionRawBody, readErr := io.ReadAll(io.LimitReader(probeResp.Body, 2048))
	if readErr != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("transport error reading session response body: %v", readErr),
			CheckedAt:         now,
		}
	}
	if _, valErr := ValidateSessionResponse(sessionRawBody, contractProbeUUID, ProfileMinimal, "none"); valErr != nil {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway session response conformance failed: %v", valErr),
			CheckedAt:         now,
		}
	}

	// Probe /api/v1/shell/exec on contract session
	execRes, execCode, execErrBody, execTransportErr, execProtocolErr := probeShellExec(ctx, client, endpoint, authToken, contractProbeUUID, "true", ProfileMinimal, "none")
	if execTransportErr != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("transport error probing /api/v1/shell/exec: %v", execTransportErr),
			CheckedAt:         now,
		}
	}
	if execProtocolErr != nil {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("shell/exec conformance error: %v", execProtocolErr),
			CheckedAt:         now,
		}
	}
	if execCode == http.StatusNotFound {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            "gateway returned 404 on /api/v1/shell/exec: exec contract missing",
			CheckedAt:         now,
		}
	}
	if execCode == http.StatusUnauthorized || execCode == http.StatusForbidden {
		return &ReadinessReport{
			Status:            StatusAuthFailure,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("authentication failed on /api/v1/shell/exec (HTTP %d)", execCode),
			CheckedAt:         now,
		}
	}
	if execCode >= 500 {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway internal server error on /api/v1/shell/exec (HTTP %d): %s", execCode, execErrBody),
			CheckedAt:         now,
		}
	}
	if execCode != http.StatusOK {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d on /api/v1/shell/exec: %s", execCode, execErrBody),
			CheckedAt:         now,
		}
	}
	if execRes == nil || execRes.ExitCode == nil || *execRes.ExitCode != 0 || execRes.TimedOut {
		detail := "gateway shell/exec failed to execute basic command successfully"
		if execRes != nil && execRes.TimedOut {
			detail = "gateway shell/exec command timed out"
		}
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            detail,
			CheckedAt:         now,
		}
	}

	// Probe /api/v1/release on contract session
	relCode, relErrBody, relErr := probeRelease(ctx, client, endpoint, authToken, contractProbeUUID)
	if relErr != nil {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("transport error probing /api/v1/release: %v", relErr),
			CheckedAt:         now,
		}
	}
	if relCode == http.StatusUnauthorized || relCode == http.StatusForbidden {
		return &ReadinessReport{
			Status:            StatusAuthFailure,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     false,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("authentication failed on /api/v1/release (HTTP %d)", relCode),
			CheckedAt:         now,
		}
	}
	if relCode >= 500 {
		return &ReadinessReport{
			Status:            StatusEndpointUnreachable,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway internal server error on /api/v1/release (HTTP %d): %s", relCode, relErrBody),
			CheckedAt:         now,
		}
	}
	if relCode != http.StatusNoContent && relCode != http.StatusOK && relCode != http.StatusNotFound {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d on /api/v1/release (expected 200, 204, or 404): %s", relCode, relErrBody),
			CheckedAt:         now,
		}
	}
	contractReleased = true

	// 3. Probe Requested Profiles (with toolchain execution verification)
	if len(profiles) == 0 {
		profiles = []string{ProfileGoBuilder, ProfileActionRuntime}
	}

	profilesMap := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		profilesMap[p] = false
	}

	for _, profile := range profiles {
		profileUUID := newProbeUUID()
		var profileReleased bool
		defer func(u string, r *bool) {
			if !*r {
				releaseProbeSession(client, endpoint, authToken, u)
			}
		}(profileUUID, &profileReleased)

		payload := map[string]any{
			"user_uuid": profileUUID,
			"profile":   profile,
			"network":   "none",
		}
		pBody, _ := json.Marshal(payload)
		pReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionsURL, bytes.NewReader(pBody))
		if err != nil {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("failed to build profile probe for %q: %v", profile, err),
				CheckedAt:         now,
			}
		}
		pReq.Header.Set("Content-Type", "application/json")
		if authToken != "" {
			pReq.Header.Set("X-Auth-Token", authToken)
		}

		pResp, err := client.Do(pReq)
		if err != nil {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("transport error probing profile %q: %v", profile, err),
				CheckedAt:         now,
			}
		}
		defer pResp.Body.Close()

		if pResp.StatusCode == http.StatusBadRequest || pResp.StatusCode == http.StatusUnprocessableEntity {
			body, _ := io.ReadAll(io.LimitReader(pResp.Body, 512))
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway rejected profile %q (HTTP %d): %s", profile, pResp.StatusCode, strings.TrimSpace(string(body))),
				CheckedAt:         now,
			}
		}

		if pResp.StatusCode == http.StatusUnauthorized || pResp.StatusCode == http.StatusForbidden {
			body, _ := io.ReadAll(io.LimitReader(pResp.Body, 512))
			return &ReadinessReport{
				Status:            StatusAuthFailure,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     false,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("authentication failed probing profile %q (HTTP %d): %s", profile, pResp.StatusCode, strings.TrimSpace(string(body))),
				CheckedAt:         now,
			}
		}

		if pResp.StatusCode >= 500 {
			body, _ := io.ReadAll(io.LimitReader(pResp.Body, 512))
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway internal server error provisioning profile %q (HTTP %d): %s", profile, pResp.StatusCode, strings.TrimSpace(string(body))),
				CheckedAt:         now,
			}
		}

		if pResp.StatusCode != http.StatusOK && pResp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(io.LimitReader(pResp.Body, 512))
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway failed to provision profile %q (HTTP %d): %s", profile, pResp.StatusCode, strings.TrimSpace(string(body))),
				CheckedAt:         now,
			}
		}

		// Verify response payload conformance for requested profile
		pRawBody, pReadErr := io.ReadAll(io.LimitReader(pResp.Body, 2048))
		if pReadErr != nil {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("transport error reading profile %q session response body: %v", profile, pReadErr),
				CheckedAt:         now,
			}
		}
		if _, pValErr := ValidateSessionResponse(pRawBody, profileUUID, profile, "none"); pValErr != nil {
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("session conformance validation failed for profile %q: %v", profile, pValErr),
				CheckedAt:         now,
			}
		}

		// Toolchain & execution verification inside the profile
		probeCmd := "true"
		if profile == ProfileGoBuilder {
			probeCmd = "go version"
		}
		pExecRes, pExecCode, pExecErrBody, pExecTransportErr, pExecProtocolErr := probeShellExec(ctx, client, endpoint, authToken, profileUUID, probeCmd, profile, "none")
		if pExecTransportErr != nil {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("transport error executing verification inside profile %q: %v", profile, pExecTransportErr),
				CheckedAt:         now,
			}
		}
		if pExecProtocolErr != nil {
			return &ReadinessReport{
				Status:            StatusAPIContractMissing,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway shell/exec conformance error inside profile %q: %v", profile, pExecProtocolErr),
				CheckedAt:         now,
			}
		}
		if pExecCode >= 500 {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway server error executing verification inside profile %q (HTTP %d): %s", profile, pExecCode, pExecErrBody),
				CheckedAt:         now,
			}
		}
		if pExecCode == http.StatusNotFound {
			return &ReadinessReport{
				Status:            StatusAPIContractMissing,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway returned 404 on /api/v1/shell/exec for profile %q: exec contract missing", profile),
				CheckedAt:         now,
			}
		}
		if pExecCode == http.StatusUnauthorized || pExecCode == http.StatusForbidden {
			return &ReadinessReport{
				Status:            StatusAuthFailure,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     false,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("authentication failed executing verification inside profile %q (HTTP %d)", profile, pExecCode),
				CheckedAt:         now,
			}
		}
		if pExecCode != http.StatusOK {
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("verification execution returned HTTP %d for profile %q: %s", pExecCode, profile, pExecErrBody),
				CheckedAt:         now,
			}
		}
		if pExecRes == nil || pExecRes.ExitCode == nil || *pExecRes.ExitCode != 0 || pExecRes.TimedOut {
			exitCode := -1
			if pExecRes != nil && pExecRes.ExitCode != nil {
				exitCode = *pExecRes.ExitCode
			}
			if profile == ProfileGoBuilder {
				detail := fmt.Sprintf("go-builder profile missing working Go toolchain (go version exited with code %d)", exitCode)
				if pExecRes != nil && pExecRes.TimedOut {
					detail = "go-builder profile verification command timed out"
				}
				return &ReadinessReport{
					Status:            StatusProfileUnsupported,
					Endpoint:          endpoint,
					Healthy:           true,
					Authenticated:     true,
					ContractSupported: true,
					ProfilesSupported: profilesMap,
					Detail:            detail,
					CheckedAt:         now,
				}
			}
			detail := fmt.Sprintf("execution verification failed for profile %q (exit code %d)", profile, exitCode)
			if pExecRes != nil && pExecRes.TimedOut {
				detail = fmt.Sprintf("execution verification timed out for profile %q", profile)
			}
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            detail,
				CheckedAt:         now,
			}
		}

		// Explicit release check for profile session
		pRelCode, pRelErrBody, pRelErr := probeRelease(ctx, client, endpoint, authToken, profileUUID)
		if pRelErr != nil {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("transport error releasing profile %q session: %v", profile, pRelErr),
				CheckedAt:         now,
			}
		}
		if pRelCode == http.StatusUnauthorized || pRelCode == http.StatusForbidden {
			return &ReadinessReport{
				Status:            StatusAuthFailure,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     false,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("authentication failed releasing profile %q session (HTTP %d)", profile, pRelCode),
				CheckedAt:         now,
			}
		}
		if pRelCode >= 500 {
			return &ReadinessReport{
				Status:            StatusEndpointUnreachable,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway internal server error releasing profile %q session (HTTP %d): %s", profile, pRelCode, pRelErrBody),
				CheckedAt:         now,
			}
		}
		if pRelCode != http.StatusNoContent && pRelCode != http.StatusOK && pRelCode != http.StatusNotFound {
			return &ReadinessReport{
				Status:            StatusAPIContractMissing,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d releasing profile %q session (expected 200, 204, or 404): %s", pRelCode, profile, pRelErrBody),
				CheckedAt:         now,
			}
		}
		profileReleased = true

		profilesMap[profile] = true
	}

	return &ReadinessReport{
		Status:            StatusReady,
		Endpoint:          endpoint,
		Healthy:           true,
		Authenticated:     true,
		ContractSupported: true,
		ProfilesSupported: profilesMap,
		Detail:            "gateway is operational, verified sessions/exec/release contract and toolchains for all requested profiles",
		CheckedAt:         now,
	}
}
