package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type TransportType string

const (
	TransportStdio          TransportType = "stdio"
	TransportStreamableHTTP TransportType = "streamable_http"
	TransportSSE            TransportType = "sse"
)

type TransportConfig struct {
	Type       TransportType     `json:"type"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

type ToolPolicy struct {
	Enabled bool `json:"enabled"`
}

type ServerConfig struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Enabled   bool                  `json:"enabled"`
	Transport TransportConfig       `json:"transport"`
	Tools     map[string]ToolPolicy `json:"tools,omitempty"` // local overrides (default enabled if absent)
}

type Config struct {
	Servers []ServerConfig `json:"servers"`
}

// ConfigStore handles thread-safe and crash-safe persistence for MCP configuration.
type ConfigStore struct {
	path string
	mu   sync.RWMutex
}

func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

func (s *ConfigStore) Load() (*Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Servers: []ServerConfig{}}, nil
		}
		return nil, fmt.Errorf("failed to read mcp config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse mcp config: %w", err)
	}
	if cfg.Servers == nil {
		cfg.Servers = []ServerConfig{}
	}
	for i := range cfg.Servers {
		if cfg.Servers[i].Tools == nil {
			cfg.Servers[i].Tools = make(map[string]ToolPolicy)
		}
	}
	return &cfg, nil
}

func (s *ConfigStore) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg == nil {
		cfg = &Config{Servers: []ServerConfig{}}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mcp config: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	// Crash-safe atomic write using temp file and rename
	tmpFile, err := os.CreateTemp(dir, "mcp_servers_*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to write temp config file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to sync temp config file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to close temp config file: %w", err)
	}

	// Crash-safe atomic replace
	if err := atomicReplaceFile(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("failed to atomically replace config file: %w", err)
	}
	return nil
}
