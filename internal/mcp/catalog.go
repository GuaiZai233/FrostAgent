package mcp

import (
	"sort"
	"sync"
	"time"

	officialmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type CatalogItem struct {
	RemoteName  string         `json:"remote_name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Enabled     bool           `json:"enabled"`
}

type ToolCatalog struct {
	mu           sync.RWMutex
	items        map[string]CatalogItem
	lastSyncedAt time.Time
}

func NewToolCatalog() *ToolCatalog {
	return &ToolCatalog{
		items: make(map[string]CatalogItem),
	}
}

// UpdateRemote updates the remote catalog from tools/list and applies local policies.
// Default policy is enabled=true if not explicitly configured.
func (c *ToolCatalog) UpdateRemote(tools []*officialmcp.Tool, policies map[string]ToolPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newItems := make(map[string]CatalogItem, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		enabled := true
		if p, ok := policies[t.Name]; ok {
			enabled = p.Enabled
		}

		params := NormalizeSchema(t.InputSchema)

		newItems[t.Name] = CatalogItem{
			RemoteName:  t.Name,
			Description: t.Description,
			Parameters:  params,
			Enabled:     enabled,
		}
	}

	c.items = newItems
	c.lastSyncedAt = time.Now()
}

// SetPolicy updates the enabled state for a specific tool in the catalog.
func (c *ToolCatalog) SetPolicy(remoteName string, enabled bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.items[remoteName]
	if !exists {
		return false
	}
	item.Enabled = enabled
	c.items[remoteName] = item
	return true
}

func (c *ToolCatalog) Get(remoteName string) (CatalogItem, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[remoteName]
	return item, ok
}

func (c *ToolCatalog) List() []CatalogItem {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]CatalogItem, 0, len(c.items))
	for _, item := range c.items {
		res = append(res, item)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].RemoteName < res[j].RemoteName
	})
	return res
}

func (c *ToolCatalog) EffectiveItems() []CatalogItem {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res := make([]CatalogItem, 0, len(c.items))
	for _, item := range c.items {
		if item.Enabled {
			res = append(res, item)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].RemoteName < res[j].RemoteName
	})
	return res
}

func (c *ToolCatalog) LastSyncedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSyncedAt
}
