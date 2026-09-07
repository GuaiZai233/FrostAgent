package instance

import (
	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/service/dialogue"
	logsvc "FrostAgent/internal/service/logs"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrBusy = errors.New("实例正忙")
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
}
type Manager struct {
	mu             sync.RWMutex
	root           string
	global         *instanceconfig.Store
	wsListenAddr   string
	registry       registry
	instances      map[string]*managed
	shared         *dialogue.Service
	billing        *billing.Client
	endpointMu     sync.Mutex
	endpointOwners map[string]string
	general        http.Handler
	shutdown       context.Context
	shutdownCancel context.CancelFunc
	closeOnce      sync.Once
}

func New(root string, global *instanceconfig.Store, dialoguePath string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	shutdown, shutdownCancel := context.WithCancel(context.Background())
	wsListenAddr := strings.TrimSpace(global.Get("WS_LISTEN_ADDR"))
	if wsListenAddr == "" {
		wsListenAddr = "127.0.0.1:1234"
	}
	m := &Manager{root: abs, global: global, wsListenAddr: wsListenAddr, instances: map[string]*managed{}, endpointOwners: map[string]string{}, registry: registry{Version: 1, NextNumber: 1, Instances: []Info{}}, shared: dialogue.New(dialoguePath, nil), shutdown: shutdown, shutdownCancel: shutdownCancel}
	data, err := os.ReadFile(filepath.Join(abs, "instances.json"))
	if err == nil {
		if err = json.Unmarshal(data, &m.registry); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if m.registry.Version != 1 || m.registry.NextNumber < 1 {
		return nil, fmt.Errorf("无效的实例注册表")
	}
	data, err = os.ReadFile(filepath.Join(abs, "endpoint_ids.json"))
	if err == nil {
		if err = json.Unmarshal(data, &m.endpointOwners); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
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
		if err := m.recoverCopyTransaction(info.ID); err != nil {
			recoveryErrors[info.ID] = err
		}
	}
	cfg := billing.LoadConfig(global.Get)
	base := cfg.BaseURL
	if base == "" {
		base = billing.DefaultAlcyoneBaseURL
	}
	m.billing = billing.NewClient(base, cfg.ServiceToken, cfg.Timeout)
	mux := http.NewServeMux()
	p, h := pbconnect.NewLogServiceHandler(logsvc.New(logs.General))
	mux.Handle(p, h)
	mux.HandleFunc(logs.LogImagePathPrefix, logs.General.ImageHandler)
	m.general = mux
	// Retained data directories reserve their endpoint IDs too.
	paths, err := filepath.Glob(filepath.Join(abs, "instance_*", "model_router.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		dir := filepath.Dir(path)
		owner := strings.TrimPrefix(filepath.Base(dir), "instance_")
		if recoveryErrors[owner] != nil {
			continue
		}
		if err := safeTree(dir); err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var c modelrouter.Configuration
		if err = json.Unmarshal(raw, &c); err != nil {
			continue
		}
		for _, e := range c.Endpoints {
			if old, ok := m.endpointOwners[e.ID]; ok && old != owner {
				return nil, fmt.Errorf("Endpoint ID 冲突: %s", e.ID)
			}
			m.endpointOwners[e.ID] = owner
		}
	}
	for index, info := range m.registry.Instances {
		pathError := recoveryErrors[info.ID]
		i := &managed{id: info.ID, logger: logs.New(info.ID, info.Name, 5000)}
		m.instances[info.ID] = i
		if pathError != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = pathError.Error()
			continue
		}
		if info.Deleting {
			continue
		}
		r, c, err := m.buildFresh(info.ID, i, false, m.dir(info.ID))
		i.config = c
		i.runtime = r
		if err != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = err.Error()
		} else if c != nil && c.Error() != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = c.Error().Error()
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
func (m *Manager) buildFresh(id string, i *managed, enabled bool, configDir string) (*Runtime, *instanceconfig.Store, error) {
	id = i.id
	if err := safeTree(m.dir(id)); err != nil {
		return nil, nil, err
	}
	if configDir != m.dir(id) {
		if err := safeTree(configDir); err != nil {
			return nil, nil, err
		}
	}
	c, openErr := instanceconfig.Open(filepath.Join(configDir, ".env"), false)
	if openErr != nil && c.AccessError() != nil {
		return nil, c, openErr
	}
	r, err := buildRuntime(m.dir(id), configDir, "/instances/"+id, m.wsListenAddr, c, m.global, i.logger, m.shared, m.billing, enabled)
	if err == nil {
		r.Engine.ModelRouter.ReserveEndpoints = func(endpoints []modelrouter.Endpoint) error { return m.reserveEndpoints(id, endpoints) }
	}
	return r, c, errors.Join(openErr, err)
}
func (m *Manager) reserveEndpoints(owner string, endpoints []modelrouter.Endpoint) error {
	if err := modelrouter.PersistentRefs(endpoints); err != nil {
		return err
	}
	m.endpointMu.Lock()
	defer m.endpointMu.Unlock()
	return m.reserveEndpointsLocked(owner, endpoints)
}
func (m *Manager) reserveEndpointsLocked(owner string, endpoints []modelrouter.Endpoint) error {
	next, err := m.nextEndpointOwnersLocked(owner, endpoints)
	if err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = instanceconfig.WriteAtomic(filepath.Join(m.root, "endpoint_ids.json"), data, 0600); err != nil {
		return err
	}
	m.endpointOwners = next
	return nil
}
func (m *Manager) nextEndpointOwnersLocked(owner string, endpoints []modelrouter.Endpoint) (map[string]string, error) {
	if err := modelrouter.PersistentRefs(endpoints); err != nil {
		return nil, err
	}
	next := map[string]string{}
	for id, v := range m.endpointOwners {
		next[id] = v
	}
	for _, e := range endpoints {
		if existing, ok := next[e.ID]; ok {
			if existing != owner {
				return nil, fmt.Errorf("Endpoint ID 已被占用: %s", e.ID)
			}
		} else {
			exists, err := modelrouter.CredentialExists(e.ID)
			if err != nil {
				return nil, err
			}
			if exists {
				return nil, fmt.Errorf("Endpoint 凭据已存在: %s", e.ID)
			}
			next[e.ID] = owner
		}
	}
	return next, nil
}
func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.registry, "", "  ")
	if err != nil {
		return err
	}
	return instanceconfig.WriteAtomic(filepath.Join(m.root, "instances.json"), data, 0600)
}
func (m *Manager) List() ([]Info, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Info{}, m.registry.Instances...), m.registry.NextNumber
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
			if err := m.saveLocked(); err != nil {
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
func (m *Manager) Create(name string) (result Info, resultErr error) {
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
			return Info{}, fmt.Errorf("实例名称已存在")
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
	if err = os.Mkdir(m.dir(id), 0700); err != nil {
		return Info{}, err
	}
	committed := false
	var partialRuntime *Runtime
	defer func() {
		if committed {
			return
		}
		if partialRuntime != nil {
			partialRuntime.Stop()
		}
		cleanupErr := safeTree(m.dir(id))
		if cleanupErr == nil {
			cleanupErr = os.RemoveAll(m.dir(id))
		}
		resultErr = errors.Join(resultErr, cleanupErr)
	}()
	if err = instanceconfig.WriteAtomic(filepath.Join(m.dir(id), ".env"), []byte(instanceconfig.Template), 0600); err != nil {
		return Info{}, err
	}
	i := &managed{id: id, logger: logs.New(id, name, 5000)}
	r, c, err := m.buildFresh(id, i, false, m.dir(id))
	if err != nil {
		return Info{}, err
	}
	partialRuntime = r
	i.config = c
	i.runtime = r
	info := Info{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	m.registry.Instances = append(m.registry.Instances, info)
	m.registry.NextNumber++
	if err = m.saveLocked(); err != nil {
		m.registry.Instances = m.registry.Instances[:len(m.registry.Instances)-1]
		m.registry.NextNumber--
		return Info{}, err
	}
	m.instances[id] = i
	committed = true
	createdLog := "创建实例: " + name
	logs.General.InfoWithConsoleSummary(logs.SYSTEM, createdLog, createdLog)
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
			if err = m.saveLocked(); err != nil {
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
	if i.runtime != nil {
		i.runtime.Stop()
	}
	i.logger.EndStreams()
	i.logger.Clear()
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
	_, transactionErr := os.Stat(transactionPath(m.dir(id)))
	hadTransaction := transactionErr == nil
	if transactionErr != nil && !os.IsNotExist(transactionErr) {
		return transactionErr
	}
	if err = m.recoverCopyTransaction(id); err != nil {
		_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	list, _ := m.List()
	for _, info := range list {
		if info.ID == id {
			if !hadTransaction && info.Enabled == enabled && i.runtime != nil && (i.runtime.Scope.Context().Err() == nil) == enabled {
				return nil
			}
		}
	}
	if err = m.stop(id, i); err != nil {
		return err
	}
	r, c, err := m.buildFresh(id, i, enabled, m.dir(id))
	if err != nil {
		if r != nil {
			r.Stop()
		}
		// Keep a stopped management runtime over the newly read file when possible,
		// so a malformed .env can still be repaired without reviving stale values.
		fallback, fallbackConfig, fallbackErr := m.buildFresh(id, i, false, m.dir(id))
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
	if err = m.update(id, func(info *Info) { info.Enabled = enabled; info.Error = ""; info.RestartRequired = false }); err != nil {
		r.Stop()
		return err
	}
	lifecycleLog := fmt.Sprintf("实例 %s enabled=%t", id, enabled)
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
	if err = m.recoverCopyTransaction(id); err != nil {
		return err
	}
	if err = m.stop(id, i); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	if err = safeTree(m.dir(id)); err != nil {
		return fail(err)
	}

	var changes []modelrouter.CredentialChange
	list, _ := m.List()
	for _, info := range list {
		if info.ID == id {
			for _, target := range info.CredentialTargets {
				changes = append(changes, modelrouter.CredentialChange{Target: target})
			}
		}
	}
	if i.runtime != nil {
		owned, e := i.runtime.Engine.ModelRouter.DeleteCredentials()
		if e != nil {
			return fail(e)
		}
		changes = append(changes, owned...)
	}

	m.endpointMu.Lock()
	ownedIDs := []string{}
	for eid, owner := range m.endpointOwners {
		if owner == id {
			ownedIDs = append(ownedIDs, eid)
		}
	}
	m.endpointMu.Unlock()
	for _, eid := range ownedIDs {
		exists, e := modelrouter.CredentialExists(eid)
		if e != nil {
			return fail(e)
		}
		if exists {
			changes = append(changes, modelrouter.CredentialChange{Target: modelrouter.CredentialTarget(eid)})
		}
	}
	seenTargets := map[string]bool{}
	unique := []modelrouter.CredentialChange{}
	for _, change := range changes {
		if !seenTargets[change.Target] {
			seenTargets[change.Target] = true
			unique = append(unique, change)
		}
	}
	changes = unique
	targets := []string{}
	for _, change := range changes {
		targets = append(targets, change.Target)
	}
	if err = m.update(id, func(info *Info) { info.Deleting = true; info.CredentialTargets = targets }); err != nil {
		return fail(err)
	}
	// A failed deletion remains manageable only through retry-delete; stale stores
	// cannot recreate files or expose a half-deleted configuration.
	i.mu.Lock()
	i.runtime = nil
	i.mu.Unlock()
	_, err = modelrouter.ApplyCredentials(changes)
	if err != nil {
		return fail(err)
	}

	if all {
		err = os.RemoveAll(m.dir(id))
	} else {
		for _, name := range []string{".env", "model_router.json", "model_router_secrets.json"} {
			e := os.Remove(filepath.Join(m.dir(id), name))
			if e != nil && !os.IsNotExist(e) {
				err = e
				break
			}
		}
	}
	if err != nil {
		return fail(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.registry.Instances
	m.registry.Instances = []Info{}
	for _, info := range old {
		if info.ID != id {
			m.registry.Instances = append(m.registry.Instances, info)
		}
	}
	if err = m.saveLocked(); err != nil {
		m.registry.Instances = old
		for index := range m.registry.Instances {
			if m.registry.Instances[index].ID == id {
				m.registry.Instances[index].Enabled = false
				m.registry.Instances[index].Error = err.Error()
			}
		}
		return err
	}
	delete(m.instances, id)
	return nil
}

func (m *Manager) Copy(target, source string) error {
	if target == source {
		return fmt.Errorf("不能复用自身")
	}
	dst, err := m.lookup(target)
	if err != nil {
		return err
	}
	src, err := m.lookup(source)
	if err != nil {
		return err
	}
	target, source = dst.id, src.id
	if !dst.op.TryLock() {
		return ErrBusy
	}
	defer dst.op.Unlock()
	if !src.op.TryLock() {
		return ErrBusy
	}
	defer src.op.Unlock()
	if _, err = m.lookup(target); err != nil {
		return err
	}
	if _, err = m.lookup(source); err != nil {
		return err
	}
	if m.shutdown.Err() != nil {
		return ErrClosing
	}
	if err = m.rejectDeleting(target, source); err != nil {
		return err
	}
	if err = m.recoverCopyTransaction(target); err != nil {
		return err
	}
	if err = m.recoverCopyTransaction(source); err != nil {
		return err
	}
	list, _ := m.List()
	targetEnabled := false
	sourceEnabled := false
	for _, info := range list {
		if info.ID == target {
			targetEnabled = info.Enabled
		}
		if info.ID == source {
			sourceEnabled = info.Enabled
		}
	}
	if targetEnabled {
		return fmt.Errorf("被覆盖的实例必须处于非活跃状态")
	}
	for id, item := range map[string]*managed{target: dst, source: src} {
		if item.runtime != nil {
			continue
		}
		enabled := sourceEnabled && id == source
		r, c, buildErr := m.buildFresh(id, item, enabled, m.dir(id))
		if buildErr != nil {
			if r != nil {
				r.Stop()
			}
			return buildErr
		}
		item.mu.Lock()
		item.runtime = r
		item.config = c
		item.mu.Unlock()
	}
	if dst.runtime == nil || src.runtime == nil {
		return fmt.Errorf("实例配置不可用")
	}
	if err = safeTree(m.dir(target)); err != nil {
		return err
	}
	raw, err := src.config.Raw()
	if err != nil {
		return err
	}
	m.endpointMu.Lock()
	endpointLocked := true
	defer func() {
		if endpointLocked {
			m.endpointMu.Unlock()
		}
	}()
	pendingEndpoints := []modelrouter.Endpoint{}
	cfg, secrets, newCreds, err := src.runtime.Engine.ModelRouter.CopyPublished(func(e []modelrouter.Endpoint) error {
		next := append(append([]modelrouter.Endpoint{}, pendingEndpoints...), e...)
		if _, reserveErr := m.nextEndpointOwnersLocked(target, next); reserveErr != nil {
			return reserveErr
		}
		pendingEndpoints = next
		return nil
	})
	if err != nil {
		return err
	}
	oldCreds, err := dst.runtime.Engine.ModelRouter.DeleteCredentials()
	if err != nil {
		return err
	}
	transaction, err := newCopyTransaction(target, newCreds, oldCreds)
	if err != nil {
		return err
	}
	targetDir := m.dir(target)
	abort := func(cause error) error {
		return errors.Join(cause, abortCopyTransaction(targetDir, transaction))
	}
	committed, writeErr := writeCopyTransaction(targetDir, transaction)
	if writeErr != nil {
		if committed {
			return errors.Join(writeErr, abortCopyTransaction(targetDir, transaction))
		}
		return writeErr
	}
	stageDir := filepath.Join(targetDir, transaction.Stage)
	if err = os.Mkdir(stageDir, 0700); err != nil {
		return abort(err)
	}
	if err = instanceconfig.SyncDirectory(targetDir); err != nil {
		return abort(err)
	}
	for name, data := range map[string][]byte{".env": []byte(raw), "model_router.json": cfg, "model_router_secrets.json": secrets} {
		if _, err = writeCopyFile(filepath.Join(stageDir, name), data, 0600); err != nil {
			return abort(err)
		}
	}
	if err = modelrouter.StageCredentialPromotions(transaction.Credentials, newCreds); err != nil {
		return abort(err)
	}
	candidate, _, err := m.buildFresh(target, dst, false, stageDir)
	if err == nil {
		err = candidate.Engine.ModelRouter.LoadError()
	}
	if err != nil {
		if candidate != nil {
			candidate.Stop()
		}
		return abort(err)
	}
	candidate.Stop()
	transaction.Phase = copyCommitting
	committed, writeErr = writeCopyTransaction(targetDir, transaction)
	if writeErr != nil && !committed {
		return abort(writeErr)
	}
	// Once the committing rename is visible, preserve recovery-forward state.
	// A directory-sync failure keeps the target unavailable until a later retry
	// durably re-establishes the marker; never expose partially installed files.
	dst.mu.Lock()
	previous := dst.runtime
	dst.runtime = nil
	dst.config = nil
	dst.mu.Unlock()
	if previous != nil {
		previous.Stop()
	}
	if writeErr != nil {
		updateErr := m.update(target, func(info *Info) { info.Enabled = false; info.Error = writeErr.Error() })
		return errors.Join(writeErr, updateErr)
	}
	if err = m.reserveEndpointsLocked(target, pendingEndpoints); err != nil {
		updateErr := m.update(target, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return errors.Join(err, updateErr)
	}
	m.endpointMu.Unlock()
	endpointLocked = false
	if err = m.recoverCopyTransaction(target); err != nil {
		_ = m.update(target, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	r, c, err := m.buildFresh(target, dst, false, targetDir)
	if err != nil {
		_ = m.update(target, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	dst.mu.Lock()
	dst.runtime = r
	dst.config = c
	dst.mu.Unlock()
	if err = m.update(target, func(info *Info) { info.Error = ""; info.RestartRequired = false }); err != nil {
		return err
	}
	return nil
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
			i.op.Lock()
			i.mu.Lock()
			if i.runtime != nil {
				i.runtime.Stop()
			}
			i.mu.Unlock()
			i.op.Unlock()
		}
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
	id := parts[0]
	i, err := m.lookup(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id = i.id
	path := "/" + parts[1]
	ws := strings.HasPrefix(path, "/ws/")
	stream := strings.HasSuffix(path, "/StreamLogs")
	readOnly := r.Method == "GET"
	method := path[strings.LastIndex(path, "/")+1:]
	for _, prefix := range []string{"Get", "List", "Search", "Export", "Test"} {
		if strings.HasPrefix(method, prefix) {
			readOnly = true
		}
	}
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
			defer i.op.RUnlock()
		} else {
			defer i.op.Unlock()
		}
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
	if (ws || stream) && rt.Scope.Context().Err() != nil {
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
	var before map[string]string
	if !ws && !stream {
		before = i.config.Snapshot()
	}
	rt.Handler.ServeHTTP(w, req)
	if !ws && !stream {
		after := i.config.Snapshot()
		pending := false
		for k := range instanceconfig.InstanceRestartKeys {
			if before[k] != after[k] {
				pending = true
			}
		}
		if pending && rt.Scope.Context().Err() == nil {
			_ = m.update(id, func(info *Info) { info.RestartRequired = true })
		}
	}
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
func (m *Manager) api(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/instances"), "/")
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
	parts := strings.Split(path, "/")
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
