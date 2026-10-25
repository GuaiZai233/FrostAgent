package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"FrostAgent/internal/computeruse/bridge"
	"FrostAgent/internal/computeruse/machine"
	mcpsvc "FrostAgent/internal/service/mcp"
	"FrostAgent/internal/storage"
)

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if mcpsvc.CheckSensitiveControlPlaneAuthScoped(r.RemoteAddr, r.Header, c.opts.Getenv) != nil {
		http.Error(w, `{"error":"管理认证失败"}`, 401)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/computer-use"), "/")
	var result any
	var err error
	a := c.managementActor()
	switch {
	case path == "" && r.Method == http.MethodGet:
		result, err = c.snapshot()
	case path == "config" && r.Method == http.MethodPut:
		var request struct {
			Enabled            bool    `json:"enabled"`
			BridgeURL          string  `json:"bridge_url"`
			Token              *string `json:"token"`
			AdapterToken       *string `json:"adapter_token"`
			CAPEM              string  `json:"ca_pem"`
			MaxConcurrent      int     `json:"max_concurrent"`
			MaxSeconds         int     `json:"max_seconds"`
			MaxModelRounds     int     `json:"max_model_rounds"`
			MaxToolCalls       int     `json:"max_tool_calls"`
			AuditRetentionDays int     `json:"audit_retention_days"`
		}
		if err = body(r, &request); err == nil {
			c.mu.Lock()
			cfg := c.config
			c.mu.Unlock()
			cfg.Enabled = request.Enabled
			cfg.BridgeURL = strings.TrimRight(strings.TrimSpace(request.BridgeURL), "/")
			cfg.CAPEM = request.CAPEM
			if request.Token != nil {
				cfg.Token = *request.Token
			}
			if request.AdapterToken != nil {
				cfg.AdapterToken = *request.AdapterToken
			}
			cfg.MaxConcurrent = request.MaxConcurrent
			cfg.MaxSeconds = request.MaxSeconds
			cfg.MaxModelRounds = request.MaxModelRounds
			cfg.MaxToolCalls = request.MaxToolCalls
			cfg.AuditRetentionDays = request.AuditRetentionDays
			err = c.setConfig(cfg)
			result = map[string]bool{"saved": err == nil}
		}
	case path == "sync" && r.Method == http.MethodPost:
		result, err = c.syncProjects(r.Context(), a)
	case path == "projects" && r.Method == http.MethodPost:
		var project machine.Project
		if err = body(r, &project); err == nil {
			var content bridge.Content
			content, err = c.manual(r.Context(), a, "project.register", machine.Params{Project: &project})
			if err == nil {
				var registered storage.ComputerUseProject
				err = json.Unmarshal([]byte(content.Text), &registered)
				if err == nil {
					err = c.opts.DB.SaveComputerUseProject(r.Context(), c.opts.InstanceID, registered)
					result = registered
				}
			}
		}
	case strings.HasPrefix(path, "projects/") && strings.HasSuffix(path, "/archive") && r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "projects/"), "/archive")
		var request struct {
			Archived bool `json:"archived"`
		}
		if err = body(r, &request); err == nil {
			_, err = c.manual(r.Context(), a, "project.archive", machine.Params{ProjectID: id, Archived: request.Archived})
			if err == nil {
				result, err = c.syncProjects(r.Context(), a)
			}
		}
	case path == "tasks" && r.Method == http.MethodPost:
		var request struct {
			ProjectID   string `json:"project_id"`
			Instruction string `json:"instruction"`
		}
		if err = body(r, &request); err == nil {
			result, err = c.start(a, request.ProjectID, request.Instruction)
		}
	case path == "takeover" && r.Method == http.MethodPost:
		result, err = c.desktopControl(r.Context(), a, true)
	case path == "desktop/resume" && r.Method == http.MethodPost:
		result, err = c.desktopControl(r.Context(), a, false)
	case strings.HasPrefix(path, "tasks/"):
		parts := strings.Split(strings.TrimPrefix(path, "tasks/"), "/")
		if len(parts) != 2 || !idPattern.MatchString(parts[0]) {
			err = ErrState
			break
		}
		id, action := parts[0], parts[1]
		switch {
		case action == "pause" && r.Method == http.MethodPost:
			result, err = c.pause(r.Context(), id, "owner_paused")
		case action == "reconcile" && r.Method == http.MethodPost:
			result, err = c.reconcile(r.Context(), id)
		case action == "resume" && r.Method == http.MethodPost:
			var request struct {
				Instruction        string `json:"instruction"`
				AcknowledgeUnknown bool   `json:"acknowledge_unknown"`
			}
			if err = body(r, &request); err == nil {
				result, err = c.resume(a, id, request.Instruction, request.AcknowledgeUnknown)
			}
		case action == "content" && r.Method == http.MethodGet:
			c.mu.Lock()
			contentID := c.contentIDs[id]
			c.mu.Unlock()
			var ok bool
			result, ok = c.content.Content(contentID)
			if !ok {
				err = bridge.ErrContentExpired
			}
		default:
			err = ErrState
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, bridge.ErrContentExpired) {
			status = http.StatusGone
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": publicError(err)})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}
func body(r *http.Request, v any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, bridge.MaxRequestBytes+1))
	if err != nil || len(data) > bridge.MaxRequestBytes {
		return bridge.ErrInvalid
	}
	if decode(data, v) != nil {
		return bridge.ErrInvalid
	}
	return nil
}
func publicError(err error) string {
	switch {
	case errors.Is(err, ErrPermission), errors.Is(err, ErrUnavailable), errors.Is(err, ErrBusy), errors.Is(err, ErrState):
		return err.Error()
	case errors.Is(err, bridge.ErrContentExpired):
		return "内容已过期或服务已重启；不会重新执行原操作。"
	case errors.Is(err, bridge.ErrInvalid):
		return "请求参数无效，请检查配置。"
	case errors.Is(err, bridge.ErrOutcomeUnknown):
		return "桥连接中断，操作结果未知；请核对状态，不要盲目重试。"
	default:
		return "操作未完成。请检查配置、任务状态或 VM；不会自动重放。"
	}
}
func (c *Controller) setConfig(cfg storage.ComputerUseConfig) error {
	c.controlMu.Lock()
	defer c.controlMu.Unlock()
	// The disable switch closes admission immediately and pauses existing
	// runs using their current bridge, before any credential/target change.
	c.mu.Lock()
	if !cfg.Enabled && c.config.Enabled {
		current := c.config
		current.Enabled = false
		if err := c.opts.DB.SaveComputerUseConfig(context.Background(), c.opts.InstanceID, current); err != nil {
			c.mu.Unlock()
			return ErrUnavailable
		}
		c.config = current
		var ids []string
		for id, cancel := range c.runs {
			cancel()
			ids = append(ids, id)
		}
		c.mu.Unlock()
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			_, _ = c.pause(ctx, id, "disabled")
			cancel()
		}
		c.mu.Lock()
	}
	c.mu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.runs) > 0 && cfg.Enabled {
		return ErrBusy
	}
	for _, t := range c.tasks {
		if (t.State == "running" || pending(t.Pending) || pending(t.PauseOperation)) &&
			(cfg.Enabled || cfg.BridgeURL != c.config.BridgeURL || cfg.Token != c.config.Token || cfg.CAPEM != c.config.CAPEM || cfg.AdapterToken != c.config.AdapterToken) {
			return ErrBusy
		}
	}
	if _, err := bridgeRoots(cfg); err != nil {
		return bridge.ErrInvalid
	}
	if cfg.Enabled {
		if _, err := c.newTransport(cfg); err != nil {
			return bridge.ErrInvalid
		}
	}
	if err := c.opts.DB.SaveComputerUseConfig(context.Background(), c.opts.InstanceID, cfg); err != nil {
		return bridge.ErrInvalid
	}
	if cfg.BridgeURL != c.config.BridgeURL {
		c.desktopConfirmed = false
		c.desktopGeneration = 0
		for _, t := range c.tasks {
			if isDesktopControlTask(t) && t.BridgeURL == cfg.BridgeURL {
				c.desktopGeneration = max(c.desktopGeneration, t.Generation)
			}
		}
	}
	c.config = cfg
	audit, err := bridge.NewAudit(c.opts.AuditDir, cfg.AuditRetentionDays)
	if err != nil {
		c.blocked = true
		return bridge.ErrAudit
	}
	c.audit = audit
	// A storage/audit failure requires a runtime restart, not just another
	// settings save, to revalidate durable state before admitting effects.
	return nil
}
func (c *Controller) snapshot() (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	projects, err := c.opts.DB.ComputerUseProjects(context.Background(), c.opts.InstanceID)
	if err != nil {
		return nil, err
	}
	configData, _ := json.Marshal(c.config)
	var cfg map[string]any
	_ = json.Unmarshal(configData, &cfg)
	delete(cfg, "token")
	delete(cfg, "adapter_token")
	cfg["adapter_token_configured"] = c.config.AdapterToken != ""
	cfg["token_configured"] = c.config.Token != ""
	tasks := make([]map[string]any, 0, len(c.tasks))
	workspaces := make([]machine.Workspace, 0)
	for _, t := range c.tasks {
		raw, _ := json.Marshal(t)
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		item["reason"] = t.Code
		if t.Pending != nil {
			item["operation_state"] = t.Pending.State
		}
		tasks = append(tasks, item)
		if t.Workspace != "" {
			workspaces = append(workspaces, machine.Workspace{TaskID: t.ID, ProjectID: t.ProjectID, Path: t.Workspace, Branch: t.Branch, Generation: t.Generation, Paused: t.State != "running"})
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i]["created_at"].(string) > tasks[j]["created_at"].(string) })
	model := "未配置"
	if c.opts.ModelDescription != nil {
		model = c.opts.ModelDescription()
	}
	return map[string]any{"config": cfg, "projects": projects, "tasks": tasks, "workspaces": workspaces, "desktop_paused": c.desktopPaused, "desktop_generation": c.desktopGeneration, "desktop_confirmed": c.desktopConfirmed, "disclosure": Disclosure, "model": model}, nil
}

func (c *Controller) manual(ctx context.Context, a actor, kind string, params machine.Params) (bridge.Content, error) {
	if kind != "desktop.takeover" {
		c.controlMu.Lock()
		defer c.controlMu.Unlock()
	}
	c.mu.Lock()
	if err := c.usable(a); err != nil {
		c.mu.Unlock()
		return bridge.Content{}, err
	}
	// Unknown administrative effects remain visible as tasks for reconciliation.
	// A new automatic invocation is never substituted for their old identity.
	t := &storage.ComputerUseTask{ID: newID(), ProjectID: "control", ActorID: a.id, ActorKind: a.kind, BridgeURL: c.config.BridgeURL,
		State: "running", Generation: 1, CreatedAt: now(), UpdatedAt: now()}
	if kind == "desktop.takeover" || kind == "desktop.resume" {
		t.Generation = c.desktopGeneration + 1
		if t.Generation == 0 {
			c.mu.Unlock()
			return bridge.Content{}, ErrState
		}
	}
	client, req, err := c.prepareOperationLocked(ctx, t, t.Generation, kind, params)
	if err != nil {
		c.mu.Unlock()
		return bridge.Content{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopCancel := context.AfterFunc(c.opts.Context, cancel)
	c.controls[t.ID] = cancel
	c.wg.Add(1)
	defer func() {
		stopCancel()
		cancel()
		c.mu.Lock()
		delete(c.controls, t.ID)
		c.mu.Unlock()
		c.wg.Done()
	}()
	if kind == "desktop.takeover" || kind == "desktop.resume" {
		c.desktopGeneration = t.Generation
		c.desktopConfirmed = false
	}
	generation := t.Generation
	c.mu.Unlock()
	result, err := c.executeOperation(ctx, client, req)
	c.mu.Lock()
	if t.Generation != generation || t.State != "running" {
		err = ErrState
	} else if err == nil {
		t.State = "completed"
		t.Code = "completed"
	} else {
		t.State = "paused"
		t.Code = errorCode(err)
	}
	saveErr := c.save(t)
	c.mu.Unlock()
	if saveErr != nil {
		return bridge.Content{}, saveErr
	}
	return result, err
}
func (c *Controller) syncProjects(ctx context.Context, a actor) (any, error) {
	content, err := c.manual(ctx, a, "project.list", machine.Params{})
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Projects          []storage.ComputerUseProject `json:"projects"`
		Workspaces        []machine.Workspace          `json:"workspaces"`
		DesktopPaused     bool                         `json:"desktop_paused"`
		DesktopGeneration uint64                       `json:"desktop_generation"`
	}
	if json.Unmarshal([]byte(content.Text), &snapshot) != nil {
		return nil, bridge.ErrProtocol
	}
	for _, p := range snapshot.Projects {
		if err := c.opts.DB.SaveComputerUseProject(context.Background(), c.opts.InstanceID, p); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	if snapshot.DesktopGeneration >= c.desktopGeneration {
		c.desktopPaused = snapshot.DesktopPaused
		c.desktopGeneration = snapshot.DesktopGeneration
		c.desktopConfirmed = true
	}
	for _, w := range snapshot.Workspaces {
		if t := c.tasks[w.TaskID]; t != nil && t.ProjectID == w.ProjectID {
			t.Workspace = w.Path
			t.Branch = w.Branch
			t.Prepared = true
			if err := c.save(t); err != nil {
				c.mu.Unlock()
				return nil, err
			}
		}
	}
	c.mu.Unlock()
	return c.snapshot()
}
func (c *Controller) desktopControl(ctx context.Context, a actor, paused bool) (any, error) {
	kind := "desktop.resume"
	if paused {
		kind = "desktop.takeover"
	}
	content, err := c.manual(ctx, a, kind, machine.Params{})
	if err != nil {
		return nil, err
	}
	var result struct {
		DesktopPaused     bool   `json:"desktop_paused"`
		GUIStopped        bool   `json:"gui_stopped"`
		DesktopGeneration uint64 `json:"desktop_generation"`
	}
	if json.Unmarshal([]byte(content.Text), &result) != nil || result.DesktopPaused != paused || (paused && !result.GUIStopped) {
		return nil, bridge.ErrProtocol
	}
	c.mu.Lock()
	if content.Generation != c.desktopGeneration || c.closing {
		c.mu.Unlock()
		return nil, ErrState
	}
	c.desktopPaused = paused
	c.desktopConfirmed = true
	result.DesktopGeneration = content.Generation
	var ids []string
	if paused {
		for id, cancel := range c.runs {
			cancel()
			ids = append(ids, id)
		}
	}
	c.mu.Unlock()
	// The machine-wide fence is already durable before acknowledging takeover.
	// Pausing scheduling does not claim terminal processes have terminated.
	for _, id := range ids {
		pauseCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		_, _ = c.pause(pauseCtx, id, "human_takeover")
		cancel()
	}
	c.mu.Lock()
	current := c.desktopGeneration == content.Generation && c.desktopConfirmed && !c.closing
	c.mu.Unlock()
	if !current {
		return nil, ErrState
	}
	return result, nil
}
