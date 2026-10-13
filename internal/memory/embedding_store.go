package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"FrostAgent/internal/core"
	"FrostAgent/internal/modelrouter"
)

func (m *Embeddings) withEntriesLocked(s SearchScope, fn func([]MemoryEntry) error) error {
	if s.Type == ScopeGroup {
		g, err := m.groups.GetGroupStoreForPlatform(s.Platform, s.GroupID)
		if err != nil {
			return err
		}
		g.mu.RLock()
		defer g.mu.RUnlock()
		brain, err := g.loadMemoryLocked()
		if err != nil {
			return err
		}
		return fn(embeddingEligible(brain.Entries))
	}
	m.store.mu.RLock()
	defer m.store.mu.RUnlock()
	brain, err := m.store.load()
	if err != nil {
		return err
	}
	entries := embeddingEligible(brain.Entries)
	if s.Owner != "" {
		entries = nil
		for _, e := range embeddingEligible(brain.Entries) {
			if CanonicalOwner(e.Owner) == s.Owner {
				entries = append(entries, e)
			}
		}
	}
	return fn(entries)
}

// Summary writes use the current row, not the pre-request snapshot, preserving
// access counters and rejecting concurrent edits or deletion.
func (m *Embeddings) replaceSummary(ctx context.Context, s SearchScope, before MemoryEntry, summary string) error {
	var brain *BrainData
	var err error
	var save func(*BrainData) error
	var sqlStore *sqlBrainStore
	if s.Type == ScopeGroup {
		g, gErr := m.groups.GetGroupStoreForPlatform(s.Platform, s.GroupID)
		if gErr != nil {
			return gErr
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		brain, err = g.loadMemoryLocked()
		save, sqlStore = g.saveMemoryLocked, g.sql
	} else {
		m.store.mu.Lock()
		defer m.store.mu.Unlock()
		brain, err = m.store.load()
		save, sqlStore = m.store.save, m.store.sql
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for i, e := range brain.Entries {
		if e.ID != before.ID {
			continue
		}
		if memoryHash(e) != memoryHash(before) {
			return ErrConditionFailed
		}
		e.Summary, e.SummaryNeedsRefresh = summary, false
		if sqlStore != nil {
			return sqlStore.upsertEntries([]MemoryEntry{e})
		}
		brain.Entries[i] = e
		return save(brain)
	}
	return ErrConditionFailed
}

func (m *Embeddings) scopeArgs(s SearchScope) []any {
	return []any{m.instanceID, s.Type, s.Platform, s.GroupID, s.Owner}
}

func (m *Embeddings) persistedHashes(ctx context.Context, s SearchScope) (map[string]embeddedMemory, error) {
	rows, err := m.db.SQL.QueryContext(ctx, m.db.Bind(`SELECT memory_id,content_hash FROM memory_embeddings
		WHERE instance_id=? AND scope_type=? AND platform=? AND group_id=? AND owner=?`), m.scopeArgs(s)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]embeddedMemory{}
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		result[id] = embeddedMemory{Hash: hash}
	}
	return result, rows.Err()
}
func (m *Embeddings) persistedFingerprint(ctx context.Context, s SearchScope) (string, error) {
	fp, _, err := m.persistedState(ctx, s)
	return fp, err
}
func (m *Embeddings) persistedState(ctx context.Context, s SearchScope) (string, string, error) {
	var fingerprint, sourceHash string
	err := m.db.SQL.QueryRowContext(ctx, m.db.Bind(`SELECT fingerprint,source_hash FROM memory_embedding_scopes
		WHERE instance_id=? AND scope_type=? AND platform=? AND group_id=? AND owner=?`), m.scopeArgs(s)...).Scan(&fingerprint, &sourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return fingerprint, sourceHash, err
}
func (m *Embeddings) publish(ctx context.Context, s SearchScope, fp string, vectors map[string]embeddedMemory, sourceHashes ...string) error {
	tx, err := m.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args := m.scopeArgs(s)
	if _, err = tx.ExecContext(ctx, m.db.Bind(`DELETE FROM memory_embedding_scopes
		WHERE instance_id=? AND scope_type=? AND platform=? AND group_id=? AND owner=?`), args...); err != nil {
		return err
	}
	sourceHash := ""
	if len(sourceHashes) > 0 {
		sourceHash = sourceHashes[0]
	}
	if _, err = tx.ExecContext(ctx, m.db.Bind(`INSERT INTO memory_embedding_scopes
		(instance_id,scope_type,platform,group_id,owner,fingerprint,source_hash)VALUES(?,?,?,?,?,?,?)`), append(args, fp, sourceHash)...); err != nil {
		return err
	}
	for id, value := range vectors {
		raw, err := json.Marshal(value.Vector)
		if err != nil {
			return err
		}
		query := `INSERT INTO memory_embeddings
			(instance_id,scope_type,platform,group_id,owner,memory_id,content_hash,dimensions,embedding)
			VALUES(?,?,?,?,?,?,?,?,CAST(? AS ` + m.vectorType + `))`
		values := append(m.scopeArgs(s), id, value.Hash, len(value.Vector), string(raw))
		if _, err = tx.ExecContext(ctx, m.db.Bind(query), values...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (m *Embeddings) pgScores(ctx context.Context, s SearchScope, fp string, vector []float32) ([]embeddingScore, error) {
	raw, err := json.Marshal(vector)
	if err != nil {
		return nil, err
	}
	namespace := strings.TrimSuffix(m.vectorType, ".vector")
	// CASE prevents comparing vectors of different dimensions after a provider
	// changes a model behind an unchanged name.
	query := `SELECT v.memory_id,v.content_hash,
		CASE WHEN v.dimensions=? THEN v.embedding OPERATOR(` + namespace + `.<=>)CAST(? AS ` + m.vectorType + `) ELSE -1 END
		FROM memory_embeddings v JOIN memory_embedding_scopes s
		ON (v.instance_id,v.scope_type,v.platform,v.group_id,v.owner)=(s.instance_id,s.scope_type,s.platform,s.group_id,s.owner)
		WHERE v.instance_id=? AND v.scope_type=? AND v.platform=? AND v.group_id=? AND v.owner=? AND s.fingerprint=?`
	args := append([]any{len(vector), string(raw)}, m.scopeArgs(s)...)
	args = append(args, fp)
	rows, err := m.db.SQL.QueryContext(ctx, m.db.Bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []embeddingScore{}
	for rows.Next() {
		var score embeddingScore
		if err := rows.Scan(&score.ID, &score.Hash, &score.Distance); err != nil {
			return nil, err
		}
		if score.Distance < 0 {
			return nil, errEmbeddingDimensionsChanged
		}
		result = append(result, score)
	}
	return result, rows.Err()
}

func (m *Embeddings) searchAllPrivate(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
	entries, err := m.entries(SearchScope{Type: ScopePrivate})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return []MemoryEntry{}, nil
	}
	owners := map[string]bool{}
	for _, e := range entries {
		owners[CanonicalOwner(e.Owner)] = true
	}
	ready := []SearchScope{}
	for owner := range owners {
		s := SearchScope{Type: ScopePrivate, Owner: owner}
		if _, err := m.ensure(s); err != nil {
			return nil, err
		}
		ready = append(ready, s)
	}
	target, provider, err := m.router.EmbeddingSession(ctx, modelrouter.Scope{})
	if err != nil {
		return nil, err
	}
	fp := modelrouter.EmbeddingFingerprint(target)
	generations := make(map[SearchScope]uint64, len(ready))
	for _, s := range ready {
		generation, err := m.indexGeneration(s, fp)
		if err != nil {
			return nil, err
		}
		generations[s] = generation
	}
	text := core.LimitInput(query, target.MaxInputTokens)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("搜索查询为空或无法截断")
	}
	response, err := embedRetry(ctx, provider, core.EmbeddingRequest{Model: target.UpstreamModel, Input: []string{text}})
	if err != nil {
		return nil, err
	}
	vector := response.Vectors[0]
	var result []MemoryEntry
	err = m.withEntriesLocked(SearchScope{Type: ScopePrivate}, func(current []MemoryEntry) error {
		var scores []embeddingScore
		var dimensionErr error
		for _, s := range ready {
			items, err := m.searchScores(ctx, s, fp, vector, generations[s])
			if errors.Is(err, errEmbeddingDimensionsChanged) {
				dimensionErr = err
				continue
			}
			if err != nil {
				return err
			}
			scores = append(scores, items...)
		}
		if dimensionErr != nil {
			return dimensionErr
		}
		result = rankEmbedded(current, scores, limit)
		return nil
	})
	return result, err
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
