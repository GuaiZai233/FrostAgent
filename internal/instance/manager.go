package instance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	pbconnect "github.com/GuaiZai233/FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"github.com/GuaiZai233/FrostAgent/internal/backup"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/envinject"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox/codeinterpreter"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	logsvc "github.com/GuaiZai233/FrostAgent/internal/service/logs"
	mcpsvc "github.com/GuaiZai233/FrostAgent/internal/service/mcp"
	routersvc "github.com/GuaiZai233/FrostAgent/internal/service/modelrouter"
	settingssvc "github.com/GuaiZai233/FrostAgent/internal/service/settings"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var ErrBusy = errors.New("实例正忙")
var ErrNameExists = errors.New("实例名称已存在")
var ErrDeleting = errors.New("实例正在删除，请重试删除操作")
var ErrClosing = errors.New("Control Plane 正在关闭")
var idPattern = regexp.MustCompile("^[a-f0-9]{8}$")

type Info struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	CreatedAt         time.Time `json:"created_at"`
	Enabled           bool      `json:"enabled"`
	Error             string    `json:"error,omitempty"`
	RestartRequired   bool      `json:"restart_required,omitempty"`
	Deleting          bool      `json:"deleting,omitempty"`
	CredentialTargets []string  `json:"credential_targets,omitempty"`
}
type registry struct {
	Version    int    `json:"version"`
	NextNumber int    `json:"next_number"`
	Instances  []Info `json:"instances"`
}
type managed struct {
	id      string // Immutable canonical ID from the registry, never a request path.
	op      sync.RWMutex
	mu      sync.RWMutex
	runtime *Runtime
	config  *instanceconfig.Store
	logger  *logs.Store
	mcp     *mcp.Manager
}

type runtimeBuilder func(*Manager, *managed, *instanceconfig.Store, bool) (*Runtime, error)

type Manager struct {
	mu              sync.RWMutex
	root            string
	global          *instanceconfig.Store
	registry        registry
	instances       map[string]*managed
	billing         atomic.Pointer[billing.Client]
	globalApplyMu   sync.Mutex
	settingsWriteMu settingsWriteLock
	appliedGlobal   map[string]string
	globalApplyHook func() error
	mcpGetenv       func(string) string
	sandbox         *sandbox.ConfigManager
	security        *security.Controller
	general         http.Handler
	shutdown        context.Context
	shutdownCancel  context.CancelFunc
	closeOnce       sync.Once
	db              *storage.DB
	buildRuntime    runtimeBuilder
}

// NewDatabase starts the control plane with SQL as its only runtime state
// source. Legacy JSON, dotenv and dialogue YAML files are never read.
func NewDatabase(root string) (*Manager, error) {
	db, err := storage.Open(context.Background(), root)
	if err != nil {
		return nil, err
	}
	return NewDatabaseWithDB(root, db)
}

// NewDatabaseWithDB adopts an already connected database for a control plane.
func NewDatabaseWithDB(root string, db *storage.DB) (*Manager, error) {
	if db == nil {
		return nil, fmt.Errorf("instance manager requires SQL storage")
	}
	global, err := instanceconfig.OpenDatabase(db, "", true)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	m, err := newManager(root, global, db, buildManagedRuntime)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return m, nil
}

func buildManagedRuntime(m *Manager, i *managed, config *instanceconfig.Store, enabled bool) (*Runtime, error) {
	wsListenAddr := strings.TrimSpace(m.global.Get("WS_LISTEN_ADDR"))
	if wsListenAddr == "" {
		wsListenAddr = instanceconfig.DefaultWSListenAddr
	}
	return buildRuntime(m.dir(i.id), "/instances/"+i.id, wsListenAddr, config, m.global, i.logger, m.billing.Load(), i.mcp, m.mcpGetenv, m.sandbox, i.id, enabled, m.security, m.db, &m.settingsWriteMu)
}

func newManager(root string, global *instanceconfig.Store, db *storage.DB, builder runtimeBuilder) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	shutdown, shutdownCancel := context.WithCancel(context.Background())
	m := &Manager{root: abs, global: global, instances: map[string]*managed{}, registry: registry{Version: 1, NextNumber: 1, Instances: []Info{}}, shutdown: shutdown, shutdownCancel: shutdownCancel, db: db, buildRuntime: builder}
	m.appliedGlobal = global.Snapshot()
	m.security = security.NewControllerSQL(db)
	if global != nil {
		rawTimeout := strings.TrimSpace(global.Get("SECURITY_GATEWAY_TIMEOUT"))
		if rawTimeout == "" {
			rawTimeout = strings.TrimSpace(global.Get("SECURITY_CLASSIFIER_TIMEOUT"))
		}
		if rawTimeout != "" {
			m.security.SetClassifierTimeout(security.ValidateClassifierTimeout(rawTimeout, security.DefaultClassifierTimeout))
		}

		rawMode := strings.TrimSpace(global.Get("SECURITY_CONTROL_MODE"))
		if rawMode != "" && !security.IsValidControlMode(rawMode) {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("SECURITY_CONTROL_MODE 配置值非法 %q，安全回退到 simple 模式", rawMode))
		}
		mode := security.ParseControlMode(rawMode)
		m.security.SetMode(mode)
	}
	records, nextNumber, err := db.LoadInstances(context.Background())
	if err != nil {
		return nil, err
	}
	m.registry.NextNumber = nextNumber
	for _, record := range records {
		m.registry.Instances = append(m.registry.Instances, Info{ID: record.ID, Name: record.Name,
			CreatedAt: record.CreatedAt, Enabled: record.Enabled, Deleting: record.Deleting,
			Error: record.Error, CredentialTargets: record.CredentialTargets})
	}
	if m.registry.Version != 1 || m.registry.NextNumber < 1 {
		return nil, fmt.Errorf("无效的实例注册表")
	}
	if err := m.recoverDeletionStages(); err != nil {
		return nil, fmt.Errorf("recover interrupted instance deletion: %w", err)
	}

	recoveryErrors := make(map[string]error)
	for index, info := range m.registry.Instances {
		if !idPattern.MatchString(info.ID) {
			return nil, fmt.Errorf("无效的实例 ID")
		}
		for j := 0; j < index; j++ {
			if m.registry.Instances[j].ID == info.ID || strings.EqualFold(m.registry.Instances[j].Name, info.Name) {
				return nil, fmt.Errorf("重复的实例 ID 或名称")
			}
		}
		if err := safeTree(m.dir(info.ID)); err != nil {
			recoveryErrors[info.ID] = err
			continue
		}
	}
	cfg := billing.LoadConfig(global.Get)
	base := cfg.BaseURL
	if base == "" {
		base = billing.DefaultAlcyoneBaseURL
	}
	m.billing.Store(billing.NewClient(base, cfg.ServiceToken, cfg.Timeout))
	mux := http.NewServeMux()
	path, handler := pbconnect.NewModelRouterServiceHandler(routersvc.New(modelrouter.NewSQL(db, "")))
	mux.Handle(path, handler)
	p, h := pbconnect.NewLogServiceHandler(logsvc.New(logs.General))
	mux.Handle(p, h)
	mux.HandleFunc(logs.LogImagePathPrefix, logs.General.ImageHandler)
	mux.HandleFunc("/api/v1/messages/send", m.handleDefaultSendMessage)
	mux.HandleFunc("/api/actionscat/", m.handleDefaultActionsCat)
	mux.HandleFunc("/api/actionscat", m.handleDefaultActionsCat)
	mux.HandleFunc("/api/v1/actionscat/", m.handleDefaultActionsCat)
	mux.HandleFunc("/api/v1/actionscat", m.handleDefaultActionsCat)
	m.general = mux
	sandboxCfg := sandbox.LoadConfig(global.Get)
	m.sandbox = sandbox.NewConfigManager(sandboxCfg)
	if sandboxCfg.Enabled {
		if err := sandboxCfg.Validate(); err != nil {
			logs.General.Warn(logs.SYSTEM, fmt.Sprintf("沙箱配置无效: %v（可在管理面板中修正后重新启用）", err))
		} else {
			backend := codeinterpreter.New(sandboxCfg)
			healthCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			healthErr := backend.Health(healthCtx)
			cancel()
			if healthErr != nil {
				logs.General.Warn(logs.SYSTEM, fmt.Sprintf("沙箱运行时暂时不可达 (%v)；execute_command 将 fail-closed，恢复后自动生效", healthErr))
			} else {
				logs.General.Info(logs.SYSTEM, fmt.Sprintf("沙箱执行运行时已就绪: %s", sandboxCfg.BaseURL))
			}
		}
	} else {
		logs.General.Info(logs.SYSTEM, "沙箱命令执行已注册（当前未启用，可在管理面板中开启）")
	}
	m.mcpGetenv = global.Get
	for index, info := range m.registry.Instances {
		pathError := recoveryErrors[info.ID]
		i := &managed{id: info.ID, logger: logs.New(info.ID, info.Name, logs.DefaultBufferSize)}
		m.instances[info.ID] = i
		if pathError != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = pathError.Error()
			continue
		}
		if info.Deleting || info.Enabled {
			continue
		}
		r, c, err := m.buildFresh(info.ID, i, false)
		i.config = c
		i.runtime = r
		if err != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = err.Error()
		} else if c != nil && c.Error() != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = c.Error().Error()
		} else if info.Error != "" {
			if err := m.update(info.ID, func(info *Info) { info.Error = "" }); err != nil {
				i.logger.Warn(logs.SYSTEM, fmt.Sprintf("保存实例恢复状态失败: %v", err))
			}
		}
	}
	for _, info := range append([]Info{}, m.registry.Instances...) {
		if info.Enabled && !info.Deleting {
			if err := m.Enable(info.ID, true); err != nil {
				logs.General.Error(logs.SYSTEM, fmt.Sprintf("实例 %s 启动失败: %v", info.Name, err))
			}
		}
	}
	return m, nil
}
func (m *Manager) dir(id string) string { return filepath.Join(m.root, "instance_"+id) }

var mcpBuiltinNames = []string{"send_message", "stay_silent", "use_subagent", "memory", "send_sticker", "steal_sticker", "inspect_message_image", "execute_command", "load_skill", "read_skill_resource", "run_skill"}

func (m *Manager) ensureInstanceMCP(i *managed) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.mcp != nil {
		return
	}
	configStore := mcp.NewSQLConfigStore(m.db, i.id)
	manager := mcp.NewInactiveManager(configStore, mcpBuiltinNames)
	if err := manager.Load(); err != nil {
		i.logger.Warn(logs.SYSTEM, fmt.Sprintf("加载实例 MCP 配置失败: %v", err))
	}
	i.mcp = manager
}

func (m *Manager) buildFresh(id string, i *managed, enabled bool) (*Runtime, *instanceconfig.Store, error) {
	id = i.id
	if err := safeTree(m.dir(id)); err != nil {
		return nil, nil, err
	}
	c, openErr := instanceconfig.OpenDatabase(m.db, id, false)
	if c == nil {
		return nil, nil, openErr
	}
	if openErr != nil && c.AccessError() != nil {
		return nil, c, openErr
	}
	m.ensureInstanceMCP(i)
	r, err := m.buildRuntime(m, i, c, enabled)
	return r, c, errors.Join(openErr, err)
}

func (m *Manager) persistInfoLocked(info Info) error {
	return m.db.UpdateInstance(context.Background(), storage.InstanceRecord{
		ID: info.ID, Name: info.Name, CreatedAt: info.CreatedAt,
		Enabled: info.Enabled, Deleting: info.Deleting, Error: info.Error,
		CredentialTargets: info.CredentialTargets,
	})
}
func (m *Manager) List() ([]Info, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Info{}, m.registry.Instances...), m.registry.NextNumber
}

// SecurityController returns the single controller shared by every runtime.
func (m *Manager) SecurityController() *security.Controller { return m.security }

// GlobalConfig returns the global configuration store.
func (m *Manager) GlobalConfig() *instanceconfig.Store { return m.global }

func (m *Manager) SetGlobalApplyHook(hook func() error) { m.globalApplyHook = hook }

func (m *Manager) applyGlobalSettings() error {
	if m.globalApplyHook != nil {
		return m.globalApplyHook()
	}
	return m.ApplyGlobalSettings()
}

// ControlPlaneGetenv returns the configuration getter used for control plane authorization.
func (m *Manager) ControlPlaneGetenv() func(string) string { return m.mcpGetenv }

// Engine returns an instance engine by its canonical registry ID.
func (m *Manager) Engine(id string) (*llm.Engine, error) {
	if id == "default" {
		items, _ := m.List()
		for _, item := range items {
			if item.Enabled {
				id = item.ID
				break
			}
		}
	}
	i, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.runtime == nil {
		return nil, fs.ErrNotExist
	}
	return i.runtime.Engine, nil
}

func (m *Manager) lookup(id string) (*managed, error) {
	if !idPattern.MatchString(id) {
		return nil, fs.ErrNotExist
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	i := m.instances[id]
	if i == nil {
		return nil, fs.ErrNotExist
	}
	return i, nil
}
func (m *Manager) update(id string, fn func(*Info)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.registry.Instances {
		if m.registry.Instances[index].ID == id {
			old := m.registry.Instances[index]
			fn(&m.registry.Instances[index])
			if err := m.persistInfoLocked(m.registry.Instances[index]); err != nil {
				m.registry.Instances[index] = old
				return err
			}
			return nil
		}
	}
	return fs.ErrNotExist
}
func (m *Manager) rejectDeleting(ids ...string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range ids {
		for _, info := range m.registry.Instances {
			if info.ID == id && info.Deleting {
				return ErrDeleting
			}
		}
	}
	return nil
}
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 32 {
		return "", fmt.Errorf("实例名称须为 1–32 个字符")
	}
	return name, nil
}
func (m *Manager) Create(name string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shutdown.Err() != nil {
		return Info{}, ErrClosing
	}
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("实例%d", m.registry.NextNumber)
	}
	name, err := validateName(name)
	if err != nil {
		return Info{}, err
	}
	for _, v := range m.registry.Instances {
		if strings.EqualFold(v.Name, name) {
			return Info{}, ErrNameExists
		}
	}
	var id string
	for {
		b := make([]byte, 4)
		if _, err = rand.Read(b); err != nil {
			return Info{}, err
		}
		id = hex.EncodeToString(b)
		_, err = os.Lstat(m.dir(id))
		if os.IsNotExist(err) && m.instances[id] == nil {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			return Info{}, err
		}
	}
	return m.createDatabaseInstanceLocked(id, name)
}

func (m *Manager) createDatabaseInstanceLocked(id, name string) (Info, error) {
	if err := os.Mkdir(m.dir(id), 0700); err != nil {
		return Info{}, err
	}
	info := Info{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	if err := m.db.CreateInstance(context.Background(), storage.InstanceRecord{
		ID: id, Name: name, CreatedAt: info.CreatedAt,
	}, m.registry.NextNumber+1); err != nil {
		return Info{}, err
	}
	m.registry.Instances = append(m.registry.Instances, info)
	m.registry.NextNumber++
	i := &managed{id: id, logger: logs.New(id, name, logs.DefaultBufferSize)}
	m.instances[id] = i
	if err := instanceconfig.InitializeInstanceSettings(m.db, id); err != nil {
		info.Error = err.Error()
		m.registry.Instances[len(m.registry.Instances)-1] = info
		_ = m.persistInfoLocked(info)
		return info, nil
	}
	r, c, err := m.buildFresh(id, i, false)
	i.runtime, i.config = r, c
	if err != nil {
		info.Error = err.Error()
		m.registry.Instances[len(m.registry.Instances)-1] = info
		_ = m.persistInfoLocked(info)
	}
	logs.General.InfoWithConsoleSummary(logs.SYSTEM, "创建实例: "+name, "创建实例: "+name)
	return info, nil
}
func (m *Manager) Rename(id, name string) error {
	name, err := validateName(name)
	if err != nil {
		return err
	}
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	id = i.id
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	if err = m.rejectDeleting(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.registry.Instances {
		if v.ID != id && strings.EqualFold(v.Name, name) {
			return fmt.Errorf("实例名称已存在")
		}
	}
	for index := range m.registry.Instances {
		if m.registry.Instances[index].ID == id {
			old := m.registry.Instances[index].Name
			m.registry.Instances[index].Name = name
			if err = m.persistInfoLocked(m.registry.Instances[index]); err != nil {
				m.registry.Instances[index].Name = old
				return err
			}
			i.logger.Rename(name)
			return nil
		}
	}
	return fs.ErrNotExist
}
func (m *Manager) stop(id string, i *managed) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.mcp != nil {
		if err := i.mcp.SetRuntimeActive(false); err != nil && !errors.Is(err, mcp.ErrManagerClosed) {
			i.logger.Warn(logs.SYSTEM, fmt.Sprintf("停止实例 MCP 连接失败: %v", err))
		}
	}
	if i.runtime != nil {
		i.runtime.Stop()
	}
	if m.security != nil {
		m.security.RemoveInstanceProvider(id)
	}
	i.logger.EndStreams()
	err := m.update(id, func(info *Info) { info.Enabled = false })
	if err != nil {
		m.mu.Lock()
		for index := range m.registry.Instances {
			if m.registry.Instances[index].ID == id {
				m.registry.Instances[index].Enabled = false
				m.registry.Instances[index].Error = err.Error()
			}
		}
		m.mu.Unlock()
	}
	return err
}
func (m *Manager) Enable(id string, enabled bool) error {
	i, err := m.lookup(id)
	if err != nil {
		return err
	}
	id = i.id
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if _, err = m.lookup(id); err != nil {
		return err
	}
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	if err = m.rejectDeleting(id); err != nil {
		return err
	}

	list, _ := m.List()
	for _, info := range list {
		if info.ID == id {
			if info.Enabled == enabled && i.runtime != nil && (i.runtime.Scope.Context().Err() == nil) == enabled {
				return nil
			}
		}
	}
	if err = m.stop(id, i); err != nil {
		return err
	}
	r, c, err := m.buildFresh(id, i, enabled)
	if err != nil {
		if r != nil {
			r.Stop()
		}
		// Retain a stopped management runtime so invalid settings remain repairable.
		fallback, fallbackConfig, fallbackErr := m.buildFresh(id, i, false)
		i.mu.Lock()
		i.runtime = fallback
		i.config = fallbackConfig
		i.mu.Unlock()
		if fallbackErr != nil {
			err = errors.Join(err, fallbackErr)
		}
		_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	i.mu.Lock()
	i.runtime = r
	i.config = c
	i.mu.Unlock()
	if m.shutdown.Err() != nil {
		r.Stop()
		if i.mcp != nil {
			_ = i.mcp.SetRuntimeActive(false)
		}
		_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = ErrClosing.Error() })
		return ErrClosing
	}
	if enabled && i.mcp != nil {
		if err = i.mcp.SetRuntimeActive(true); err != nil {
			r.Stop()
			_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
			return err
		}
	}
	if err = m.update(id, func(info *Info) { info.Enabled = enabled; info.Error = ""; info.RestartRequired = false }); err != nil {
		r.Stop()
		if i.mcp != nil {
			_ = i.mcp.SetRuntimeActive(false)
		}
		return err
	}
	lifecycleLog := fmt.Sprintf("实例 %s enabled=%t", id, enabled)
	r.StartBackground()
	logs.General.InfoWithConsoleSummary(logs.SYSTEM, lifecycleLog, lifecycleLog)
	return nil
}

// safeTree refuses symlinks/junctions before an owned path is read or removed.
func safeTree(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("实例目录不是安全目录: %s", dir)
	}
	return filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("实例目录包含链接，拒绝操作: %s", path)
		}
		return nil
	})
}
func (m *Manager) Delete(id string, all bool) error {
	return fmt.Errorf("use prepare, backup, and confirm to delete a SQL instance")
}

func (m *Manager) Copy(target, source string) error {
	return m.copySQLInstanceSettings(target, source)
}
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.shutdownCancel()
		logs.General.EndStreams()
		m.mu.RLock()
		items := []*managed{}
		for _, i := range m.instances {
			items = append(items, i)
		}
		m.mu.RUnlock()
		for _, i := range items {
			i.logger.EndStreams()
			i.mu.RLock()
			closingMCP := i.mcp
			i.mu.RUnlock()
			if closingMCP != nil {
				_ = closingMCP.Close()
			}
			i.op.Lock()
			i.mu.Lock()
			if i.mcp != nil && i.mcp != closingMCP {
				_ = i.mcp.Close()
			}
			if i.runtime != nil {
				i.runtime.Stop()
			}
			i.mu.Unlock()
			i.op.Unlock()
		}
		_ = m.db.Close()
	})
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m.shutdown.Err() != nil {
		http.Error(w, ErrClosing.Error(), http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	stopCancel := context.AfterFunc(m.shutdown, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()
	r = r.Clone(ctx)
	if strings.HasPrefix(r.URL.Path, "/api/instances") {
		m.api(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/instances/") {
		m.general.ServeHTTP(w, r)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/instances/"), "/", 2)
	if len(parts) != 2 || !idPattern.MatchString(parts[0]) {
		http.NotFound(w, r)
		return
	}
	m.serveInstance(w, r, parts[0], "/"+parts[1])
}

func (m *Manager) serveInstance(w http.ResponseWriter, r *http.Request, id, path string) {
	i, err := m.lookup(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id = i.id
	ws := strings.HasPrefix(path, "/ws/")
	stream := strings.HasSuffix(path, "/StreamLogs")
	isSendMessage := path == "/api/v1/messages/send" || strings.HasSuffix(path, "/api/v1/messages/send")
	isActionsCat := strings.HasPrefix(path, "/api/actionscat") || strings.HasPrefix(path, "/api/v1/actionscat")
	if isActionsCat {
		if err := mcpsvc.CheckControlPlaneAuthScoped(remoteAddr(r), r.Header, m.mcpGetenv); err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "unauthorized: " + err.Error(),
			})
			return
		}
	}
	readOnly := r.Method == "GET" || isSendMessage || isActionsCat
	method := path[strings.LastIndex(path, "/")+1:]
	for _, prefix := range []string{"Get", "List", "Search", "Export", "Test"} {
		if strings.HasPrefix(method, prefix) {
			readOnly = true
		}
	}
	settingsWrite := !readOnly && strings.HasPrefix(path, "/frostagent.v1.SettingsService/")
	if settingsWrite {
		m.settingsWriteMu.Lock()
		defer m.settingsWriteMu.Unlock()
	}
	var opUnlock func()
	if !ws && !stream {
		var locked bool
		if readOnly {
			locked = i.op.TryRLock()
		} else {
			locked = i.op.TryLock()
		}
		if !locked {
			writeError(w, ErrBusy)
			return
		}
		if readOnly {
			opUnlock = i.op.RUnlock
		} else {
			opUnlock = i.op.Unlock
		}
		defer func() {
			if opUnlock != nil {
				opUnlock()
			}
		}()
		if _, err = m.lookup(id); err != nil {
			http.NotFound(w, r)
			return
		}
		if m.shutdown.Err() != nil {
			writeError(w, ErrClosing)
			return
		}
	}
	i.mu.RLock()
	rt := i.runtime
	i.mu.RUnlock()
	if rt == nil {
		http.Error(w, "实例配置不可用", 503)
		return
	}
	if (ws || stream || isSendMessage || isActionsCat) && rt.Scope.Context().Err() != nil {
		http.Error(w, "实例未启用", 503)
		return
	}
	requestContext := r.Context()
	if stream {
		streamContext, streamCancel := context.WithCancel(requestContext)
		stopRuntimeCancel := context.AfterFunc(rt.Scope.Context(), streamCancel)
		defer func() {
			stopRuntimeCancel()
			streamCancel()
		}()
		requestContext = streamContext
	}
	req := r.Clone(requestContext)
	urlCopy := *r.URL
	urlCopy.Path = path
	req.URL = &urlCopy
	var before, globalBefore map[string]string
	settingsDraft := settingsWrite && strings.HasSuffix(path, "/UpdateEnvVars")
	wasRunning := rt.Scope.Context().Err() == nil
	if !ws && !stream {
		before = i.config.Snapshot()
	}
	if settingsWrite {
		globalBefore = m.global.Snapshot()
	}
	response := w
	var buffered *httptest.ResponseRecorder
	if settingsWrite {
		buffered = httptest.NewRecorder()
		response = buffered
	}
	rt.Handler.ServeHTTP(response, req)
	if !ws && !stream {
		after := i.config.Snapshot()
		globalAfter := m.global.Snapshot()
		instanceReload := !readOnly && !equalSettings(before, after) && !onlyHotInstanceChanges(before, after)
		if instanceReload {
			if err := m.reloadSQLRuntimeLocked(i, wasRunning, before); err != nil {
				i.logger.Error(logs.SYSTEM, fmt.Sprintf("应用数据库设置失败: %v", err))
				if settingsWrite {
					if settingsDraft {
						opUnlock()
						opUnlock = nil
						err = errors.Join(err, m.rollbackSettingsDraft(i, before, after, globalBefore, globalAfter, wasRunning, true))
					}
					writeError(w, err)
				}
				return
			}
		}
		if settingsWrite {
			if !equalSettings(globalBefore, m.global.Snapshot()) {
				opUnlock()
				opUnlock = nil
				if err := m.applyGlobalSettings(); err != nil {
					if settingsDraft {
						writeError(w, errors.Join(err, m.rollbackSettingsDraft(i, before, after, globalBefore, globalAfter, wasRunning, instanceReload)))
						return
					}
					rollbackErr := m.restoreGlobalSnapshot(globalBefore)
					if rollbackErr == nil {
						rollbackErr = m.applyGlobalSettings()
					}
					writeError(w, errors.Join(err, rollbackErr))
					return
				}
			}
			i.logger.SetPersistentBudget(logs.PersistentBudget(i.config.Get(logs.LogBudgetKey)))
			for key, values := range buffered.Header() {
				w.Header()[key] = append([]string(nil), values...)
			}
			w.WriteHeader(buffered.Code)
			_, _ = w.Write(buffered.Body.Bytes())
		}
		return
	}
}

func (m *Manager) handleDefaultSendMessage(w http.ResponseWriter, r *http.Request) {
	if m.shutdown.Err() != nil {
		http.Error(w, ErrClosing.Error(), http.StatusServiceUnavailable)
		return
	}
	targetInstanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if targetInstanceID == "" {
		targetInstanceID = strings.TrimSpace(r.Header.Get("X-Instance-ID"))
	}

	if targetInstanceID == "" && r.Body != nil && r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to read request body: " + err.Error()})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		var peek struct {
			InstanceID string `json:"instance_id"`
		}
		if err := json.Unmarshal(bodyBytes, &peek); err == nil {
			targetInstanceID = strings.TrimSpace(peek.InstanceID)
		}
	}

	if targetInstanceID != "" {
		if _, err := m.lookup(targetInstanceID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("instance %q not found", targetInstanceID),
			})
			return
		}
		m.serveInstance(w, r, targetInstanceID, "/api/v1/messages/send")
		return
	}

	selectedID := m.findActiveInstanceID()
	if selectedID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no active instance available to handle message send"})
		return
	}

	m.serveInstance(w, r, selectedID, "/api/v1/messages/send")
}

func (m *Manager) handleDefaultActionsCat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if m.shutdown.Err() != nil {
		http.Error(w, ErrClosing.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := mcpsvc.CheckControlPlaneAuthScoped(remoteAddr(r), r.Header, m.mcpGetenv); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "unauthorized: " + err.Error(),
		})
		return
	}
	targetInstanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if targetInstanceID == "" {
		targetInstanceID = strings.TrimSpace(r.Header.Get("X-Instance-ID"))
	}
	if targetInstanceID != "" {
		if _, err := m.lookup(targetInstanceID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("instance %q not found", targetInstanceID),
			})
			return
		}
		m.serveInstance(w, r, targetInstanceID, r.URL.Path)
		return
	}

	selectedID := m.findActiveInstanceID()
	if selectedID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no active instance available to handle actionscat request"})
		return
	}
	m.serveInstance(w, r, selectedID, r.URL.Path)
}

func (m *Manager) findActiveInstanceID() string {
	// Candidate selection:
	// IMPORTANT: To avoid lock order inversion with lifecycle operations like stop()
	// (which acquires i.mu.Lock then m.mu.Lock via m.update), NEVER acquire i.mu while holding m.mu.
	m.mu.RLock()
	var enabledCandidates []string
	var allCandidates []string
	for _, info := range m.registry.Instances {
		if m.instances[info.ID] != nil {
			if info.Enabled {
				enabledCandidates = append(enabledCandidates, info.ID)
			}
			allCandidates = append(allCandidates, info.ID)
		}
	}
	m.mu.RUnlock()

	for _, id := range enabledCandidates {
		i, err := m.lookup(id)
		if err != nil {
			continue
		}
		i.mu.RLock()
		rt := i.runtime
		i.mu.RUnlock()
		if rt != nil && rt.Scope.Context().Err() == nil {
			return id
		}
	}
	for _, id := range allCandidates {
		i, err := m.lookup(id)
		if err != nil {
			continue
		}
		i.mu.RLock()
		rt := i.runtime
		i.mu.RUnlock()
		if rt != nil {
			return id
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, err error) {
	code := 400
	if errors.Is(err, ErrBusy) {
		code = 409
	}
	if errors.Is(err, ErrClosing) {
		code = http.StatusServiceUnavailable
	}
	if errors.Is(err, fs.ErrNotExist) {
		code = 404
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "code": "aborted", "message": err.Error()})
}

const blobDownloadHeader = "X-FrostAgent-Download"

func isBlobDownloadRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.EqualFold(strings.TrimSpace(r.Header.Get(blobDownloadHeader)), "blob")
}

func (m *Manager) api(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/instances"), "/")
	if path == "global/import/settings" && r.Method == http.MethodPost {
		m.settingsWriteMu.Lock()
		defer m.settingsWriteMu.Unlock()
	}
	if path == "global/extra-env" || strings.HasPrefix(path, "global/extra-env/") ||
		path == "global/backup/settings" || path == "global/import/settings" {
		w.Header().Set("Cache-Control", "no-store")
		if err := mcpsvc.CheckSensitiveControlPlaneAuthScoped(remoteAddr(r), r.Header, m.mcpGetenv); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if path == "global/extra-env" || strings.HasPrefix(path, "global/extra-env/") {
		m.serveExtraEnv(w, r, path)
		return
	}
	if path == "global/backup/security" && r.Method == http.MethodGet {
		var state backup.GlobalSecurity
		err := m.security.WithStateLock(func() error {
			var exportErr error
			state, exportErr = backup.ExportGlobalSecurity(m.db)
			return exportErr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="global-security.json"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(append(data, '\n'))
		return
	}
	if path == "global/import/security" && r.Method == http.MethodPost {
		var state backup.GlobalSecurity
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&state); err != nil {
			writeError(w, err)
			return
		}
		if err := m.security.WithStateLock(func() error {
			return backup.ImportGlobalSecurity(m.db, state)
		}); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	if path == "global/backup/settings" && r.Method == http.MethodGet {
		settings := backup.ExportGlobalSettings(m.global)
		entries, err := envinject.List(r.Context(), m.db)
		if err != nil {
			writeError(w, err)
			return
		}
		extra := make(map[string]string, len(entries))
		for _, entry := range entries {
			extra[entry.Key] = entry.Value
		}
		settings.ExtraEnv = &extra
		data, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="global-setting.json"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(append(data, '\n'))
		return
	}
	if path == "global/import/settings" && r.Method == http.MethodPost {
		before := m.global.Snapshot()
		var data backup.GlobalSettings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&data); err != nil {
			writeError(w, err)
			return
		}
		var previousExtra map[string]string
		if data.ExtraEnv != nil {
			for key, value := range *data.ExtraEnv {
				if settingssvc.IsManagedKey(key) {
					writeError(w, fmt.Errorf("%s belongs to managed settings", key))
					return
				}
				if err := envinject.Validate(key, value); err != nil {
					writeError(w, err)
					return
				}
			}
			entries, err := envinject.List(r.Context(), m.db)
			if err != nil {
				writeError(w, err)
				return
			}
			previousExtra = make(map[string]string, len(entries))
			for _, entry := range entries {
				previousExtra[entry.Key] = entry.Value
			}
		}
		if err := backup.ImportGlobalSettings(m.global, data); err != nil {
			writeError(w, err)
			return
		}
		if data.ExtraEnv != nil {
			if err := envinject.Replace(r.Context(), m.db, *data.ExtraEnv); err != nil {
				writeError(w, errors.Join(err, m.restoreGlobalSnapshot(before)))
				return
			}
		}
		if err := m.applyGlobalSettings(); err != nil {
			rollbackErr := m.restoreGlobalSnapshot(before)
			if data.ExtraEnv != nil {
				rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				rollbackErr = errors.Join(rollbackErr, envinject.Replace(rollbackCtx, m.db, previousExtra))
				cancel()
			}
			if rollbackErr == nil {
				rollbackErr = m.applyGlobalSettings()
			}
			writeError(w, errors.Join(err, rollbackErr))
			return
		}
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	if path == "restore" && r.Method == http.MethodPost {
		m.handleInstanceRestore(w, r)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[1] == "import" && parts[2] == "settings" && r.Method == http.MethodPost {
		m.handleSettingsImport(w, r, parts[0])
		return
	}
	if len(parts) == 3 && parts[1] == "import" && parts[2] == "memories" && r.Method == http.MethodPost {
		m.handleMemoryImport(w, r, parts[0])
		return
	}
	if len(parts) == 3 && parts[1] == "backup" && (r.Method == http.MethodGet || isBlobDownloadRequest(r)) {
		data, mediaType, fileName, err := m.InstancePart(parts[0], parts[2])
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", mediaType)
		if !isBlobDownloadRequest(r) {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if len(parts) == 2 && parts[1] == "backup" && (r.Method == http.MethodGet || isBlobDownloadRequest(r)) {
		data, err := m.InstanceZIP(parts[0])
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		if !isBlobDownloadRequest(r) {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="frostagent-%s.zip"`, parts[0]))
		}
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
		return
	}
	if len(parts) == 3 && parts[1] == "delete" && r.Method == http.MethodPost {
		var err error
		switch parts[2] {
		case "prepare":
			err = m.PrepareDeletion(parts[0])
		case "cancel":
			err = m.CancelDeletion(parts[0])
		case "confirm":
			err = m.ConfirmDeletion(parts[0])
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"success": true})
		return
	}
	if path == "" && r.Method == "GET" {
		items, next := m.List()
		writeJSON(w, map[string]any{"instances": items, "next_name": fmt.Sprintf("实例%d", next)})
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		All     bool   `json:"all"`
		Source  string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, err)
		return
	}
	var err error
	if path == "" {
		info, e := m.Create(req.Name)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, info)
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "rename":
		err = m.Rename(parts[0], req.Name)
	case "enable":
		err = m.Enable(parts[0], req.Enabled)
	case "delete":
		err = m.Delete(parts[0], req.All)
	case "copy":
		err = m.Copy(parts[0], req.Source)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func remoteAddr(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.RemoteAddr)
}
