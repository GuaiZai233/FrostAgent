package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Supported canonical profiles and networks.
var (
	DefaultSupportedProfiles = []string{"go-builder", "action-runtime", "minimal"}
	DefaultSupportedNetworks = []string{"none", "public", "allowlist", "isolated"}
)

// Config configures the compatible Sandbox Gateway server.
type Config struct {
	AuthToken         string
	WorkDir           string
	SupportedProfiles []string
	SupportedNetworks []string
	ShellPath         string
}

// Session represents an isolated sandbox session.
type Session struct {
	UserUUID           string            `json:"user_uuid"`
	Profile            string            `json:"profile"`
	Network            string            `json:"network"`
	AllowedHosts       []string          `json:"allowed_hosts"`
	Env                map[string]string `json:"env"`
	RuntimeCallbackURL string            `json:"runtime_callback_url"`
	Dir                string            `json:"dir"`
	CreatedAt          time.Time         `json:"created_at"`
}

// Server implements the ActionsCat / FrostAgent compatible Sandbox Gateway HTTP API.
type Server struct {
	cfg       Config
	mu        sync.RWMutex
	sessions  map[string]*Session
	listener  net.Listener
	server    *http.Server
	shellPath string
}

// NewServer creates a new compatible Gateway Server.
func NewServer(cfg Config) *Server {
	if len(cfg.SupportedProfiles) == 0 {
		cfg.SupportedProfiles = DefaultSupportedProfiles
	}
	if len(cfg.SupportedNetworks) == 0 {
		cfg.SupportedNetworks = DefaultSupportedNetworks
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Join(os.TempDir(), "fa_sandbox_gateway")
	}
	_ = os.MkdirAll(cfg.WorkDir, 0755)

	shellPath := cfg.ShellPath
	if shellPath == "" {
		shellPath = detectShell()
	}

	s := &Server{
		cfg:       cfg,
		sessions:  make(map[string]*Session),
		shellPath: shellPath,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/sessions", s.handleSessions)
	mux.HandleFunc("/api/v1/shell/exec", s.handleShellExec)
	mux.HandleFunc("/api/v1/release", s.handleRelease)

	s.server = &http.Server{
		Handler: mux,
	}

	return s
}

// Handler returns the HTTP handler for testing with httptest.
func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

// Start binds to the given address and starts serving in the background.
// Returns the actual listening base URL (e.g. "http://127.0.0.1:3874").
func (s *Server) Start(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("gateway listen %s: %w", addr, err)
	}
	s.listener = ln

	go func() {
		_ = s.server.Serve(ln)
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	return fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port), nil
}

// Close stops the HTTP server and cleans up active sessions.
func (s *Server) Close() error {
	var err error
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err = s.server.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.Dir != "" {
			_ = os.RemoveAll(sess.Dir)
		}
	}
	s.sessions = make(map[string]*Session)
	return err
}

func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.cfg.AuthToken == "" {
		return true
	}
	token := r.Header.Get("X-Auth-Token")
	if token == "" {
		token = r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")
	}
	if token != s.cfg.AuthToken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "unauthorized: invalid or missing auth token",
		})
		return false
	}
	return true
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":             "ok",
		"version":            "1.0.0",
		"supported_profiles": s.cfg.SupportedProfiles,
		"supported_networks": s.cfg.SupportedNetworks,
		"capabilities":       []string{"sessions", "shell/exec", "release", "profiles", "network_policies"},
	})
}

type sessionInitReq struct {
	UserUUID           string            `json:"user_uuid"`
	Profile            string            `json:"profile"`
	Network            string            `json:"network"`
	AllowedHosts       []string          `json:"allowed_hosts"`
	NetworkRules       any               `json:"network_rules"`
	MemoryLimitMB      int               `json:"memory_limit_mb"`
	CPULimit           float64           `json:"cpu_limit"`
	Env                map[string]string `json:"env"`
	RuntimeCallbackURL string            `json:"runtime_callback_url"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}

	var req sessionInitReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024*1024)).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
		return
	}

	userUUID := strings.TrimSpace(req.UserUUID)
	if userUUID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user_uuid is required"})
		return
	}

	// Validate profile against supported list
	profile := req.Profile
	if profile == "" {
		profile = "minimal"
	}
	if !s.isProfileSupported(profile) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("profile %q not supported", profile),
		})
		return
	}

	network := req.Network
	if network == "" {
		network = "none"
	}

	// Session workspace directory
	sessDir := filepath.Join(s.cfg.WorkDir, userUUID)
	_ = os.MkdirAll(filepath.Join(sessDir, "out"), 0755)

	envMap := make(map[string]string)
	maps.Copy(envMap, req.Env)
	if req.RuntimeCallbackURL != "" {
		envMap["ACTIONSCAT_RUNTIME_ENDPOINT"] = req.RuntimeCallbackURL
	}

	sess := &Session{
		UserUUID:           userUUID,
		Profile:            profile,
		Network:            network,
		AllowedHosts:       req.AllowedHosts,
		Env:                envMap,
		RuntimeCallbackURL: req.RuntimeCallbackURL,
		Dir:                sessDir,
		CreatedAt:          time.Now().UTC(),
	}

	s.mu.Lock()
	s.sessions[userUUID] = sess
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user_uuid":            userUUID,
		"profile":              profile,
		"network":              network,
		"status":               "ready",
		"allowed_hosts":        req.AllowedHosts,
		"runtime_callback_url": req.RuntimeCallbackURL,
	})
}

type shellExecReq struct {
	Command string  `json:"command"`
	Cwd     string  `json:"cwd"`
	Timeout float64 `json:"timeout"`
}

type shellExecResp struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        *int   `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMs      int64  `json:"duration_ms"`
}

func (s *Server) handleShellExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}

	userUUID := strings.TrimSpace(r.URL.Query().Get("user_uuid"))
	if userUUID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user_uuid query parameter is required"})
		return
	}

	var req shellExecReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024*1024)).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid shell exec json: " + err.Error()})
		return
	}

	s.mu.RLock()
	sess, exists := s.sessions[userUUID]
	s.mu.RUnlock()

	if !exists {
		// Auto-provision for standalone FrostAgent codeinterpreter clients
		sessDir := filepath.Join(s.cfg.WorkDir, userUUID)
		_ = os.MkdirAll(filepath.Join(sessDir, "out"), 0755)
		sess = &Session{
			UserUUID:  userUUID,
			Profile:   "minimal",
			Network:   "none",
			Env:       make(map[string]string),
			Dir:       sessDir,
			CreatedAt: time.Now().UTC(),
		}
		s.mu.Lock()
		s.sessions[userUUID] = sess
		s.mu.Unlock()
	}

	execResult := s.executeCommand(r.Context(), sess, req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(execResult)
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(w, r) {
		return
	}

	userUUID := strings.TrimSpace(r.URL.Query().Get("user_uuid"))
	if userUUID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user_uuid query parameter is required"})
		return
	}

	s.mu.Lock()
	sess, exists := s.sessions[userUUID]
	delete(s.sessions, userUUID)
	s.mu.Unlock()

	if !exists {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "session not found"})
		return
	}

	if sess.Dir != "" {
		_ = os.RemoveAll(sess.Dir)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) isProfileSupported(p string) bool {
	return slices.Contains(s.cfg.SupportedProfiles, p)
}

// exportPattern extracts leading "export KEY='VAL'; " or "export KEY="VAL"; "
var exportPattern = regexp.MustCompile(`export\s+([A-Za-z0-9_]+)=['"]([^'"]*)['"];?\s*`)

func (s *Server) executeCommand(parentCtx context.Context, sess *Session, req shellExecReq) shellExecResp {
	startTime := time.Now()

	timeout := time.Duration(req.Timeout * float64(time.Second))
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmdStr := req.Command

	// Extract any inline export commands into per-exec env overrides
	envOverrides := make(map[string]string)
	for {
		m := exportPattern.FindStringSubmatchIndex(cmdStr)
		if m == nil || m[0] != 0 {
			break
		}
		k := cmdStr[m[2]:m[3]]
		v := cmdStr[m[4]:m[5]]
		envOverrides[k] = v
		cmdStr = strings.TrimSpace(cmdStr[m[1]:])
	}

	// Resolve target directory
	targetDir := sess.Dir
	if req.Cwd != "" && req.Cwd != "/sandbox" && req.Cwd != "/sandbox/" {
		cleanCwd := strings.TrimPrefix(req.Cwd, "/sandbox/")
		cleanCwd = strings.TrimPrefix(cleanCwd, "/sandbox")
		cleanCwd = strings.TrimPrefix(cleanCwd, "/")
		targetDir = filepath.Join(sess.Dir, filepath.FromSlash(cleanCwd))
	}
	_ = os.MkdirAll(targetDir, 0755)

	// Prepare shell command with sandbox path translation
	translatedCmd := translateSandboxPaths(cmdStr, sess.Dir)

	var cmd *exec.Cmd
	if s.shellPath != "" {
		cmd = exec.CommandContext(ctx, s.shellPath, "-c", translatedCmd)
	} else if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/c", translatedCmd)
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", translatedCmd)
	}

	cmd.Dir = targetDir

	// Setup environment variables
	cmdEnv := os.Environ()

	// Inject session env
	for k, v := range sess.Env {
		cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
	}
	// Inject overrides
	for k, v := range envOverrides {
		cmdEnv = append(cmdEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Inject SANDBOX root
	cmdEnv = append(cmdEnv, fmt.Sprintf("SANDBOX_ROOT=%s", sess.Dir))

	// Enforce network isolation for network: none
	if sess.Network == "none" || sess.Network == "isolated" {
		cmdEnv = append(cmdEnv,
			"HTTP_PROXY=http://127.0.0.1:9",
			"HTTPS_PROXY=http://127.0.0.1:9",
			"ALL_PROXY=http://127.0.0.1:9",
			"NO_PROXY=127.0.0.1,localhost,host.docker.internal",
		)
	}

	cmd.Env = cmdEnv

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	duration := time.Since(startTime)

	var exitCode *int
	timedOut := false

	if ctx.Err() == context.DeadlineExceeded {
		timedOut = true
		exitCode = nil
	} else if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			exitCode = &code
		} else {
			code := 1
			exitCode = &code
			stderrBuf.WriteString(err.Error())
		}
	} else {
		code := 0
		exitCode = &code
	}

	// On Windows, if a compiled binary "out/entrypoint" or "entrypoint" was built,
	// ensure entrypoint can be executed by path
	if exitCode != nil && *exitCode == 0 {
		ensureBinaryExecutable(sess.Dir)
	}

	return shellExecResp{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		ExitCode:   exitCode,
		TimedOut:   timedOut,
		DurationMs: duration.Milliseconds(),
	}
}

func ensureBinaryExecutable(dir string) {
	outEntrypoint := filepath.Join(dir, "out", "entrypoint")
	outEntrypointExe := filepath.Join(dir, "out", "entrypoint.exe")
	if _, err := os.Stat(outEntrypoint); err == nil {
		_ = os.Chmod(outEntrypoint, 0755)
		if runtime.GOOS == "windows" {
			if _, errExe := os.Stat(outEntrypointExe); os.IsNotExist(errExe) {
				data, errRead := os.ReadFile(outEntrypoint)
				if errRead == nil {
					_ = os.WriteFile(outEntrypointExe, data, 0755)
				}
			}
		}
	} else if _, errExe := os.Stat(outEntrypointExe); errExe == nil {
		_ = os.Chmod(outEntrypointExe, 0755)
		data, errRead := os.ReadFile(outEntrypointExe)
		if errRead == nil {
			_ = os.WriteFile(outEntrypoint, data, 0755)
		}
	}

	rootEntrypoint := filepath.Join(dir, "entrypoint")
	rootEntrypointExe := filepath.Join(dir, "entrypoint.exe")
	if _, err := os.Stat(rootEntrypoint); err == nil {
		_ = os.Chmod(rootEntrypoint, 0755)
		if runtime.GOOS == "windows" {
			if _, errExe := os.Stat(rootEntrypointExe); os.IsNotExist(errExe) {
				data, errRead := os.ReadFile(rootEntrypoint)
				if errRead == nil {
					_ = os.WriteFile(rootEntrypointExe, data, 0755)
				}
			}
		}
	} else if _, errExe := os.Stat(rootEntrypointExe); errExe == nil {
		_ = os.Chmod(rootEntrypointExe, 0755)
		data, errRead := os.ReadFile(rootEntrypointExe)
		if errRead == nil {
			_ = os.WriteFile(rootEntrypoint, data, 0755)
		}
	}
}

func translateSandboxPaths(cmdStr, sessDir string) string {
	// Convert session directory to forward slash for POSIX shell compatibility
	sessDirSlash := filepath.ToSlash(sessDir)

	// Replace occurrences of /sandbox with sessDirSlash
	translated := strings.ReplaceAll(cmdStr, "/sandbox/", sessDirSlash+"/")
	translated = strings.ReplaceAll(translated, "\"/sandbox\"", fmt.Sprintf("%q", sessDirSlash))
	translated = strings.ReplaceAll(translated, "'/sandbox'", fmt.Sprintf("'%s'", sessDirSlash))
	translated = strings.ReplaceAll(translated, " /sandbox ", fmt.Sprintf(" %s ", sessDirSlash))
	if strings.HasSuffix(translated, " /sandbox") {
		translated = translated[:len(translated)-len(" /sandbox")] + " " + sessDirSlash
	}
	if strings.HasPrefix(translated, "/sandbox") && len(translated) > 8 && (translated[8] == ' ' || translated[8] == '&' || translated[8] == ';') {
		translated = sessDirSlash + translated[8:]
	}

	return translated
}

func detectShell() string {
	if runtime.GOOS != "windows" {
		if path, err := exec.LookPath("sh"); err == nil {
			return path
		}
		if path, err := exec.LookPath("bash"); err == nil {
			return path
		}
		return "/bin/sh"
	}

	// Windows detection order:
	// 1. Git sh.exe or bash.exe
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\sh.exe`,
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\sh.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\msys64\usr\bin\sh.exe`,
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	if path, err := exec.LookPath("sh.exe"); err == nil {
		return path
	}
	if path, err := exec.LookPath("bash.exe"); err == nil {
		return path
	}

	return ""
}
