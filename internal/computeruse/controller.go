// Package computeruse owns isolated, privileged work sessions. Ordinary chat
// history and ordinary tool registries never enter this package.
package computeruse

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"FrostAgent/internal/computeruse/bridge"
	"FrostAgent/internal/core"
	"FrostAgent/internal/storage"
)

var ErrUnavailable = errors.New("computer use 未启用或实例未就绪")
var ErrPermission = errors.New("只有可信主人私聊或真实管理工作台可以操作 computer use")
var ErrBusy = errors.New("任务仍在执行或结果尚未核对，请先暂停并核对状态")
var ErrState = errors.New("任务状态已变化，请刷新后重试")
var ErrBudget = errors.New("computer use 工具预算已耗尽，任务已暂停")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

const Disclosure = "这是专用 VM 的整机权限，不是沙箱。代码、终端输出及截图可能发送给配置的模型供应商（包括云端）；不会自动携带群聊历史。VM 登录和 Git 凭据由操作者在 VM 内配置。"

type transport interface {
	Run(context.Context, bridge.Request) (bridge.Operation, error)
	Lookup(context.Context, bridge.Identity, string) (bridge.Operation, error)
	Content(context.Context, bridge.Request) (bridge.Content, error)
}
type Options struct {
	DB                   *storage.DB
	InstanceID, AuditDir string
	Context              context.Context
	Provider             core.LLMProvider
	Getenv               func(string) string
	OwnerAllowed         func(string) bool
	ModelDescription     func() string
}
type Controller struct {
	mu                sync.Mutex
	controlMu         sync.Mutex
	opts              Options
	config            storage.ComputerUseConfig
	tasks             map[string]*storage.ComputerUseTask
	runs              map[string]context.CancelFunc
	controls          map[string]context.CancelFunc
	wg                sync.WaitGroup
	closing, blocked  bool
	desktopPaused     bool
	desktopGeneration uint64
	desktopConfirmed  bool
	audit             *bridge.Audit
	content           *bridge.ContentCache
	contentIDs        map[string]string
	newTransport      func(storage.ComputerUseConfig) (transport, error)
}
type actor struct{ kind, id string }

func New(opts Options) (*Controller, error) {
	if opts.DB == nil || !idPattern.MatchString(opts.InstanceID) || !filepath.IsAbs(opts.AuditDir) {
		return nil, ErrUnavailable
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Getenv == nil {
		opts.Getenv = func(string) string { return "" }
	}
	cfg, err := opts.DB.ComputerUseConfig(context.Background(), opts.InstanceID)
	if err != nil {
		return nil, err
	}
	if err := opts.DB.PauseComputerUseTasks(context.Background(), opts.InstanceID); err != nil {
		return nil, err
	}
	tasks, err := opts.DB.ComputerUseTasks(context.Background(), opts.InstanceID)
	if err != nil {
		return nil, err
	}
	c := &Controller{opts: opts, config: cfg, tasks: map[string]*storage.ComputerUseTask{}, runs: map[string]context.CancelFunc{}, controls: map[string]context.CancelFunc{},
		desktopPaused: true, content: bridge.NewContentCache(), contentIDs: map[string]string{}, newTransport: makeTransport}
	for _, t := range tasks {
		c.tasks[t.ID] = &t
		if t.ProjectID == "control" && t.BridgeURL == cfg.BridgeURL {
			c.desktopGeneration = max(c.desktopGeneration, t.Generation)
		}
	}
	c.audit, err = bridge.NewAudit(opts.AuditDir, cfg.AuditRetentionDays)
	if err != nil {
		c.blocked = true
	}
	// Startup reads metadata only: no automatic connection, resume or replay.
	return c, nil
}

func makeTransport(cfg storage.ComputerUseConfig) (transport, error) {
	u, err := url.Parse(cfg.BridgeURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, bridge.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return nil, errors.New("桥地址必须为 HTTPS 私网或回环数字 IP")
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if cfg.CAPEM != "" && !roots.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
		return nil, errors.New("CA PEM 无效")
	}
	httpTransport := http.DefaultTransport.(*http.Transport).Clone()
	httpTransport.Proxy = nil
	httpTransport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	httpTransport.DisableKeepAlives = true
	return bridge.NewClient(bridge.ClientConfig{BaseURL: cfg.BridgeURL, Token: cfg.Token, HTTPClient: &http.Client{Transport: httpTransport}})
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (c *Controller) managementActor() actor {
	hash := sha256.Sum256([]byte(c.opts.Getenv("MCP_CONTROL_TOKEN") + "\x00" + c.opts.Getenv("ADMIN_TOKEN") + "\x00" + c.opts.Getenv("MCP_ENFORCE_LOCAL_TOKEN")))
	return actor{"management", "management-" + hex.EncodeToString(hash[:16])}
}
func (c *Controller) allowed(a actor) bool {
	if a.kind == "management" {
		return a == c.managementActor()
	}
	return a.kind == "qq_private" && idPattern.MatchString(a.id) && c.opts.OwnerAllowed != nil && c.opts.OwnerAllowed(a.id)
}
func (c *Controller) usable(a actor) error {
	if !c.allowed(a) {
		return ErrPermission
	}
	if c.closing || c.blocked || !c.config.Enabled || c.opts.Context.Err() != nil {
		return ErrUnavailable
	}
	return nil
}
func (c *Controller) save(t *storage.ComputerUseTask) error {
	t.UpdatedAt = now()
	if err := c.opts.DB.SaveComputerUseTask(context.Background(), c.opts.InstanceID, *t); err != nil {
		c.blocked = true
		return errors.New("computer use 状态保存失败，已停止新操作")
	}
	return nil
}
func cloneTask(t *storage.ComputerUseTask) storage.ComputerUseTask {
	data, _ := json.Marshal(t)
	var copy storage.ComputerUseTask
	_ = json.Unmarshal(data, &copy)
	return copy
}
func operationMeta(op bridge.Operation) *storage.ComputerUseOperation {
	return &storage.ComputerUseOperation{ID: op.OperationID, InstanceID: op.InstanceID, TaskID: op.TaskID, ActorID: op.ActorID, Kind: op.Kind,
		RequestHash: op.RequestHash, State: string(op.State), Code: op.Code, Generation: op.Generation, ExitCode: op.ExitCode}
}
func operationIdentity(op *storage.ComputerUseOperation) bridge.Identity {
	return bridge.Identity{Version: 1, OperationID: op.ID, InstanceID: op.InstanceID, TaskID: op.TaskID, ActorID: op.ActorID, Kind: op.Kind, Generation: op.Generation}
}
func pending(op *storage.ComputerUseOperation) bool {
	return op != nil && (op.State == string(bridge.Accepted) || op.State == string(bridge.Running))
}
func (c *Controller) auditRecord(op bridge.Operation) error {
	if c.audit == nil || c.audit.Record(op) != nil {
		c.blocked = true
		return errors.New("computer use 审计不可用，已停止新操作")
	}
	return nil
}

func (c *Controller) start(a actor, projectID, instruction string) (storage.ComputerUseTask, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(a); err != nil {
		return storage.ComputerUseTask{}, err
	}
	if strings.TrimSpace(instruction) == "" || len(instruction) > 64<<10 {
		return storage.ComputerUseTask{}, errors.New("请提供不超过 64 KiB 的独立任务指令")
	}
	if len(c.runs) >= c.config.MaxConcurrent {
		return storage.ComputerUseTask{}, ErrBusy
	}
	projects, err := c.opts.DB.ComputerUseProjects(context.Background(), c.opts.InstanceID)
	if err != nil {
		return storage.ComputerUseTask{}, ErrUnavailable
	}
	found := false
	for _, p := range projects {
		if p.ID == projectID && !p.Archived {
			found = true
		}
	}
	if !found {
		return storage.ComputerUseTask{}, errors.New("项目不存在或已归档")
	}
	t := &storage.ComputerUseTask{ID: newID(), ProjectID: projectID, ActorKind: a.kind, ActorID: a.id, BridgeURL: c.config.BridgeURL,
		State: "running", Generation: 1, CreatedAt: now(), UpdatedAt: now()}
	if err := c.save(t); err != nil {
		return storage.ComputerUseTask{}, err
	}
	c.tasks[t.ID] = t
	c.launchLocked(t, instruction)
	return cloneTask(t), nil
}
func (c *Controller) launchLocked(t *storage.ComputerUseTask, instruction string) {
	ctx, cancel := context.WithTimeout(c.opts.Context, time.Duration(c.config.MaxSeconds)*time.Second)
	c.runs[t.ID] = cancel
	cfg := c.config
	id, gen := t.ID, t.Generation
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer cancel()
		c.run(ctx, id, gen, instruction, cfg)
		c.mu.Lock()
		delete(c.runs, id)
		c.mu.Unlock()
	}()
}
func (c *Controller) resume(a actor, id, instruction string, ack bool) (storage.ComputerUseTask, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.usable(a); err != nil {
		return storage.ComputerUseTask{}, err
	}
	t := c.tasks[id]
	if t == nil || t.ProjectID == "control" {
		return storage.ComputerUseTask{}, ErrState
	}
	if _, running := c.runs[id]; running || pending(t.Pending) || pending(t.PauseOperation) || len(c.runs) >= c.config.MaxConcurrent {
		return storage.ComputerUseTask{}, ErrBusy
	}
	if t.BridgeURL != c.config.BridgeURL {
		return storage.ComputerUseTask{}, errors.New("任务绑定另一桥地址，不允许切换机器续跑")
	}
	if t.ReconcileRequired && !ack {
		return storage.ComputerUseTask{}, errors.New("请先核对未知操作并明确确认；旧操作不会重放")
	}
	if strings.TrimSpace(instruction) == "" || len(instruction) > 64<<10 {
		return storage.ComputerUseTask{}, errors.New("继续需要新的独立指令")
	}
	t.Generation++
	t.ActorKind = a.kind
	t.ActorID = a.id
	t.State = "running"
	t.Code = ""
	t.GUIStopped = false
	t.ReconcileRequired = false
	t.Pending = nil
	t.PauseOperation = nil
	t.ModelRounds = 0
	t.ToolCalls = 0
	if err := c.save(t); err != nil {
		return storage.ComputerUseTask{}, err
	}
	c.launchLocked(t, instruction)
	return cloneTask(t), nil
}

// perform saves an immutable operation identity before allowing transport.
// Late facts are retained but cannot advance a paused/newer task generation.
func (c *Controller) perform(ctx context.Context, id string, gen uint64, kind string, parameters any) (bridge.Content, error) {
	raw, err := json.Marshal(parameters)
	if err != nil {
		return bridge.Content{}, bridge.ErrInvalid
	}
	c.mu.Lock()
	t := c.tasks[id]
	if t == nil || t.Generation != gen || t.State != "running" {
		c.mu.Unlock()
		return bridge.Content{}, ErrState
	}
	if err := c.usable(actor{t.ActorKind, t.ActorID}); err != nil {
		c.mu.Unlock()
		return bridge.Content{}, err
	}
	if t.ToolCalls >= c.config.MaxToolCalls {
		c.mu.Unlock()
		return bridge.Content{}, ErrBudget
	}
	if t.BridgeURL != c.config.BridgeURL || pending(t.Pending) {
		c.mu.Unlock()
		return bridge.Content{}, ErrBusy
	}
	if ctx.Err() != nil {
		c.mu.Unlock()
		return bridge.Content{}, ctx.Err()
	}
	client, err := c.newTransport(c.config)
	if err != nil {
		c.mu.Unlock()
		return bridge.Content{}, err
	}
	timeout := time.Duration(c.config.MaxSeconds) * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	timeout = min(timeout, 10*time.Minute)
	req := bridge.Request{Identity: bridge.Identity{Version: 1, OperationID: newID(), InstanceID: c.opts.InstanceID, TaskID: id,
		Generation: gen, ActorID: t.ActorID, Kind: kind}, TimeoutMS: max(int64(1), timeout.Milliseconds()), Parameters: raw}
	hash, err := req.Fingerprint()
	if err != nil {
		c.mu.Unlock()
		return bridge.Content{}, err
	}
	op := bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Accepted}}
	t.Pending = operationMeta(op)
	t.ToolCalls++
	if err = c.save(t); err == nil {
		err = c.auditRecord(op)
	}
	c.mu.Unlock()
	if err != nil {
		return bridge.Content{}, err
	}
	result, runErr := client.Run(ctx, req)
	if runErr != nil {
		result = op
		result.Outcome = bridge.Outcome{State: bridge.Unknown, Code: "outcome_unknown"}
	}
	c.mu.Lock()
	t = c.tasks[id]
	if t == nil {
		c.mu.Unlock()
		return bridge.Content{}, ErrState
	}
	if t.Pending != nil && t.Pending.ID == req.OperationID {
		t.Pending = operationMeta(result)
		t.LastOperation = operationMeta(result)
	}
	if result.State == bridge.Unknown {
		t.ReconcileRequired = true
	}
	saveErr := c.save(t)
	auditErr := c.auditRecord(result)
	current := t.Generation == gen && t.State == "running"
	c.mu.Unlock()
	if saveErr != nil {
		return bridge.Content{}, saveErr
	}
	if auditErr != nil {
		return bridge.Content{}, auditErr
	}
	if runErr != nil {
		return bridge.Content{}, bridge.ErrOutcomeUnknown
	}
	if result.State != bridge.Succeeded {
		return bridge.Content{}, errors.New("桥操作未成功，已停止自动执行；请核对结果")
	}
	if !current {
		return bridge.Content{}, ErrState
	}
	content, err := client.Content(ctx, req)
	if err != nil {
		return bridge.Content{}, err
	}
	c.mu.Lock()
	t = c.tasks[id]
	if t == nil || t.Generation != gen || t.State != "running" || c.closing {
		c.mu.Unlock()
		return bridge.Content{}, ErrState
	}
	if err = c.content.Put(req, content.Text, content.PNG); err == nil {
		c.contentIDs[id] = req.OperationID
	}
	c.mu.Unlock()
	return content, err
}

func (c *Controller) pause(ctx context.Context, id, reason string) (storage.ComputerUseTask, error) {
	c.mu.Lock()
	t := c.tasks[id]
	if t == nil {
		c.mu.Unlock()
		return storage.ComputerUseTask{}, ErrState
	}
	if cancel := c.runs[id]; cancel != nil {
		cancel()
	}
	// This higher generation fences old queued prepare/input requests as well
	// as their late HTTP completions. It does not assert terminal processes died.
	t.Generation++
	t.State = "paused"
	t.Code = reason
	t.GUIStopped = false
	client, err := c.newTransport(c.config)
	if err != nil || t.BridgeURL != c.config.BridgeURL {
		t.ReconcileRequired = true
		_ = c.save(t)
		out := cloneTask(t)
		c.mu.Unlock()
		return out, ErrUnavailable
	}
	req := bridge.Request{Identity: bridge.Identity{Version: 1, OperationID: newID(), InstanceID: c.opts.InstanceID, TaskID: id,
		Generation: t.Generation, ActorID: t.ActorID, Kind: "task.pause"}, TimeoutMS: 30000, Parameters: json.RawMessage("{}")}
	hash, _ := req.Fingerprint()
	op := bridge.Operation{Identity: req.Identity, RequestHash: hash, Outcome: bridge.Outcome{State: bridge.Accepted}}
	t.PauseOperation = operationMeta(op)
	if err = c.save(t); err == nil {
		err = c.auditRecord(op)
	}
	c.mu.Unlock()
	if err != nil {
		return storage.ComputerUseTask{}, err
	}
	result, runErr := client.Run(ctx, req)
	if runErr != nil {
		result = op
		result.Outcome = bridge.Outcome{State: bridge.Unknown, Code: "outcome_unknown"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t = c.tasks[id]
	if t.PauseOperation != nil && t.PauseOperation.ID == req.OperationID {
		t.PauseOperation = operationMeta(result)
		t.GUIStopped = result.State == bridge.Succeeded
		t.ReconcileRequired = t.ReconcileRequired || result.State != bridge.Succeeded
	}
	saveErr := c.save(t)
	auditErr := c.auditRecord(result)
	if saveErr != nil {
		return cloneTask(t), saveErr
	}
	if auditErr != nil {
		return cloneTask(t), auditErr
	}
	if runErr != nil || result.State != bridge.Succeeded {
		return cloneTask(t), errors.New("暂停已请求，但未确认 GUI 停止；请勿据此接管桌面")
	}
	return cloneTask(t), nil
}

func (c *Controller) reconcile(ctx context.Context, id string) (storage.ComputerUseTask, error) {
	c.mu.Lock()
	t := c.tasks[id]
	if t == nil {
		c.mu.Unlock()
		return storage.ComputerUseTask{}, ErrState
	}
	if t.BridgeURL != c.config.BridgeURL {
		c.mu.Unlock()
		return storage.ComputerUseTask{}, ErrState
	}
	client, err := c.newTransport(c.config)
	if err != nil {
		c.mu.Unlock()
		return storage.ComputerUseTask{}, err
	}
	snapshot := cloneTask(t)
	c.mu.Unlock()
	for index, p := range []*storage.ComputerUseOperation{snapshot.Pending, snapshot.PauseOperation} {
		if p == nil {
			continue
		}
		op, err := client.Lookup(ctx, operationIdentity(p), p.RequestHash)
		if err != nil {
			return snapshot, errors.New("无法确认桥操作状态，保留未知结果；不会重放")
		}
		c.mu.Lock()
		t = c.tasks[id]
		target := &t.Pending
		if index == 1 {
			target = &t.PauseOperation
		}
		if *target != nil && (*target).ID == p.ID {
			*target = operationMeta(op)
			if index == 0 {
				t.LastOperation = operationMeta(op)
			} else if t.Generation == op.Generation {
				t.GUIStopped = op.State == bridge.Succeeded
			}
			if op.State == bridge.Unknown {
				t.ReconcileRequired = true
			}
		}
		err = c.save(t)
		snapshot = cloneTask(t)
		c.mu.Unlock()
		if err != nil {
			return snapshot, err
		}
	}
	return snapshot, nil
}

func (c *Controller) Close() {
	c.mu.Lock()
	c.closing = true
	var ids []string
	for id, cancel := range c.runs {
		cancel()
		ids = append(ids, id)
	}
	for _, cancel := range c.controls {
		cancel()
	}
	c.mu.Unlock()
	// Control RPCs use a separate deadline after the runtime context stopped.
	var stops sync.WaitGroup
	for _, id := range ids {
		stops.Add(1)
		go func(id string) {
			defer stops.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = c.pause(ctx, id, "runtime_stopped")
		}(id)
	}
	stops.Wait()
	c.wg.Wait()
	_ = c.content.Close()
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrBudget):
		return "tool_budget"
	case errors.Is(err, context.DeadlineExceeded):
		return "time_budget"
	case errors.Is(err, ErrPermission):
		return "owner_revoked"
	case errors.Is(err, bridge.ErrOutcomeUnknown):
		return "outcome_unknown"
	case errors.Is(err, bridge.ErrContentExpired):
		return "content_expired"
	default:
		return "operation_stopped"
	}
}

// String formatting stays metadata-only; raw provider/transport errors are
// never forwarded to ordinary logs or task status.
func taskSummary(t storage.ComputerUseTask) string {
	return fmt.Sprintf("任务 %s：%s（%s）。GUI 停止确认：%t。", t.ID, t.State, t.Code, t.GUIStopped)
}
