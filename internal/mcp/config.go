package mcp

import (
	"FrostAgent/internal/storage"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	path       string
	db         *storage.DB
	instanceID string
	mu         sync.RWMutex
}

func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

func NewSQLConfigStore(db *storage.DB, instanceID string) *ConfigStore {
	return &ConfigStore{db: db, instanceID: instanceID}
}

func (s *ConfigStore) Load() (*Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db != nil {
		return s.loadSQL()
	}

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
	if s.db != nil {
		return s.saveSQL(cfg)
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

func (s *ConfigStore) loadSQL() (*Config, error) {
	ctx := context.Background()
	rows, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT id, name, enabled, transport_type,
		command, working_dir, url, args_json, env_json, headers_json FROM mcp_servers
		WHERE instance_id = ? ORDER BY position, id`), s.instanceID)
	if err != nil {
		return nil, err
	}
	cfg := &Config{Servers: []ServerConfig{}}
	for rows.Next() {
		var server ServerConfig
		var enabled int
		var transport string
		var args, env, headers string
		if err := rows.Scan(&server.ID, &server.Name, &enabled, &transport, &server.Transport.Command,
			&server.Transport.WorkingDir, &server.Transport.URL, &args, &env, &headers); err != nil {
			rows.Close()
			return nil, err
		}
		server.Enabled = enabled != 0
		server.Transport.Type = TransportType(transport)
		server.Tools = make(map[string]ToolPolicy)
		for _, pair := range []struct {
			raw    string
			target any
		}{
			{args, &server.Transport.Args}, {env, &server.Transport.Env}, {headers, &server.Transport.Headers},
		} {
			if err := json.Unmarshal([]byte(pair.raw), pair.target); err != nil {
				rows.Close()
				return nil, err
			}
		}
		cfg.Servers = append(cfg.Servers, server)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	byID := make(map[string]*ServerConfig, len(cfg.Servers))
	for i := range cfg.Servers {
		byID[cfg.Servers[i].ID] = &cfg.Servers[i]
	}
	policies, err := s.db.SQL.QueryContext(ctx, s.db.Bind(`SELECT server_id, tool_name, enabled
		FROM mcp_tool_policies WHERE instance_id = ?`), s.instanceID)
	if err != nil {
		return nil, err
	}
	defer policies.Close()
	for policies.Next() {
		var serverID, name string
		var enabled int
		if err := policies.Scan(&serverID, &name, &enabled); err != nil {
			return nil, err
		}
		if server := byID[serverID]; server != nil {
			server.Tools[name] = ToolPolicy{Enabled: enabled != 0}
		}
	}
	return cfg, policies.Err()
}

func (s *ConfigStore) saveSQL(cfg *Config) error {
	ctx := context.Background()
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeConfigTx(ctx, s.db, tx, s.instanceID, cfg); err != nil {
		return err
	}
	return tx.Commit()
}

// WriteImportedConfigTx saves a redacted MCP configuration as one part of a
// caller-owned settings transaction.
func WriteImportedConfigTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg *Config) error {
	seen := make(map[string]bool)
	for i := range cfg.Servers {
		server := &cfg.Servers[i]
		if strings.TrimSpace(server.ID) == "" || strings.TrimSpace(server.Name) == "" || strings.Contains(server.ID, "__") {
			return fmt.Errorf("invalid MCP server id or name")
		}
		if seen[server.ID] {
			return fmt.Errorf("duplicate MCP server id %q", server.ID)
		}
		seen[server.ID] = true
		switch server.Transport.Type {
		case TransportStdio:
			if strings.TrimSpace(server.Transport.Command) == "" {
				return fmt.Errorf("MCP server %q has no command", server.ID)
			}
		case TransportStreamableHTTP, TransportSSE:
			if !strings.HasPrefix(server.Transport.URL, "http://") && !strings.HasPrefix(server.Transport.URL, "https://") {
				return fmt.Errorf("MCP server %q has no HTTP URL", server.ID)
			}
		default:
			return fmt.Errorf("MCP server %q has unsupported transport", server.ID)
		}
		server.Transport.Env = nil
		server.Transport.Headers = nil
		server.Transport.Args = nil
	}
	return writeConfigTx(ctx, db, tx, instanceID, cfg)
}

func writeConfigTx(ctx context.Context, db *storage.DB, tx *sql.Tx, instanceID string, cfg *Config) error {
	if _, err := tx.ExecContext(ctx, db.Bind(`DELETE FROM mcp_servers WHERE instance_id = ?`), instanceID); err != nil {
		return err
	}
	for position, server := range cfg.Servers {
		args, err := json.Marshal(server.Transport.Args)
		if err != nil {
			return err
		}
		env, err := json.Marshal(server.Transport.Env)
		if err != nil {
			return err
		}
		headers, err := json.Marshal(server.Transport.Headers)
		if err != nil {
			return err
		}
		enabled := 0
		if server.Enabled {
			enabled = 1
		}
		if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO mcp_servers
			(instance_id, id, name, enabled, transport_type, command, working_dir, url,
			args_json, env_json, headers_json, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`), instanceID, server.ID, server.Name,
			enabled, server.Transport.Type, server.Transport.Command, server.Transport.WorkingDir,
			server.Transport.URL, string(args), string(env), string(headers), position); err != nil {
			return err
		}
		for name, policy := range server.Tools {
			enabled := 0
			if policy.Enabled {
				enabled = 1
			}
			if _, err := tx.ExecContext(ctx, db.Bind(`INSERT INTO mcp_tool_policies
				(instance_id, server_id, tool_name, enabled) VALUES (?, ?, ?, ?)`),
				instanceID, server.ID, name, enabled); err != nil {
				return err
			}
		}
	}
	return nil
}
