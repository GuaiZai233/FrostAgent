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
	// required ActionsCat/FrostAgent sandbox API contract (e.g. 404 on /api/v1/sessions, /shell/exec, or /release).
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
	if !strings.EqualFold(resp.UserUUID, expectedUUID) {
		return nil, fmt.Errorf("user_uuid mismatch: expected %q, got %q", expectedUUID, resp.UserUUID)
	}
	if resp.Profile != expectedProfile {
		return nil, fmt.Errorf("profile mismatch: expected %q, got %q", expectedProfile, resp.Profile)
	}
	if expectedNetwork != "" && resp.Network != "" && resp.Network != expectedNetwork {
		return nil, fmt.Errorf("network policy mismatch: expected %q, got %q", expectedNetwork, resp.Network)
	}
	st := strings.ToLower(strings.TrimSpace(resp.Status))
	if st != "ready" && st != "created" {
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

func probeShellExec(ctx context.Context, client *http.Client, endpoint, authToken, userUUID, command string) (*probeExecResp, int, string, error) {
	execURL := fmt.Sprintf("%s/api/v1/shell/exec?user_uuid=%s", endpoint, url.QueryEscape(userUUID))
	bodyData, _ := json.Marshal(probeExecReq{
		Command: command,
		Cwd:     "/sandbox",
		Timeout: 10.0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, execURL, bytes.NewReader(bodyData))
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to build shell/exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("X-Auth-Token", authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, bodyStr, nil
	}

	var res probeExecResp
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, resp.StatusCode, bodyStr, fmt.Errorf("malformed JSON in shell/exec response: %w", err)
	}
	if res.ExitCode == nil {
		return nil, resp.StatusCode, bodyStr, fmt.Errorf("shell/exec response missing exit_code")
	}

	return &res, http.StatusOK, bodyStr, nil
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

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	bodyStr := strings.TrimSpace(string(body))

	if resp.StatusCode == http.StatusNoContent {
		return http.StatusNoContent, "", nil
	}
	if resp.StatusCode == http.StatusNotFound {
		lower := strings.ToLower(bodyStr)
		if strings.Contains(lower, "no active session") || strings.Contains(lower, "session not found") {
			return http.StatusNoContent, "", nil
		}
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
// 3. API contract missing (/status, /sessions, /shell/exec, /release 404 or conformance failure)
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
	defer releaseProbeSession(client, endpoint, authToken, contractProbeUUID)

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
	sessionRawBody, _ := io.ReadAll(io.LimitReader(probeResp.Body, 2048))
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
	execRes, execCode, execErrBody, execErr := probeShellExec(ctx, client, endpoint, authToken, contractProbeUUID, "true")
	if execErr != nil {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("shell/exec conformance error: %v", execErr),
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
	if execRes == nil || execRes.ExitCode == nil || *execRes.ExitCode != 0 {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            "gateway shell/exec failed to execute basic command successfully",
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
	if relCode == http.StatusNotFound {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            "gateway returned 404 on /api/v1/release: release contract missing",
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
	if relCode != http.StatusNoContent {
		return &ReadinessReport{
			Status:            StatusAPIContractMissing,
			Endpoint:          endpoint,
			Healthy:           true,
			Authenticated:     true,
			ContractSupported: false,
			ProfilesSupported: make(map[string]bool),
			Detail:            fmt.Sprintf("gateway returned unexpected HTTP %d on /api/v1/release (expected 204): %s", relCode, relErrBody),
			CheckedAt:         now,
		}
	}

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
		defer releaseProbeSession(client, endpoint, authToken, profileUUID)

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
		pRawBody, _ := io.ReadAll(io.LimitReader(pResp.Body, 2048))
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
		pExecRes, pExecCode, pExecErrBody, pExecErr := probeShellExec(ctx, client, endpoint, authToken, profileUUID, probeCmd)
		if pExecErr != nil {
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("execution verification failed for profile %q: %v", profile, pExecErr),
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
		if pExecRes == nil || pExecRes.ExitCode == nil || *pExecRes.ExitCode != 0 {
			exitCode := -1
			if pExecRes != nil && pExecRes.ExitCode != nil {
				exitCode = *pExecRes.ExitCode
			}
			if profile == ProfileGoBuilder {
				return &ReadinessReport{
					Status:            StatusProfileUnsupported,
					Endpoint:          endpoint,
					Healthy:           true,
					Authenticated:     true,
					ContractSupported: true,
					ProfilesSupported: profilesMap,
					Detail:            fmt.Sprintf("go-builder profile missing working Go toolchain (go version exited with code %d)", exitCode),
					CheckedAt:         now,
				}
			}
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("execution verification failed for profile %q (exit code %d)", profile, exitCode),
				CheckedAt:         now,
			}
		}

		// Explicit release check for profile session
		_, _, _ = probeRelease(ctx, client, endpoint, authToken, profileUUID)

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
