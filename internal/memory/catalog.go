package memory

import (
	"FrostAgent/internal/storage"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

const currentCatalogVersion = 1

// MemoryTopic is a compact lookup hint. It is not a memory fact and must only
// be used by the model to decide whether to call the memory search tool.
type MemoryTopic struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

// UserMemoryCatalog contains the derived topic index for one owner.
type UserMemoryCatalog struct {
	Owner       string        `json:"owner"`
	Topics      []MemoryTopic `json:"topics"`
	MemoryCount int           `json:"memory_count"`
	GeneratedAt time.Time     `json:"generated_at"`
}

type catalogFile struct {
	Version   int                          `json:"version"`
	UpdatedAt time.Time                    `json:"updated_at"`
	Users     map[string]UserMemoryCatalog `json:"users"`
}

// CatalogStore persists replaceable reflection output outside brain.json.
type CatalogStore struct {
	path string
	sql  *sqlBrainStore
	mu   sync.RWMutex
}

// NewCatalogStore creates a topic catalog backed by an independent JSON file.
func NewCatalogStore(path string) *CatalogStore {
	return &CatalogStore{path: path}
}

func NewSQLCatalogStore(db *storage.DB, instanceID string, scope ScopeType, platform, groupID string) *CatalogStore {
	return &CatalogStore{sql: &sqlBrainStore{db: db, instanceID: instanceID, scope: scope, platform: platform, groupID: groupID}}
}

// Get returns the topic catalog for one owner, resolving legacy and aliased QQ owners.
func (s *CatalogStore) Get(owner string) (*UserMemoryCatalog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := s.load()
	if err != nil {
		return nil, err
	}
	canonical := CanonicalOwner(owner)
	if catalog, ok := file.Users[canonical]; ok {
		return &catalog, nil
	}
	for _, alias := range OwnerAliases(owner) {
		if catalog, ok := file.Users[alias]; ok {
			return &catalog, nil
		}
	}
	return nil, nil
}

// Replace overwrites one owner's derived topic catalog under its canonical owner,
// clearing any legacy alias buckets for the same logical owner.
func (s *CatalogStore) Replace(catalog UserMemoryCatalog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.load()
	if err != nil {
		return err
	}
	if file.Users == nil {
		file.Users = make(map[string]UserMemoryCatalog)
	}
	canonical := CanonicalOwner(catalog.Owner)
	catalog.Owner = canonical
	for _, alias := range OwnerAliases(canonical) {
		delete(file.Users, alias)
	}
	file.Users[canonical] = catalog
	file.Version = currentCatalogVersion
	file.UpdatedAt = time.Now()
	return s.save(file)
}

// Delete removes one owner's catalog and all its aliases when no source memories remain.
func (s *CatalogStore) Delete(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.load()
	if err != nil {
		return err
	}
	canonical := CanonicalOwner(owner)
	deleted := false
	for _, alias := range OwnerAliases(canonical) {
		if _, ok := file.Users[alias]; ok {
			delete(file.Users, alias)
			deleted = true
		}
	}
	if !deleted {
		return nil
	}
	file.UpdatedAt = time.Now()
	return s.save(file)
}

// FormatForPrompt returns a bounded index prompt for the current user.
func (s *CatalogStore) FormatForPrompt(owner string) (string, error) {
	catalog, err := s.Get(owner)
	if err != nil || catalog == nil || len(catalog.Topics) == 0 {
		return "", err
	}

	topics := append([]MemoryTopic(nil), catalog.Topics...)
	if len(topics) > 24 {
		topics = topics[:24]
	}

	labels := make([]string, 0, len(topics))
	for _, topic := range topics {
		name := sanitizeTopicText(topic.Name)
		if name == "" {
			continue
		}
		aliases := make([]string, 0, len(topic.Aliases))
		for _, alias := range topic.Aliases {
			alias = sanitizeTopicText(alias)
			if alias != "" && !strings.EqualFold(alias, name) {
				aliases = append(aliases, alias)
			}
			if len(aliases) == 4 {
				break
			}
		}
		if len(aliases) > 0 {
			name += "(" + strings.Join(aliases, ", ") + ")"
		}
		labels = append(labels, name)
	}
	if len(labels) == 0 {
		return "", nil
	}

	return "## 记忆主题索引\n" +
		"当前用户已有以下记忆主题：" + strings.Join(labels, ", ") + "\n\n" +
		"这些只是索引，不代表具体事实。\n" +
		"当问题可能涉及这些主题时，调用 memory 搜索工具获取原始记忆。", nil
}

// FormatForGroupPrompt returns a bounded index prompt for the isolated group.
func (s *CatalogStore) FormatForGroupPrompt(groupID string) (string, error) {
	if s == nil {
		return "", nil
	}
	catalog, err := s.Get(groupID)
	if err != nil || catalog == nil || len(catalog.Topics) == 0 {
		return "", err
	}

	topics := append([]MemoryTopic(nil), catalog.Topics...)
	if len(topics) > 24 {
		topics = topics[:24]
	}

	labels := make([]string, 0, len(topics))
	for _, topic := range topics {
		name := sanitizeTopicText(topic.Name)
		if name == "" {
			continue
		}
		aliases := make([]string, 0, len(topic.Aliases))
		for _, alias := range topic.Aliases {
			alias = sanitizeTopicText(alias)
			if alias != "" && !strings.EqualFold(alias, name) {
				aliases = append(aliases, alias)
			}
			if len(aliases) == 4 {
				break
			}
		}
		if len(aliases) > 0 {
			name += "(" + strings.Join(aliases, ", ") + ")"
		}
		labels = append(labels, name)
	}
	if len(labels) == 0 {
		return "", nil
	}

	return "## 群聊记忆主题索引\n" +
		"当前群聊已有以下记忆主题：" + strings.Join(labels, ", ") + "\n\n" +
		"这些只是索引，不代表具体事实。\n" +
		"当群聊问题可能涉及这些主题时，调用 memory 搜索工具获取原始群记忆。", nil
}

func (s *CatalogStore) load() (*catalogFile, error) {
	if s.sql != nil {
		file := &catalogFile{Version: currentCatalogVersion, Users: make(map[string]UserMemoryCatalog)}
		b := s.sql
		rows, err := b.db.SQL.QueryContext(context.Background(), b.db.Bind(`SELECT owner, catalog_json FROM memory_catalogs
			WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ?`),
			b.instanceID, b.scope, b.platform, b.groupID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var owner, raw string
			var catalog UserMemoryCatalog
			if err := rows.Scan(&owner, &raw); err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
				return nil, err
			}
			file.Users[owner] = catalog
		}
		return file, rows.Err()
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &catalogFile{
				Version: currentCatalogVersion,
				Users:   make(map[string]UserMemoryCatalog),
			}, nil
		}
		return nil, fmt.Errorf("read memory catalog: %w", err)
	}

	var file catalogFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse memory catalog: %w", err)
	}
	if file.Users == nil {
		file.Users = make(map[string]UserMemoryCatalog)
	} else {
		canonicalUsers := make(map[string]UserMemoryCatalog, len(file.Users))
		for k, cat := range file.Users {
			canonical := CanonicalOwner(k)
			cat.Owner = canonical
			existing, exists := canonicalUsers[canonical]
			if !exists || cat.GeneratedAt.After(existing.GeneratedAt) {
				canonicalUsers[canonical] = cat
			}
		}
		file.Users = canonicalUsers
	}
	return &file, nil
}

func (s *CatalogStore) save(file *catalogFile) error {
	if s.sql != nil {
		b := s.sql
		ctx := context.Background()
		tx, err := b.db.SQL.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, b.db.Bind(`DELETE FROM memory_catalogs
			WHERE instance_id = ? AND scope_type = ? AND platform = ? AND group_id = ?`),
			b.instanceID, b.scope, b.platform, b.groupID); err != nil {
			return err
		}
		for owner, catalog := range file.Users {
			raw, err := json.Marshal(catalog)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, b.db.Bind(`INSERT INTO memory_catalogs
				(instance_id, scope_type, platform, group_id, owner, catalog_json)
				VALUES (?, ?, ?, ?, ?, ?)`), b.instanceID, b.scope, b.platform, b.groupID, owner, string(raw)); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory catalog: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write memory catalog: %w", err)
	}
	return nil
}

func sanitizeTopicText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '(' || r == ')' || r == ',' {
			return -1
		}
		return r
	}, value)
	runes := []rune(value)
	if len(runes) > 48 {
		value = string(runes[:48])
	}
	return strings.TrimSpace(value)
}
