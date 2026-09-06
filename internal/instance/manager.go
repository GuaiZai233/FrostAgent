package instance

import (
	pbconnect "FrostAgent/gen/proto/frostagent/v1/frostagentv1connect"
	"FrostAgent/internal/billing"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/service/dialogue"
	logsvc "FrostAgent/internal/service/logs"
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
	registry       registry
	instances      map[string]*managed
	shared         *dialogue.Service
	billing        *billing.Client
	endpointMu     sync.Mutex
	endpointOwners map[string]string
	general        http.Handler
}

func New(root string, global *instanceconfig.Store, dialoguePath string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	m := &Manager{root: abs, global: global, instances: map[string]*managed{}, endpointOwners: map[string]string{}, registry: registry{Version: 1, NextNumber: 1, Instances: []Info{}}, shared: dialogue.New(dialoguePath, nil)}
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
		owner := strings.TrimPrefix(filepath.Base(dir), "instance_")
		for _, e := range c.Endpoints {
			if old, ok := m.endpointOwners[e.ID]; ok && old != owner {
				return nil, fmt.Errorf("Endpoint ID 冲突: %s", e.ID)
			}
			m.endpointOwners[e.ID] = owner
		}
	}
	for index, info := range m.registry.Instances {
		if !idPattern.MatchString(info.ID) || m.instances[info.ID] != nil {
			return nil, fmt.Errorf("无效或重复的实例 ID")
		}
		for j := 0; j < index; j++ {
			if strings.EqualFold(m.registry.Instances[j].Name, info.Name) {
				return nil, fmt.Errorf("重复的实例名称")
			}
		}
		pathError := safeTree(m.dir(info.ID))
		c, configError := instanceconfig.Open(filepath.Join(m.dir(info.ID), ".env"), false)
		i := &managed{config: c, logger: logs.New(info.ID, info.Name, 5000)}
		m.instances[info.ID] = i
		if pathError != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = pathError.Error()
			continue
		}
		if configError != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = configError.Error()
		}
		if info.Deleting {
			continue
		}
		r, err := m.build(info.ID, i, false)
		i.runtime = r
		if err != nil {
			m.registry.Instances[index].Enabled = false
			m.registry.Instances[index].Error = err.Error()
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
func (m *Manager) build(id string, i *managed, enabled bool) (*Runtime, error) {
	r, err := buildRuntime(m.dir(id), "/instances/"+id, i.config, m.global, i.logger, m.shared, m.billing, enabled)
	if err == nil {
		r.Engine.ModelRouter.ReserveEndpoints = func(endpoints []modelrouter.Endpoint) error { return m.reserveEndpoints(id, endpoints) }
	}
	return r, err
}
func (m *Manager) reserveEndpoints(owner string, endpoints []modelrouter.Endpoint) error {
	if err := modelrouter.PersistentRefs(endpoints); err != nil {
		return err
	}
	m.endpointMu.Lock()
	defer m.endpointMu.Unlock()
	next := map[string]string{}
	for id, v := range m.endpointOwners {
		next[id] = v
	}
	for _, e := range endpoints {
		if existing, ok := next[e.ID]; ok {
			if existing != owner {
				return fmt.Errorf("Endpoint ID 已被占用: %s", e.ID)
			}
		} else {
			exists, err := modelrouter.CredentialExists(e.ID)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("Endpoint 凭据已存在: %s", e.ID)
			}
			next[e.ID] = owner
		}
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
	if err = instanceconfig.WriteAtomic(filepath.Join(m.dir(id), ".env"), []byte(instanceconfig.Template), 0600); err != nil {
		return Info{}, err
	}
	c, err := instanceconfig.Open(filepath.Join(m.dir(id), ".env"), false)
	if err != nil {
		return Info{}, err
	}
	i := &managed{config: c, logger: logs.New(id, name, 5000)}
	r, err := m.build(id, i, false)
	if err != nil {
		return Info{}, err
	}
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
	logs.General.Info(logs.SYSTEM, "创建实例: "+name)
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
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
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
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if _, err = m.lookup(id); err != nil {
		return err
	}
	list, _ := m.List()
	for _, info := range list {
		if info.ID == id {
			if info.Deleting {
				return fmt.Errorf("实例正在删除，请重试删除操作")
			}
			if info.Enabled == enabled && i.runtime != nil && (i.runtime.Scope.Context().Err() == nil) == enabled {
				return nil
			}
		}
	}
	if err = m.stop(id, i); err != nil {
		return err
	}
	r, err := m.build(id, i, enabled)
	if err == nil {
		i.mu.Lock()
		i.runtime = r
		i.mu.Unlock()
	}
	if err != nil {
		_ = m.update(id, func(info *Info) { info.Enabled = false; info.Error = err.Error() })
		return err
	}
	if err = m.update(id, func(info *Info) { info.Enabled = enabled; info.Error = ""; info.RestartRequired = false }); err != nil {
		r.Stop()
		return err
	}
	logs.General.Info(logs.SYSTEM, fmt.Sprintf("实例 %s enabled=%t", id, enabled))
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
	if !i.op.TryLock() {
		return ErrBusy
	}
	defer i.op.Unlock()
	if _, err = m.lookup(id); err != nil {
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

type fileBackup struct {
	path   string
	data   []byte
	exists bool
}

func backupFiles(dir string) ([]fileBackup, error) {
	result := []fileBackup{}
	for _, name := range []string{".env", "model_router.json", "model_router_secrets.json"} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		result = append(result, fileBackup{path, data, err == nil})
	}
	return result, nil
}
func restoreFiles(files []fileBackup) error {
	var errs error
	for _, f := range files {
		var err error
		if f.exists {
			err = instanceconfig.WriteAtomic(f.path, f.data, 0600)
		} else {
			err = os.Remove(f.path)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		errs = errors.Join(errs, err)
	}
	return errs
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
	list, _ := m.List()
	for _, info := range list {
		if info.ID == target && info.Enabled {
			return fmt.Errorf("被覆盖的实例必须处于非活跃状态")
		}
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
	cfg, secrets, newCreds, err := src.runtime.Engine.ModelRouter.CopyPublished(func(e []modelrouter.Endpoint) error { return m.reserveEndpoints(target, e) })
	if err != nil {
		return err
	}
	oldCreds, err := dst.runtime.Engine.ModelRouter.DeleteCredentials()
	if err != nil {
		return err
	}
	backup, err := backupFiles(m.dir(target))
	if err != nil {
		return err
	}
	undo, err := modelrouter.ApplyCredentials(append(newCreds, oldCreds...))
	rollback := func(cause error) error { return errors.Join(cause, undo(), restoreFiles(backup)) }
	if err != nil {
		return rollback(err)
	}
	for name, data := range map[string][]byte{".env": []byte(raw), "model_router.json": cfg, "model_router_secrets.json": secrets} {
		if err = instanceconfig.WriteAtomic(filepath.Join(m.dir(target), name), data, 0600); err != nil {
			return rollback(err)
		}
	}
	c, err := instanceconfig.Open(filepath.Join(m.dir(target), ".env"), false)
	if err != nil {
		return rollback(err)
	}
	previousConfig := dst.config
	dst.config = c
	r, err := m.build(target, dst, false)
	if err != nil {
		dst.config = previousConfig
		return rollback(err)
	}
	if err = m.update(target, func(info *Info) { info.Error = ""; info.RestartRequired = false }); err != nil {
		dst.config = previousConfig
		return rollback(err)
	}
	dst.mu.Lock()
	dst.runtime = r
	dst.mu.Unlock()
	return nil
}
func (m *Manager) Close() {
	m.mu.RLock()
	items := []*managed{}
	for _, i := range m.instances {
		items = append(items, i)
	}
	m.mu.RUnlock()
	for _, i := range items {
		i.op.Lock()
		i.mu.Lock()
		if i.runtime != nil {
			i.runtime.Stop()
		}
		i.mu.Unlock()
		i.op.Unlock()
	}
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	}
	i.mu.RLock()
	rt := i.runtime
	i.mu.RUnlock()
	if rt == nil {
		http.Error(w, "实例配置不可用", 503)
		return
	}
	if ws && rt.Scope.Context().Err() != nil {
		http.Error(w, "实例未启用", 503)
		return
	}
	req := r.Clone(r.Context())
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
		for k := range instanceconfig.RestartKeys {
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
