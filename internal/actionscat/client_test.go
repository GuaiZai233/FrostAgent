package actionscat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"FrostAgent/internal/sandbox"
)

func TestClient_NotConfigured(t *testing.T) {
	client := New(func(k string) string { return "" })
	if client.IsConfigured() {
		t.Fatal("expected client to not be configured")
	}

	ctx := context.Background()
	_, err := client.ListActions(ctx)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}

	st := client.Status(ctx)
	if st.Configured || st.Healthy {
		t.Fatalf("expected unconfigured status, got %+v", st)
	}
}

func TestClient_InvalidURL(t *testing.T) {
	client := New(func(k string) string {
		if k == "ACTIONSCAT_ENDPOINT" {
			return "ftp://invalid-scheme"
		}
		return ""
	})

	ctx := context.Background()
	_, err := client.Health(ctx)
	if !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}
}

func TestClient_HealthAndStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		case "/api/v1/actions":
			auth := r.Header.Get("Authorization")
			if auth == "Bearer valid_token" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]Action{})
			} else {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	ctx := context.Background()

	// 1. Without management token: Healthy but unauthenticated
	cNoAuth := New(func(k string) string {
		if k == "ACTIONSCAT_ENDPOINT" {
			return ts.URL
		}
		return ""
	})
	hs, err := cNoAuth.Health(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if hs.Status != "ok" {
		t.Fatalf("expected status ok, got %s", hs.Status)
	}
	stNoAuth := cNoAuth.Status(ctx)
	if !stNoAuth.Configured || !stNoAuth.Healthy || stNoAuth.Authenticated {
		t.Fatalf("expected configured and healthy but not authenticated, got %+v", stNoAuth)
	}

	// 2. With invalid management token
	cBadAuth := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "wrong_token"
		default:
			return ""
		}
	})
	stBadAuth := cBadAuth.Status(ctx)
	if !stBadAuth.Healthy || stBadAuth.Authenticated {
		t.Fatalf("expected healthy but not authenticated, got %+v", stBadAuth)
	}

	// 3. With valid management token
	cValidAuth := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "valid_token"
		default:
			return ""
		}
	})
	stValidAuth := cValidAuth.Status(ctx)
	if !stValidAuth.Configured || !stValidAuth.Healthy || !stValidAuth.Authenticated {
		t.Fatalf("expected configured, healthy, and authenticated, got %+v", stValidAuth)
	}
}

func TestClient_ActionsAndRuns(t *testing.T) {
	const expectedToken = "test_mgmt_token_xyz"
	mockAction := Action{
		ID:          "act_test_1",
		Name:        "Test Action",
		Description: "A test action for integration",
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	mockRun := Run{
		ID:          "run_test_100",
		ActionID:    "act_test_1",
		Status:      "succeeded",
		TriggerType: "manual",
		Stdout:      "Execution successful",
		Stderr:      "",
		DurationMs:  120,
		CreatedAt:   time.Now().UTC(),
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+expectedToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		switch r.URL.Path {
		case "/api/v1/actions":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Action{mockAction})
				return
			}
			if r.Method == http.MethodPost {
				var req CreateActionReq
				_ = json.NewDecoder(r.Body).Decode(&req)
				created := mockAction
				created.ID = "act_created_01"
				created.Name = req.Name
				created.Description = req.Description
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(created)
				return
			}
		case "/api/v1/actions/act_test_1":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(mockAction)
				return
			}
		case "/api/v1/actions/act_test_1/runs":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(mockRun)
				return
			}
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Run{mockRun})
				return
			}
		case "/api/v1/actions/act_test_1/runs/run_test_100":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(mockRun)
				return
			}
		case "/api/v1/actions/act_test_1/runs/run_test_100/logs":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(RunLogs{
					Stdout: mockRun.Stdout,
					Stderr: mockRun.Stderr,
				})
				return
			}
		case "/api/v1/actions/act_missing":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return expectedToken
		default:
			return ""
		}
	})

	ctx := context.Background()

	// 1. List Actions
	actions, err := client.ListActions(ctx)
	if err != nil {
		t.Fatalf("ListActions failed: %v", err)
	}
	if len(actions) != 1 || actions[0].ID != "act_test_1" {
		t.Fatalf("unexpected actions: %+v", actions)
	}

	// 2. Get Action
	act, err := client.GetAction(ctx, "act_test_1")
	if err != nil {
		t.Fatalf("GetAction failed: %v", err)
	}
	if act.Name != "Test Action" {
		t.Fatalf("unexpected action name: %s", act.Name)
	}

	// 3. Get Missing Action -> ErrNotFound
	_, err = client.GetAction(ctx, "act_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing action, got %v", err)
	}

	// 4. Trigger Run
	run, err := client.TriggerRun(ctx, "act_test_1", ManualRunReq{
		ExtraEnv: map[string]string{"ENV_A": "VAL_A"},
	})
	if err != nil {
		t.Fatalf("TriggerRun failed: %v", err)
	}
	if run.ID != "run_test_100" || run.Status != "succeeded" {
		t.Fatalf("unexpected run: %+v", run)
	}

	// 5. TriggerRunAndWait
	runWait, err := client.TriggerRunAndWait(ctx, "act_test_1", ManualRunReq{}, 5*time.Second)
	if err != nil {
		t.Fatalf("TriggerRunAndWait failed: %v", err)
	}
	if !runWait.IsTerminal() {
		t.Fatalf("expected terminal run, got status %s", runWait.Status)
	}

	// 6. List Runs
	runs, err := client.ListRuns(ctx, "act_test_1", 10, 0)
	if err != nil {
		t.Fatalf("ListRuns failed: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run_test_100" {
		t.Fatalf("unexpected runs: %+v", runs)
	}

	// 7. Get Run
	rObj, err := client.GetRun(ctx, "act_test_1", "run_test_100")
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if rObj.Stdout != "Execution successful" {
		t.Fatalf("unexpected stdout: %s", rObj.Stdout)
	}

	// 8. Get Run Logs
	logs, err := client.GetRunLogs(ctx, "act_test_1", "run_test_100")
	if err != nil {
		t.Fatalf("GetRunLogs failed: %v", err)
	}
	if logs.Stdout != "Execution successful" {
		t.Fatalf("unexpected log stdout: %s", logs.Stdout)
	}

	// 9. Create Action
	created, err := client.CreateAction(ctx, CreateActionReq{
		Name:        "New Action",
		Description: "Newly created action",
	})
	if err != nil {
		t.Fatalf("CreateAction failed: %v", err)
	}
	if created.ID != "act_created_01" || created.Name != "New Action" {
		t.Fatalf("unexpected created action: %+v", created)
	}

	// 10. Create Action with empty name should fail
	_, err = client.CreateAction(ctx, CreateActionReq{Name: "   "})
	if err == nil {
		t.Fatal("expected error for empty action name, got nil")
	}
}

func TestClient_Dispatch(t *testing.T) {
	const dispatchToken = "disp_token_123"
	var receivedEvent map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/dispatch" && r.Method == http.MethodPost {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+dispatchToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewDecoder(r.Body).Decode(&receivedEvent)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"matched": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_DISPATCH_TOKEN":
			return dispatchToken
		default:
			return ""
		}
	})

	err := client.Dispatch(context.Background(), map[string]any{
		"platform": "mock",
		"content":  "test dispatch",
	})
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}
	if receivedEvent["content"] != "test dispatch" {
		t.Fatalf("unexpected received event: %+v", receivedEvent)
	}
}

func TestClient_VersionsAndBuilds(t *testing.T) {
	const expectedToken = "test_mgmt_token_versions"

	mockVersion := ActionVersion{
		ID:            "ver_001",
		ActionID:      "act_test_1",
		VersionNumber: 1,
		SourceDigest:  "sha256:abcd1234",
		BuildSpec: BuildSpec{
			Language: "go",
			Command:  "go build -o /out/entrypoint .",
		},
		RuntimeSpec: RuntimeSpec{
			Entrypoint:     "entrypoint",
			TimeoutSeconds: 60,
			MemoryLimitMB:  128,
			CPULimit:       1.0,
		},
		CreatedAt: time.Now().UTC(),
	}

	exitCodeZero := 0
	mockBuild := ArtifactBuild{
		ID:               "bld_001",
		ActionID:         "act_test_1",
		VersionID:        "ver_001",
		BuildNumber:      1,
		Status:           "succeeded",
		ToolchainVersion: "go1.25.6",
		Stdout:           "Compilation finished successfully",
		Stderr:           "",
		ExitCode:         &exitCodeZero,
		CreatedAt:        time.Now().UTC(),
	}

	var activatedVersionID, activatedBuildID string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+expectedToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		switch {
		case r.URL.Path == "/api/v1/actions/act_test_1/versions" && r.Method == http.MethodPost:
			var req CreateVersionReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(mockVersion)
		case r.URL.Path == "/api/v1/actions/act_test_1/versions" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]ActionVersion{mockVersion})
		case r.URL.Path == "/api/v1/actions/act_test_1/versions/ver_001" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mockVersion)
		case r.URL.Path == "/api/v1/actions/act_test_1/versions/ver_001/builds" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mockBuild)
		case r.URL.Path == "/api/v1/actions/act_test_1/builds" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]ArtifactBuild{mockBuild})
		case r.URL.Path == "/api/v1/actions/act_test_1/builds/bld_001" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(mockBuild)
		case r.URL.Path == "/api/v1/actions/act_test_1/builds/bld_001/logs" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(BuildLogs{
				Stdout: mockBuild.Stdout,
				Stderr: mockBuild.Stderr,
			})
		case r.URL.Path == "/api/v1/actions/act_test_1/active-build" && r.Method == http.MethodPost:
			var req SetActiveBuildReq
			_ = json.NewDecoder(r.Body).Decode(&req)
			activatedVersionID = req.VersionID
			activatedBuildID = req.BuildID
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return expectedToken
		default:
			return ""
		}
	})

	ctx := context.Background()

	// 1. CreateVersion
	ver, err := client.CreateVersion(ctx, "act_test_1", CreateVersionReq{
		Files: map[string]string{
			"main.go": "package main\nfunc main() {}",
		},
		BuildSpec: BuildSpec{
			Language: "go",
		},
	})
	if err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}
	if ver.ID != "ver_001" || ver.VersionNumber != 1 {
		t.Fatalf("unexpected version: %+v", ver)
	}

	// 2. ListVersions
	vers, err := client.ListVersions(ctx, "act_test_1")
	if err != nil {
		t.Fatalf("ListVersions failed: %v", err)
	}
	if len(vers) != 1 || vers[0].ID != "ver_001" {
		t.Fatalf("unexpected versions: %+v", vers)
	}

	// 3. GetVersion
	vObj, err := client.GetVersion(ctx, "act_test_1", "ver_001")
	if err != nil {
		t.Fatalf("GetVersion failed: %v", err)
	}
	if vObj.ID != "ver_001" {
		t.Fatalf("unexpected version: %+v", vObj)
	}

	// 4. BuildVersion
	bld, err := client.BuildVersion(ctx, "act_test_1", "ver_001")
	if err != nil {
		t.Fatalf("BuildVersion failed: %v", err)
	}
	if bld.ID != "bld_001" || bld.Status != "succeeded" {
		t.Fatalf("unexpected build: %+v", bld)
	}

	// 5. GetBuild
	bObj, err := client.GetBuild(ctx, "act_test_1", "bld_001")
	if err != nil {
		t.Fatalf("GetBuild failed: %v", err)
	}
	if bObj.ID != "bld_001" {
		t.Fatalf("unexpected build: %+v", bObj)
	}

	// 6. GetBuildLogs
	logs, err := client.GetBuildLogs(ctx, "act_test_1", "bld_001")
	if err != nil {
		t.Fatalf("GetBuildLogs failed: %v", err)
	}
	if logs.Stdout != "Compilation finished successfully" {
		t.Fatalf("unexpected logs: %+v", logs)
	}

	// 7. ListBuilds
	builds, err := client.ListBuilds(ctx, "act_test_1")
	if err != nil {
		t.Fatalf("ListBuilds failed: %v", err)
	}
	if len(builds) != 1 || builds[0].ID != "bld_001" {
		t.Fatalf("unexpected builds: %+v", builds)
	}

	// 8. ActivateBuild
	err = client.ActivateBuild(ctx, "act_test_1", SetActiveBuildReq{
		VersionID: "ver_001",
		BuildID:   "bld_001",
	})
	if err != nil {
		t.Fatalf("ActivateBuild failed: %v", err)
	}
	if activatedVersionID != "ver_001" || activatedBuildID != "bld_001" {
		t.Fatalf("expected version ver_001 and build bld_001 activated, got %s / %s", activatedVersionID, activatedBuildID)
	}
}

func TestClient_BuildTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/builds") {
			// Simulate a slow build that takes longer than context deadline
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(ArtifactBuild{
				ID:     "bld_slow",
				Status: "succeeded",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "token"
		default:
			return ""
		}
	})

	// Pass a context with very short timeout to trigger transport deadline
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := client.BuildVersion(ctx, "act_test", "ver_slow")
	if err == nil {
		t.Fatal("expected build timeout error, got nil")
	}
	if !errors.Is(err, ErrBuildTimeoutUnknownResult) {
		t.Fatalf("expected ErrBuildTimeoutUnknownResult, got: %v", err)
	}
}


type faultTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (f *faultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f.roundTripFunc(req)
}

type faultyReader struct {
	readCount int
}

func (r *faultyReader) Read(p []byte) (n int, err error) {
	if r.readCount == 0 {
		r.readCount++
		copy(p, []byte(`{"id":"bld_interrupted",`))
		return 24, nil
	}
	return 0, errors.New("unexpected EOF during streaming response body")
}

func (r *faultyReader) Close() error {
	return nil
}

func TestClient_BuildUnknownResult_TransportErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Parameter validation: empty IDs do not wrap in ErrBuildUnknownResult
	cEmpty := New(func(k string) string { return "http://127.0.0.1:1" })
	_, err := cEmpty.BuildVersion(ctx, "", "ver_1")
	if err == nil || errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("expected param validation error, got: %v", err)
	}
	_, err = cEmpty.BuildVersion(ctx, "act_1", "")
	if err == nil || errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("expected param validation error, got: %v", err)
	}
	_, err = cEmpty.ListBuilds(ctx, "")
	if err == nil || errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("expected param validation error for ListBuilds, got: %v", err)
	}

	// 2. Transport level connection reset / network error -> ErrBuildUnknownResult
	cConnErr := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cConnErr.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: connection reset by peer")
			},
		},
	}
	_, err = cConnErr.BuildVersion(ctx, "act_1", "ver_1")
	if !errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("expected ErrBuildUnknownResult on connection drop, got: %v", err)
	}

	// 3. Severed response body (io.ErrUnexpectedEOF or broken stream) -> ErrBuildUnknownResult
	cSevered := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cSevered.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       &faultyReader{},
				}, nil
			},
		},
	}
	_, err = cSevered.BuildVersion(ctx, "act_1", "ver_1")
	if !errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("expected ErrBuildUnknownResult on severed response body, got: %v", err)
	}

	// 4. Definite HTTP errors (400 Bad Request, 404 Not Found, 409 Conflict) -> NOT ErrBuildUnknownResult
	cHttpErr := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cHttpErr.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusConflict,
					Status:     "409 Conflict",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":"build already running"}`)),
				}, nil
			},
		},
	}
	_, err = cHttpErr.BuildVersion(ctx, "act_1", "ver_1")
	if err == nil {
		t.Fatal("expected error on 409 conflict, got nil")
	}
	if errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("definitive HTTP 409 error must NOT be wrapped as ErrBuildUnknownResult, got: %v", err)
	}

	// 5. Server 500 error (e.g. sandbox/export failed after build record created) -> MUST wrap as ErrBuildUnknownResult
	cHttp500 := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cHttp500.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Status:     "500 Internal Server Error",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":"backend sandbox creation failed after build record saved"}`)),
				}, nil
			},
		},
	}
	_, err = cHttp500.BuildVersion(ctx, "act_1", "ver_1")
	if err == nil {
		t.Fatal("expected error on 500 server error, got nil")
	}
	if !errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("HTTP 500 error must be wrapped as ErrBuildUnknownResult, got: %v", err)
	}

	// 6. HTTP 200 with malformed JSON body -> MUST wrap as ErrBuildUnknownResult
	cMalformed := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cMalformed.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"id":"bld_1", "status":`)), // truncated JSON
				}, nil
			},
		},
	}
	_, err = cMalformed.BuildVersion(ctx, "act_1", "ver_1")
	if err == nil {
		t.Fatal("expected error on malformed 200 JSON, got nil")
	}
	if !errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("malformed 200 JSON must be wrapped as ErrBuildUnknownResult, got: %v", err)
	}

	// 7. HTTP 200 with missing essential fields (e.g. empty id/status) -> MUST wrap as ErrBuildUnknownResult
	cEmptyFields := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return "http://actionscat.example.internal"
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "test_token"
		default:
			return ""
		}
	})
	cEmptyFields.httpClient = &http.Client{
		Transport: &faultTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{}`)), // empty object missing id/status
				}, nil
			},
		},
	}
	_, err = cEmptyFields.BuildVersion(ctx, "act_1", "ver_1")
	if err == nil {
		t.Fatal("expected error on empty fields JSON, got nil")
	}
	if !errors.Is(err, ErrBuildUnknownResult) {
		t.Fatalf("empty fields 200 JSON must be wrapped as ErrBuildUnknownResult, got: %v", err)
	}
}

func TestClient_SchedulesAndMatchers(t *testing.T) {
	ctx := context.Background()

	var receivedPath string
	var receivedMethod string
	var receivedBody []byte
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedMethod = r.Method
		receivedAuth = r.Header.Get("Authorization")
		receivedBody, _ = io.ReadAll(r.Body)

		switch {
		case r.URL.Path == "/api/v1/actions/act_test/schedules" && r.Method == http.MethodPost:
			var req CreateScheduleReq
			_ = json.Unmarshal(receivedBody, &req)
			now := time.Now().UTC()
			sched := Schedule{
				ID:        "sched_123",
				ActionID:  "act_test",
				CronExpr:  req.CronExpr,
				Timezone:  req.Timezone,
				NextRunAt: now.Add(time.Hour),
				Enabled:   req.Enabled,
				CreatedAt: now,
				UpdatedAt: now,
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sched)

		case r.URL.Path == "/api/v1/actions/act_test/schedules" && r.Method == http.MethodGet:
			now := time.Now().UTC()
			list := []Schedule{
				{
					ID:        "sched_123",
					ActionID:  "act_test",
					CronExpr:  "0 8 * * *",
					Timezone:  "Asia/Shanghai",
					NextRunAt: now.Add(time.Hour),
					Enabled:   true,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(list)

		case r.URL.Path == "/api/v1/schedules/sched_123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))

		case r.URL.Path == "/api/v1/actions/act_test/matchers" && r.Method == http.MethodPost:
			var req CreateMatcherReq
			_ = json.Unmarshal(receivedBody, &req)
			now := time.Now().UTC()
			target := req.TargetField
			if target == "" {
				target = "text"
			}
			matcher := Matcher{
				ID:               "m_456",
				ActionID:         "act_test",
				Name:             req.Name,
				MatchType:        req.MatchType,
				Pattern:          req.Pattern,
				TargetField:      target,
				CaptureEnvMap:    req.CaptureEnvMap,
				Priority:         req.Priority,
				ContinueMatching: req.ContinueMatching,
				Enabled:          req.Enabled,
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(matcher)

		case r.URL.Path == "/api/v1/actions/act_test/matchers" && r.Method == http.MethodGet:
			now := time.Now().UTC()
			list := []Matcher{
				{
					ID:          "m_456",
					ActionID:    "act_test",
					Name:        "bilibili-link",
					MatchType:   "regex",
					Pattern:     `https://b23\.tv/(?P<bvid>\w+)`,
					TargetField: "text",
					CaptureEnvMap: map[string]string{
						"bvid": "BVID",
					},
					Enabled:   true,
					CreatedAt: now,
					UpdatedAt: now,
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(list)

		case r.URL.Path == "/api/v1/matchers/m_456" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := New(func(k string) string {
		switch k {
		case "ACTIONSCAT_ENDPOINT":
			return ts.URL
		case "ACTIONSCAT_MANAGEMENT_TOKEN":
			return "mgmt_token_xyz"
		default:
			return ""
		}
	})

	// 1. CreateSchedule
	sched, err := client.CreateSchedule(ctx, "act_test", CreateScheduleReq{
		CronExpr: "0 8 * * *",
		Timezone: "Asia/Shanghai",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("CreateSchedule failed: %v", err)
	}
	if sched.ID != "sched_123" || sched.CronExpr != "0 8 * * *" || !sched.Enabled {
		t.Fatalf("unexpected schedule: %+v", sched)
	}
	if receivedAuth != "Bearer mgmt_token_xyz" {
		t.Fatalf("expected Bearer mgmt_token_xyz, got %q", receivedAuth)
	}

	// 2. ListSchedules
	schedules, err := client.ListSchedules(ctx, "act_test")
	if err != nil {
		t.Fatalf("ListSchedules failed: %v", err)
	}
	if len(schedules) != 1 || schedules[0].ID != "sched_123" {
		t.Fatalf("unexpected schedules: %+v", schedules)
	}

	// 3. DeleteSchedule
	if err := client.DeleteSchedule(ctx, "sched_123"); err != nil {
		t.Fatalf("DeleteSchedule failed: %v", err)
	}
	if receivedPath != "/api/v1/schedules/sched_123" || receivedMethod != http.MethodDelete {
		t.Fatalf("unexpected delete schedule call: %s %s", receivedMethod, receivedPath)
	}

	// 4. CreateMatcher
	matcher, err := client.CreateMatcher(ctx, "act_test", CreateMatcherReq{
		Name:        "bilibili-link",
		MatchType:   "regex",
		Pattern:     `https://b23\.tv/(?P<bvid>\w+)`,
		TargetField: "text",
		CaptureEnvMap: map[string]string{
			"bvid": "BVID",
		},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMatcher failed: %v", err)
	}
	if matcher.ID != "m_456" || matcher.Name != "bilibili-link" || matcher.MatchType != "regex" {
		t.Fatalf("unexpected matcher: %+v", matcher)
	}

	// 5. ListMatchers
	matchers, err := client.ListMatchers(ctx, "act_test")
	if err != nil {
		t.Fatalf("ListMatchers failed: %v", err)
	}
	if len(matchers) != 1 || matchers[0].ID != "m_456" {
		t.Fatalf("unexpected matchers: %+v", matchers)
	}

	// 6. DeleteMatcher
	if err := client.DeleteMatcher(ctx, "m_456"); err != nil {
		t.Fatalf("DeleteMatcher failed: %v", err)
	}
	if receivedPath != "/api/v1/matchers/m_456" || receivedMethod != http.MethodDelete {
		t.Fatalf("unexpected delete matcher call: %s %s", receivedMethod, receivedPath)
	}

	// 7. Validation errors
	if _, err := client.CreateSchedule(ctx, "", CreateScheduleReq{CronExpr: "* * * * *"}); err == nil {
		t.Fatal("expected error on empty actionID for CreateSchedule")
	}
	if _, err := client.CreateSchedule(ctx, "act_test", CreateScheduleReq{CronExpr: ""}); err == nil {
		t.Fatal("expected error on empty cron_expr for CreateSchedule")
	}
	if _, err := client.ListSchedules(ctx, ""); err == nil {
		t.Fatal("expected error on empty actionID for ListSchedules")
	}
	if err := client.DeleteSchedule(ctx, ""); err == nil {
		t.Fatal("expected error on empty scheduleID for DeleteSchedule")
	}

	if _, err := client.CreateMatcher(ctx, "", CreateMatcherReq{Name: "a", Pattern: "p"}); err == nil {
		t.Fatal("expected error on empty actionID for CreateMatcher")
	}
	if _, err := client.CreateMatcher(ctx, "act_test", CreateMatcherReq{Name: "", Pattern: "p"}); err == nil {
		t.Fatal("expected error on empty name for CreateMatcher")
	}
	if _, err := client.CreateMatcher(ctx, "act_test", CreateMatcherReq{Name: "a", Pattern: ""}); err == nil {
		t.Fatal("expected error on empty pattern for CreateMatcher")
	}
	if _, err := client.ListMatchers(ctx, ""); err == nil {
		t.Fatal("expected error on empty actionID for ListMatchers")
	}
	if err := client.DeleteMatcher(ctx, ""); err == nil {
		t.Fatal("expected error on empty matcherID for DeleteMatcher")
	}
}

func TestClient_SandboxReadiness_CacheKeyBindingAndForce(t *testing.T) {
	ctx := context.Background()
	var serverHits atomic.Int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHits.Add(1)
		switch r.URL.Path {
		case "/api/v1/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
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
		case "/api/v1/shell/exec":
			var req struct {
				Command string `json:"command"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			stdout := "ok"
			if req.Command == "go version" {
				stdout = "go version go1.24.0 linux/amd64"
			}
			exitCode := 0
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stdout":    stdout,
				"exit_code": exitCode,
			})
		case "/api/v1/release":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	endpointVal := ts.URL
	tokenVal := "token-1"

	client := New(func(k string) string {
		switch k {
		case "FA_SANDBOX_ENDPOINT":
			return endpointVal
		case "FA_SANDBOX_AUTH_TOKEN":
			return tokenVal
		default:
			return ""
		}
	})

	// 1. Initially unprobed
	last := client.LastSandboxReadiness()
	if last == nil || last.Status != sandbox.StatusUnprobed {
		t.Fatalf("expected StatusUnprobed initially, got %+v", last)
	}

	// 2. First probe executes active probe and caches result
	initialHits := serverHits.Load()
	rep1 := client.CheckSandboxReadiness(ctx)
	if rep1.Status != sandbox.StatusReady {
		t.Fatalf("expected StatusReady, got %s (detail: %s)", rep1.Status, rep1.Detail)
	}
	hitsAfterFirst := serverHits.Load()
	if hitsAfterFirst <= initialHits {
		t.Fatalf("expected server hits to increase on first probe, went from %d to %d", initialHits, hitsAfterFirst)
	}

	// 3. Second probe with same endpoint and token returns cached result without hitting server
	rep2 := client.CheckSandboxReadiness(ctx)
	if rep2 != rep1 {
		t.Fatalf("expected cached report reference, got different report")
	}
	if serverHits.Load() != hitsAfterFirst {
		t.Fatalf("expected no new server hits during TTL cache hit, got %d (was %d)", serverHits.Load(), hitsAfterFirst)
	}

	// 4. Changing token immediately invalidates cache: LastSandboxReadiness returns unprobed
	tokenVal = "token-2"
	lastAfterTokenChange := client.LastSandboxReadiness()
	if lastAfterTokenChange == nil || lastAfterTokenChange.Status != sandbox.StatusUnprobed {
		t.Fatalf("expected LastSandboxReadiness to return StatusUnprobed after token change, got: %+v", lastAfterTokenChange)
	}

	// 5. CheckSandboxReadiness immediately reprobes after token change
	repTokenChange := client.CheckSandboxReadiness(ctx)
	if repTokenChange.Status != sandbox.StatusReady {
		t.Fatalf("expected StatusReady, got %s", repTokenChange.Status)
	}
	hitsAfterTokenChange := serverHits.Load()
	if hitsAfterTokenChange <= hitsAfterFirst {
		t.Fatalf("expected active probe on token change, server hits did not increase: %d vs %d", hitsAfterTokenChange, hitsAfterFirst)
	}

	// 6. Changing endpoint immediately invalidates cache: LastSandboxReadiness returns unprobed
	endpointVal = ts.URL + "/v2"
	lastAfterEpChange := client.LastSandboxReadiness()
	if lastAfterEpChange == nil || lastAfterEpChange.Status != sandbox.StatusUnprobed {
		t.Fatalf("expected LastSandboxReadiness to return StatusUnprobed after endpoint change, got: %+v", lastAfterEpChange)
	}

	// Restore endpoint for Force check test
	endpointVal = ts.URL
	repRestored := client.CheckSandboxReadiness(ctx)
	if repRestored.Status != sandbox.StatusReady {
		t.Fatalf("expected StatusReady, got %s", repRestored.Status)
	}
	hitsBeforeForce := serverHits.Load()

	// 7. ForceCheckSandboxReadiness forces probe even within TTL
	repForced := client.ForceCheckSandboxReadiness(ctx)
	if repForced.Status != sandbox.StatusReady {
		t.Fatalf("expected StatusReady on forced probe, got %s", repForced.Status)
	}
	if serverHits.Load() <= hitsBeforeForce {
		t.Fatalf("expected ForceCheckSandboxReadiness to hit server, hits stayed at %d", serverHits.Load())
	}
}
