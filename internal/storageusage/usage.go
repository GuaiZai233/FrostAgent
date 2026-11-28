// Package storageusage estimates how much persistent storage one instance uses,
// grouped into the categories shown on the console overview.
package storageusage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

// Category identifiers are stable API values consumed by the console.
const (
	CategoryChat    = "chat"
	CategoryImages  = "images"
	CategorySticker = "stickers"
	CategoryMemory  = "memory"
	CategoryOther   = "other"
)

// Order is the display order of categories.
var Order = []string{CategoryChat, CategoryImages, CategorySticker, CategoryMemory, CategoryOther}

const cacheTTL = 10 * time.Second

// Item is the estimated size of one category.
type Item struct {
	Category string
	Bytes    int64
}

// Snapshot lists every category (zero-sized ones included) in display order.
type Snapshot struct {
	Items []Item
	Total int64
}

// table describes a per-instance table and the variable-size columns that
// dominate its footprint. Fixed-width columns are ignored, so sizes are a
// lower-bound estimate of the logical payload, not of on-disk pages.
type table struct {
	name     string
	category string
	columns  []string
	// vector names a blob or vector column that needs a backend-specific size
	// function.
	vector string
	// optional tables may not exist in every schema version.
	optional bool
}

var tables = []table{
	{name: "chat_history", category: CategoryChat, columns: []string{"id", "platform", "message_type", "target_id", "message_id", "sender_id", "sender_name", "role", "content", "search_text", "reply_to", "delivery_status"}},
	{name: "session_states", category: CategoryChat, columns: []string{"session_id", "payload"}},
	{name: "session_images", category: CategoryImages, columns: []string{"key", "content_type"}, vector: "data", optional: true},
	{name: "sticker_entries", category: CategorySticker, columns: []string{"id", "file_name", "description", "status"}},
	{name: "sticker_keywords", category: CategorySticker, columns: []string{"sticker_id", "keyword"}},
	{name: "memory_entries", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "id", "owner", "owner_type", "content", "summary", "evidence", "source_message_id", "source_sender_id", "source", "visibility", "created_at", "updated_at", "merged_from_json", "source_media"}},
	{name: "memory_tags", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "memory_id", "tag"}},
	{name: "memory_merge_archives", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "merged_id", "owner", "merged_at", "sources_json"}},
	{name: "memory_catalogs", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "owner", "catalog_json"}},
	{name: "memory_embedding_scopes", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "owner", "fingerprint", "source_hash"}, optional: true},
	{name: "memory_embeddings", category: CategoryMemory, columns: []string{"scope_type", "platform", "group_id", "owner", "memory_id", "content_hash"}, vector: "embedding", optional: true},
	{name: "group_profiles", category: CategoryMemory, columns: []string{"platform", "group_id", "group_name", "updated_at"}},
	{name: "group_members", category: CategoryMemory, columns: []string{"platform", "group_id", "user_id", "nickname", "card", "role", "preferred_name", "aliases_json", "source", "created_at", "updated_at", "last_spoke_at"}},
	{name: "group_summaries", category: CategoryMemory, columns: []string{"platform", "group_id", "summary", "created_at", "updated_at"}},
	{name: "tool_history", category: CategoryOther, columns: []string{"ref_id", "platform", "message_type", "target_id", "source_message_id", "record_json"}, optional: true},
	{name: "dialogues", category: CategoryOther, columns: []string{"id", "scene", "relation", "user_text", "preferred"}},
}

// Collector measures one instance. Results are cached briefly so a polling
// overview page does not rescan tables and directories on every refresh.
type Collector struct {
	db         *storage.DB
	instanceID string
	// stickerDir holds sticker image files; logDir holds per-entry log files.
	stickerDir string
	logDir     string

	mu       sync.Mutex
	cached   Snapshot
	cachedAt time.Time
	now      func() time.Time
}

// New returns a collector. db may be nil for legacy file-only runtimes, in
// which case Snapshot reports an error and the console shows "unavailable".
func New(db *storage.DB, instanceID, stickerDir, logDir string) *Collector {
	return &Collector{db: db, instanceID: instanceID, stickerDir: stickerDir, logDir: logDir, now: time.Now}
}

// Snapshot returns the cached measurement, refreshing it when stale.
func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	if c == nil || c.db == nil {
		return Snapshot{}, errors.New("storage usage requires the SQL storage backend")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cachedAt.IsZero() && c.now().Sub(c.cachedAt) < cacheTTL {
		return c.cached, nil
	}
	snapshot, err := c.measure(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	c.cached, c.cachedAt = snapshot, c.now()
	return snapshot, nil
}

func (c *Collector) measure(ctx context.Context) (Snapshot, error) {
	totals := map[string]int64{}
	for _, t := range tables {
		size, err := c.tableBytes(ctx, t)
		if err != nil {
			return Snapshot{}, fmt.Errorf("measure %s: %w", t.name, err)
		}
		totals[t.category] += size
	}
	totals[CategorySticker] += dirBytes(c.stickerDir)
	totals[CategoryOther] += dirBytes(c.logDir)

	snapshot := Snapshot{}
	for _, category := range Order {
		snapshot.Items = append(snapshot.Items, Item{Category: category, Bytes: totals[category]})
		snapshot.Total += totals[category]
	}
	return snapshot, nil
}

func (c *Collector) bytesExpr(column string) string {
	if c.db.Backend == storage.Postgres {
		return "COALESCE(octet_length(" + column + "), 0)"
	}
	return "COALESCE(length(CAST(" + column + " AS BLOB)), 0)"
}

func (c *Collector) vectorExpr(column string) string {
	if c.db.Backend == storage.Postgres {
		return "COALESCE(pg_column_size(" + column + "), 0)"
	}
	return c.bytesExpr(column)
}

func (c *Collector) tableBytes(ctx context.Context, t table) (int64, error) {
	parts := make([]string, 0, len(t.columns)+1)
	for _, column := range t.columns {
		parts = append(parts, c.bytesExpr(column))
	}
	if t.vector != "" {
		parts = append(parts, c.vectorExpr(t.vector))
	}
	query := "SELECT COALESCE(SUM(" + strings.Join(parts, " + ") + "), 0) FROM " + t.name + " WHERE instance_id = ?"
	var size int64
	err := c.db.SQL.QueryRowContext(ctx, c.db.Bind(query), c.instanceID).Scan(&size)
	if err != nil && t.optional && isMissingTable(err) {
		return 0, nil
	}
	return size, err
}

func isMissingTable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") || strings.Contains(message, "does not exist")
}

// dirBytes totals regular files below dir. A missing directory or unreadable
// entry counts as zero so a partial scan never fails the overview.
func dirBytes(dir string) int64 {
	if dir == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
