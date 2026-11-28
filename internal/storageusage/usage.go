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
	// sizeColumn is a column that already stores a payload size in bytes, so the
	// large payload column itself is never scanned.
	sizeColumn string
	// vector names a blob or vector column that needs a backend-specific size
	// function.
	vector string
	// optional tables may not exist in every schema version.
	optional bool
}

var tables = []table{
	{name: "chat_history", category: CategoryChat, columns: []string{"id", "platform", "message_type", "target_id", "message_id", "sender_id", "sender_name", "role", "content", "search_text", "reply_to", "delivery_status"}},
	{name: "session_states", category: CategoryChat, columns: []string{"session_id", "payload"}},
	// size is the decoded image length written by the image store; data is
	// Base64 text and is deliberately not scanned.
	{name: "session_images", category: CategoryImages, columns: []string{"key", "content_type"}, sizeColumn: "size", optional: true},
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

const (
	// firstWait bounds how long the first request waits for an initial result.
	firstWait = 1500 * time.Millisecond
	// measureTimeout bounds one background measurement.
	measureTimeout = 60 * time.Second
	// failureBackoff is the minimum gap before retrying after a failed measurement.
	failureBackoff = 60 * time.Second
	// queryTimeout caps one table scan. The database has a single shared
	// connection, so a scan that runs longer is abandoned (and the measurement
	// backs off) rather than starving chat and memory queries.
	queryTimeout = 3 * time.Second
	// yieldGap is left between tables so queued business statements can run.
	yieldGap = 25 * time.Millisecond
)

// Collector measures one instance. Measuring runs in one background goroutine
// at a time, never inside the overview request: table scans use the database's
// shared connection, so a polling page must not trigger them directly. Requests
// get the last good result (possibly stale) immediately.
type Collector struct {
	db         *storage.DB
	instanceID string
	// stickerDir holds sticker image files, logDir per-entry log files and
	// checkpointDir the local session snapshots written before the SQL copy.
	stickerDir    string
	logDir        string
	checkpointDir string

	mu           sync.Mutex
	cached       Snapshot
	cachedAt     time.Time
	refreshing   chan struct{}
	retryAfter   time.Time
	lastErr      error
	now          func() time.Time
	measureFunc  func(context.Context) (Snapshot, error)
	queryTimeout time.Duration

	// life bounds every background refresh to the owning runtime.
	life   context.Context
	stop   context.CancelFunc
	wg     sync.WaitGroup
	closed bool
}

// New returns a collector. db may be nil for legacy file-only runtimes, in
// which case Snapshot reports an error and the console shows "unavailable".
func New(db *storage.DB, instanceID, stickerDir, logDir, checkpointDir string) *Collector {
	c := &Collector{db: db, instanceID: instanceID, stickerDir: stickerDir, logDir: logDir, checkpointDir: checkpointDir, now: time.Now, queryTimeout: queryTimeout}
	c.life, c.stop = context.WithCancel(context.Background())
	c.measureFunc = c.measure
	return c
}

// Close cancels an in-flight measurement, waits for it to exit and prevents new
// ones. It is safe to call more than once; Snapshot keeps serving the last result.
func (c *Collector) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.stop != nil {
		c.stop()
	}
	c.wg.Wait()
}

// Snapshot returns the cached measurement and, when it is stale, starts a
// background refresh. Only the very first call can wait (briefly) for a result.
func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	if c == nil || c.db == nil {
		return Snapshot{}, errors.New("storage usage requires the SQL storage backend")
	}
	c.mu.Lock()
	if !c.cachedAt.IsZero() && c.now().Sub(c.cachedAt) < cacheTTL {
		defer c.mu.Unlock()
		return c.cached, nil
	}
	done := c.startRefreshLocked()
	if !c.cachedAt.IsZero() {
		defer c.mu.Unlock()
		return c.cached, nil // stale but real; the refresh updates it
	}
	if done == nil {
		defer c.mu.Unlock()
		return Snapshot{}, c.pendingErrorLocked()
	}
	c.mu.Unlock()

	timer := time.NewTimer(firstWait)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-timer.C:
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.cachedAt.IsZero() {
		return c.cached, nil
	}
	return Snapshot{}, c.pendingErrorLocked()
}

func (c *Collector) pendingErrorLocked() error {
	if c.lastErr != nil {
		return c.lastErr
	}
	return errors.New("storage usage is still being measured")
}

// startRefreshLocked returns a channel closed when the running (or newly
// started) refresh ends, or nil when none runs because of failure backoff.
func (c *Collector) startRefreshLocked() <-chan struct{} {
	if c.refreshing != nil {
		return c.refreshing
	}
	if c.closed || c.now().Before(c.retryAfter) {
		return nil
	}
	done := make(chan struct{})
	c.refreshing = done
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ctx, cancel := context.WithTimeout(c.life, measureTimeout)
		defer cancel()
		snapshot, err := c.measureFunc(ctx)
		c.mu.Lock()
		if err != nil {
			c.lastErr = err
			c.retryAfter = c.now().Add(failureBackoff)
		} else {
			c.cached, c.cachedAt, c.lastErr, c.retryAfter = snapshot, c.now(), nil, time.Time{}
		}
		c.refreshing = nil
		c.mu.Unlock()
		close(done)
	}()
	return done
}

func (c *Collector) measure(ctx context.Context) (Snapshot, error) {
	totals := map[string]int64{}
	for _, t := range tables {
		// One short statement per table so the shared connection is released
		// between tables instead of being held for the whole measurement.
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-time.After(yieldGap):
		}
		size, err := c.tableBytes(ctx, t)
		if err != nil {
			return Snapshot{}, fmt.Errorf("measure %s: %w", t.name, err)
		}
		totals[t.category] += size
	}
	for _, dir := range []struct {
		path     string
		category string
	}{{c.stickerDir, CategorySticker}, {c.logDir, CategoryOther}, {c.checkpointDir, CategoryChat}} {
		size, err := dirBytes(ctx, dir.path)
		if err != nil {
			return Snapshot{}, err
		}
		totals[dir.category] += size
	}

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
	if t.sizeColumn != "" {
		parts = append(parts, "COALESCE("+t.sizeColumn+", 0)")
	}
	if t.vector != "" {
		parts = append(parts, c.vectorExpr(t.vector))
	}
	query := "SELECT COALESCE(SUM(" + strings.Join(parts, " + ") + "), 0) FROM " + t.name + " WHERE instance_id = ?"
	var size int64
	ctx, cancel := context.WithTimeout(ctx, c.queryTimeout)
	defer cancel()
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

// dirBytes totals regular files below dir. A missing directory (or a file that
// disappears mid-walk) counts as zero; any other read failure is an error so an
// incomplete total is never reported as a real one.
func dirBytes(ctx context.Context, dir string) (int64, error) {
	if dir == "" {
		return 0, nil
	}
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if cancelled := ctx.Err(); cancelled != nil {
			return cancelled
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			if errors.Is(infoErr, fs.ErrNotExist) {
				return nil
			}
			return infoErr
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", filepath.Base(dir), err)
	}
	return total, nil
}
