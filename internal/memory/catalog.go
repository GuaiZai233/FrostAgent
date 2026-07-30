package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const currentCatalogVersion = 1

// MemoryTopic is a compact lookup hint. It is not a memory fact and must only
// be used by the model to decide whether to call the memory search tool.
type MemoryTopic struct {
	Name       string   `json:"name"`
	Aliases    []string `json:"aliases,omitempty"`
	Importance float64  `json:"importance,omitempty"`
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
	mu   sync.RWMutex
}

// NewCatalogStore creates a topic catalog backed by an independent JSON file.
func NewCatalogStore(path string) *CatalogStore {
	return &CatalogStore{path: path}
}

// Get returns the topic catalog for one owner.
func (s *CatalogStore) Get(owner string) (*UserMemoryCatalog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := s.load()
	if err != nil {
		return nil, err
	}
	catalog, ok := file.Users[owner]
	if !ok {
		return nil, nil
	}
	return &catalog, nil
}

// Replace overwrites one owner's derived topic catalog.
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
	file.Users[catalog.Owner] = catalog
	file.Version = currentCatalogVersion
	file.UpdatedAt = time.Now()
	return s.save(file)
}

// Delete removes one owner's catalog when no source memories remain.
func (s *CatalogStore) Delete(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := file.Users[owner]; !ok {
		return nil
	}
	delete(file.Users, owner)
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
	sort.SliceStable(topics, func(i, j int) bool {
		return topics[i].Importance > topics[j].Importance
	})
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

func (s *CatalogStore) load() (*catalogFile, error) {
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
	}
	return &file, nil
}

func (s *CatalogStore) save(file *catalogFile) error {
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
