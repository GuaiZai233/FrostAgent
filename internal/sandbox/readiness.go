package sandbox

import (
	"bytes"
	"context"
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
	// the required session/profile contract, and supports all requested profiles.
	StatusReady ReadinessStatus = "ready"

	// StatusEndpointUnreachable indicates the gateway endpoint could not be contacted
	// (e.g. network down, connection refused, DNS failure, timeout).
	StatusEndpointUnreachable ReadinessStatus = "endpoint_unreachable"

	// StatusAuthFailure indicates the gateway is reachable but rejected the provided credentials.
	StatusAuthFailure ReadinessStatus = "auth_failure"

	// StatusAPIContractMissing indicates the gateway responded but does not implement the
	// required ActionsCat/FrostAgent sandbox API contract (e.g. 404 on /api/v1/sessions).
	StatusAPIContractMissing ReadinessStatus = "api_contract_missing"

	// StatusProfileUnsupported indicates the gateway implements the contract but rejected
	// one or more required execution profiles (e.g. go-builder or action-runtime).
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

// CheckReadiness performs active probing of a sandbox gateway to distinguish:
// 1. endpoint unreachable
// 2. auth failure
// 3. API contract missing (/api/v1/sessions 404)
// 4. profile unsupported (e.g. go-builder, action-runtime)
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

	// 2. Probe /api/v1/sessions (API Contract check)
	sessionsURL := endpoint + "/api/v1/sessions"
	probePayload := map[string]any{
		"user_uuid": "readiness-probe-contract-test",
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

	// Always clean up probe session if created
	if probeResp.StatusCode == http.StatusOK || probeResp.StatusCode == http.StatusCreated {
		releaseProbeSession(client, endpoint, authToken, "readiness-probe-contract-test")
	}

	// 3. Probe Requested Profiles
	if len(profiles) == 0 {
		profiles = []string{ProfileGoBuilder, ProfileActionRuntime}
	}

	profilesMap := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		profilesMap[p] = false
	}

	for _, profile := range profiles {
		sessUUID := fmt.Sprintf("readiness-probe-%s", strings.ReplaceAll(profile, "_", "-"))
		payload := map[string]any{
			"user_uuid": sessUUID,
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

		// Verify response payload echoes back the profile
		var initResp struct {
			UserUUID string `json:"user_uuid"`
			Profile  string `json:"profile"`
			Status   string `json:"status"`
		}
		_ = json.NewDecoder(io.LimitReader(pResp.Body, 1024)).Decode(&initResp)
		if initResp.Profile != "" && initResp.Profile != profile {
			releaseProbeSession(client, endpoint, authToken, sessUUID)
			return &ReadinessReport{
				Status:            StatusProfileUnsupported,
				Endpoint:          endpoint,
				Healthy:           true,
				Authenticated:     true,
				ContractSupported: true,
				ProfilesSupported: profilesMap,
				Detail:            fmt.Sprintf("gateway profile mismatch: requested %q but got %q", profile, initResp.Profile),
				CheckedAt:         now,
			}
		}

		profilesMap[profile] = true
		releaseProbeSession(client, endpoint, authToken, sessUUID)
	}

	return &ReadinessReport{
		Status:            StatusReady,
		Endpoint:          endpoint,
		Healthy:           true,
		Authenticated:     true,
		ContractSupported: true,
		ProfilesSupported: profilesMap,
		Detail:            "gateway is operational and supports all requested profiles",
		CheckedAt:         now,
	}
}

func releaseProbeSession(client *http.Client, endpoint, authToken, userUUID string) {
	relURL := fmt.Sprintf("%s/api/v1/release?user_uuid=%s", endpoint, url.QueryEscape(userUUID))
	req, err := http.NewRequest(http.MethodPost, relURL, nil)
	if err != nil {
		return
	}
	if authToken != "" {
		req.Header.Set("X-Auth-Token", authToken)
	}
	resp, err := client.Do(req)
	if err == nil && resp != nil {
		_ = resp.Body.Close()
	}
}
