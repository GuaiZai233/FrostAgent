package instanceconfig

import (
	"errors"
	"fmt"
	"github.com/joho/godotenv"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// GlobalKeys are owned by the control plane, including the temporary shared persona.
var GlobalKeys = map[string]bool{
	"LISTEN_ADDR": true, "WS_LISTEN_ADDR": true,
	"WS_ALLOWED_ORIGINS": true, "HTTP_ALLOWED_ORIGINS": true,
	"ALCYONE_BASE_URL": true, "ALCYONE_SERVICE_TOKEN": true, "ALCYONE_TIMEOUT": true,
	"SANDBOX_ENABLED": true, "SANDBOX_BASE_URL": true, "SANDBOX_AUTH_TOKEN": true, "SANDBOX_SESSION_NAMESPACE": true,
	"MCP_CONTROL_TOKEN": true, "ADMIN_TOKEN": true, "ALLOW_REMOTE_MCP_MANAGEMENT": true, "MCP_ENFORCE_LOCAL_TOKEN": true,
	"SYSTEM_PROMPT": true,
}
var InstanceRestartKeys = map[string]bool{"ENABLE_ONEBOT_ADAPTER": true, "ENABLE_ASTRBOT_ADAPTER": true, "MEMORY_REFLECTION_TIMEOUT": true, "GROUP_COMPACT_BUFFER_SIZE": true, "GROUP_COMPACT_MAX_BUFFER_SIZE": true, "GROUP_COMPACT_MIN_INTERVAL": true, "BILLING_ENABLED": true, "BILLING_MAX_OUTPUT_TOKENS": true, "BILLING_SAFETY_MULTIPLIER": true, "BILLING_PROMPT_PRICE_PER_MILLION": true, "BILLING_COMPLETION_PRICE_PER_MILLION": true}
var ControlPlaneRestartKeys = map[string]bool{
	"LISTEN_ADDR": true, "WS_LISTEN_ADDR": true, "HTTP_ALLOWED_ORIGINS": true,
	"ALCYONE_BASE_URL": true, "ALCYONE_SERVICE_TOKEN": true, "ALCYONE_TIMEOUT": true,
	"SANDBOX_ENABLED": true, "SANDBOX_BASE_URL": true, "SANDBOX_AUTH_TOKEN": true, "SANDBOX_SESSION_NAMESPACE": true,
	"MCP_CONTROL_TOKEN": true, "ADMIN_TOKEN": true, "ALLOW_REMOTE_MCP_MANAGEMENT": true, "MCP_ENFORCE_LOCAL_TOKEN": true,
}
var keyPattern = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")

type Store struct {
	mu        sync.RWMutex
	path      string
	values    map[string]string
	global    bool
	raw       string
	loadErr   error
	accessErr error
}

func Open(path string, global bool) (*Store, error) {
	s := &Store{path: path, global: global, values: map[string]string{}}
	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			err = fmt.Errorf("配置路径不是普通文件")
		} else {
			err = os.Chmod(path, 0600)
		}
	}
	if err != nil && !os.IsNotExist(err) {
		s.accessErr = err
		s.loadErr = err
		return s, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		raw = nil
		err = nil
	}
	if err != nil {
		s.accessErr = err
		s.loadErr = err
		return s, err
	}
	s.raw = string(raw)
	values, err := godotenv.Unmarshal(string(raw))
	if err != nil {
		s.loadErr = err
		return s, err
	}
	for k := range values {
		if !allowed(k, global) {
			delete(values, k)
		}
	}
	s.values = values
	return s, nil
}
func (s *Store) AccessError() error { s.mu.RLock(); defer s.mu.RUnlock(); return s.accessErr }
func (s *Store) Error() error       { s.mu.RLock(); defer s.mu.RUnlock(); return s.loadErr }

func allowed(k string, global bool) bool {
	if !keyPattern.MatchString(k) {
		return false
	}
	if global {
		return GlobalKeys[k]
	}
	return !GlobalKeys[k] && k != "BRAIN_PATH" && k != "DIALOGUE_PATH" && k != "ONEBOT_WS_PATH" && k != "ASTRBOT_WS_PATH"
}
func (s *Store) Get(k string) string { s.mu.RLock(); defer s.mu.RUnlock(); return s.values[k] }
func (s *Store) Snapshot() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := map[string]string{}
	for k, x := range s.values {
		v[k] = x
	}
	return v
}
func (s *Store) Raw() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.raw, s.accessErr
}
func (s *Store) Update(k, v string, remove bool) error {
	if !allowed(k, s.global) {
		return fmt.Errorf("字段 %s 不属于此配置", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return fmt.Errorf("配置文件不可解析，请通过原始 .env 编辑修复: %w", s.loadErr)
	}
	if strings.ContainsRune(v, 0) || (k != "SYSTEM_PROMPT" && strings.ContainsAny(v, "\r\n")) {
		return fmt.Errorf("字段 %s 不允许 NUL 或换行", k)
	}
	raw, err := MutateEnv(s.raw, k, v, remove)
	if err != nil {
		return err
	}
	next, err := godotenv.Unmarshal(raw)
	if err != nil {
		return err
	}
	value, exists := next[k]
	if (remove && exists) || (!remove && (!exists || value != v)) {
		return fmt.Errorf("字段 %s 无法安全写入配置", k)
	}
	if err = WriteAtomic(s.path, []byte(raw), 0600); err != nil {
		return err
	}
	for key := range next {
		if !allowed(key, s.global) {
			delete(next, key)
		}
	}
	s.values = next
	s.raw = raw
	return nil
}

func (s *Store) Replace(raw string) error {
	next, err := godotenv.Unmarshal(raw)
	if err != nil {
		return err
	}
	for k := range next {
		if !allowed(k, s.global) {
			return fmt.Errorf("字段 %s 不属于此配置", k)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accessErr != nil {
		return s.accessErr
	}
	if err := WriteAtomic(s.path, []byte(raw), 0600); err != nil {
		return err
	}
	s.values = next
	s.raw = raw
	s.loadErr = nil
	return nil
}
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

var syncDirectory = func(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// WriteAtomicDurable reports whether rename committed the new file before a
// possible directory-sync error. Callers must not roll back committed state as
// though the rename never happened.
func WriteAtomicDurable(path string, data []byte, mode os.FileMode) (committed bool, err error) {
	if err = WriteAtomic(path, data, mode); err != nil {
		return false, err
	}
	return true, syncDirectory(filepath.Dir(path))
}

// SyncDirectory makes a newly created child entry durable where supported.
func SyncDirectory(path string) error { return syncDirectory(path) }

const Template = `# Instance settings. Restart-required fields take effect after disabling/enabling.
UPSTREAM_API_KEY=
BOT_NAME=霜降狐
BOT_ALIASES=霜降,FrostAgent
ADMIN_QQ_IDS=
MAX_CONTEXT_MESSAGES=20
MAX_CONTEXT_CHARS=24000
MEMORY_REFLECTION_TIMEOUT=10m
ENABLE_AT_IN_GROUP_MSG=true
GROUP_REPLY_ON_MENTION=true
ENABLE_REPLY_IN_GROUP_MSG=false
GROUP_COMPACT_BUFFER_SIZE=20
GROUP_COMPACT_MIN_INTERVAL=30s
GROUP_RAW_CONTEXT_MAX_CHARS=12000
MEMORY_EXTRACT_BATCH_MIN=3
MEMORY_EXTRACT_BATCH_MAX=5
ENABLE_ONEBOT_ADAPTER=true
ENABLE_ASTRBOT_ADAPTER=true
BILLING_ENABLED=false
BILLING_MAX_OUTPUT_TOKENS=2048
BILLING_SAFETY_MULTIPLIER=1.2
`
