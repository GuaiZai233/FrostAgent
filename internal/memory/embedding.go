package memory

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/provider/llm/openai"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/storage"
)

var ErrEmbeddingNotReady = errors.New("向量索引尚未就绪")
var errEmbeddingDimensionsChanged = errors.New("Embedding 向量维度已变化，正在重建索引")

// SearchScope is supplied by trusted runtime/RPC routing, never by model text.
type SearchScope struct {
	Type                     ScopeType
	Platform, GroupID, Owner string
}

func (s SearchScope) normalized() SearchScope {
	if s.Type == ScopeGroup {
		s.Platform, s.GroupID, s.Owner = CanonicalPlatform(s.Platform), CanonicalGroupID(s.GroupID), ""
	} else {
		s.Type, s.Platform, s.GroupID, s.Owner = ScopePrivate, "", "", CanonicalOwner(s.Owner)
	}
	return s
}
func (s SearchScope) route() core.RouteContext {
	return core.RouteContext{Platform: s.Platform, GroupID: s.GroupID}
}

type embeddingBootstrapKey struct{}

func (m *Embeddings) BindWriter(writer *Writer) {
	writer.embeddings = m
	m.store.refreshEditedSummaries = true
	m.groups.refreshEditedSummaries = true
	m.reflector.embeddings = m
}

type embeddedMemory struct {
	Hash   string
	Vector []float32
}
type embeddingState struct {
	Fingerprint              string
	SourceHash               string
	RequestedHash            string
	Ready                    bool
	Vectors                  map[string]embeddedMemory
	Active, Pending, Reflect bool
	LastError                error
	RetryAt                  time.Time
	Failures                 int
	Generation               uint64
	CatalogRevision          int64
}

// Embeddings owns derived indexes and workers within one instance lifetime.
// SQLite indexes only live here; PostgreSQL publishes a scope in one transaction.
type Embeddings struct {
	*runtimescope.Scope
	store                  *Store
	groups                 *GroupManager
	router                 *modelrouter.Manager
	reflector              *Reflector
	db                     *storage.DB
	instanceID, vectorType string
	mu                     sync.Mutex
	startOnce              sync.Once
	states                 map[SearchScope]*embeddingState
}

func NewEmbeddings(scope *runtimescope.Scope, store *Store, groups *GroupManager, router *modelrouter.Manager, reflector *Reflector) (*Embeddings, error) {
	m := &Embeddings{Scope: scope, store: store, groups: groups, router: router, reflector: reflector, states: map[SearchScope]*embeddingState{}}
	if store.sql != nil {
		m.db, m.instanceID = store.sql.db, store.sql.instanceID
	}
	if m.db != nil && m.db.Backend == storage.Postgres {
		var err error
		m.vectorType, err = m.db.EnsureEmbeddingStorage(context.Background())
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Start discovers model changes even when the shared catalog is published by
// another instance. This timer checks index compatibility; it does not schedule
// periodic reflection.
func (m *Embeddings) Start() {
	m.startOnce.Do(func() {
		m.Go(func() {
			m.scan()
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-m.Context().Done():
					return
				case <-ticker.C:
					m.scan()
				}
			}
		})
	})
}

// CarryIndexesFrom retains volatile indexes during a settings hot reload.
// The old scope must have been cancelled and drained before this call.
func (m *Embeddings) CarryIndexesFrom(old *Embeddings) {
	if m == nil || old == nil || m.db != old.db || m.instanceID != old.instanceID {
		return
	}
	old.mu.Lock()
	defer old.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for s, state := range old.states {
		if !state.Ready {
			continue
		}
		m.states[s] = &embeddingState{Fingerprint: state.Fingerprint, SourceHash: state.SourceHash,
			RequestedHash: state.SourceHash, Ready: state.Ready, Vectors: state.Vectors, Generation: state.Generation,
			CatalogRevision: state.CatalogRevision}
	}
}

func (m *Embeddings) scopes() ([]SearchScope, error) {
	entries, err := m.store.ListAll()
	if err != nil {
		return nil, err
	}
	seen := map[SearchScope]bool{}
	for _, e := range entries {
		seen[SearchScope{Type: ScopePrivate, Owner: e.Owner}.normalized()] = true
	}
	groups, err := m.groups.ListGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g.MemoryCount > 0 {
			seen[SearchScope{Type: ScopeGroup, Platform: g.Platform, GroupID: g.GroupID}.normalized()] = true
		}
	}
	scopes := make([]SearchScope, 0, len(seen))
	for s := range seen {
		scopes = append(scopes, s)
	}
	sort.Slice(scopes, func(i, j int) bool { return fmt.Sprint(scopes[i]) < fmt.Sprint(scopes[j]) })
	return scopes, nil
}

func (m *Embeddings) scan() {
	scopes, err := m.scopes()
	if err != nil {
		m.Log().Warn(logs.SYSTEM, fmt.Sprintf("读取向量索引范围失败: %v", err))
		return
	}
	for _, s := range scopes {
		if _, err := m.ensure(s); err != nil && !errors.Is(err, ErrEmbeddingNotReady) {
			m.Log().Warn(logs.SYSTEM, fmt.Sprintf("检查 Embedding 路由失败: %v", err))
		}
	}
}

func (m *Embeddings) ensure(s SearchScope) (*embeddingState, error) {
	s = s.normalized()
	snapshot := m.router.Snapshot()
	target, err := snapshot.Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{Platform: s.Platform, GroupID: s.GroupID})
	if err != nil {
		return nil, fmt.Errorf("Embedding 模型未配置或不可用: %w", err)
	}
	fp := modelrouter.EmbeddingFingerprint(target)
	entries, err := m.entries(s)
	if err != nil {
		return nil, err
	}
	sourceHash := memorySnapshotHash(entries)
	m.mu.Lock()
	state := m.states[s]
	if state == nil {
		state = &embeddingState{}
		m.states[s] = state
		if m.vectorType != "" {
			stored, storedHash, loadErr := m.persistedState(m.Context(), s)
			if loadErr != nil {
				m.mu.Unlock()
				return nil, loadErr
			}
			if stored == fp {
				state.Fingerprint, state.Ready = fp, true
				state.SourceHash = storedHash
				state.RequestedHash = storedHash
				state.Vectors, loadErr = m.persistedHashes(m.Context(), s)
				if loadErr != nil {
					m.mu.Unlock()
					return nil, loadErr
				}
			}
		}
	}
	if state.Fingerprint != fp {
		state.Fingerprint, state.Ready, state.Vectors, state.LastError = fp, false, nil, nil
		state.RequestedHash = sourceHash
		state.RetryAt, state.Failures = time.Time{}, 0
		state.Generation++
		state.Reflect = true
		m.queueLocked(s, state)
	} else if state.RequestedHash != sourceHash {
		if state.Ready && len(entries) > 0 {
			hasValid := false
			for _, e := range entries {
				if v, ok := state.Vectors[e.ID]; ok && v.Hash == memoryHash(e) {
					hasValid = true
					break
				}
			}
			if !hasValid {
				state.Ready, state.Reflect = false, true
			}
		}
		state.RequestedHash = sourceHash
		state.LastError, state.RetryAt, state.Failures = nil, time.Time{}, 0
		m.queueLocked(s, state)
	} else if !state.Active && state.LastError != nil && state.CatalogRevision != snapshot.CatalogRevision() {
		// A publication can repair permanent errors such as invalid credentials.
		// rebuild records its own snapshot revision, so an in-flight old request
		// cannot consume a publication that should wake it after failure.
		state.LastError, state.RetryAt, state.Failures = nil, time.Time{}, 0
		m.queueLocked(s, state)
	} else if !state.Active && !state.RetryAt.IsZero() && !time.Now().Before(state.RetryAt) {
		// Each worker has a finite request budget. Later scans may start a new
		// attempt after backoff, without repeating completed reflection.
		state.RetryAt = time.Time{}
		m.queueLocked(s, state)
	}
	ready := state.Ready
	m.mu.Unlock()
	if !ready {
		return state, ErrEmbeddingNotReady
	}
	return state, nil
}

// Update queues the entire affected owner/group after a successful extraction
// or reflection, coalescing concurrent requests instead of dropping them.
func (m *Embeddings) Update(s SearchScope) {
	if m == nil {
		return
	}
	s = s.normalized()
	_, _ = m.ensure(s)
	m.mu.Lock()
	state := m.states[s]
	if state != nil {
		state.RetryAt, state.Failures = time.Time{}, 0
		m.queueLocked(s, state)
	}
	m.mu.Unlock()
}
func (m *Embeddings) queueLocked(s SearchScope, state *embeddingState) {
	state.Pending = true
	if state.Active {
		return
	}
	state.Active = true
	if !m.Go(func() { m.worker(s) }) {
		state.Active = false
	}
}
func (m *Embeddings) worker(s SearchScope) {
	for {
		m.mu.Lock()
		state := m.states[s]
		if !state.Pending || m.Context().Err() != nil {
			state.Active = false
			m.mu.Unlock()
			return
		}
		reflect, fp := state.Reflect, state.Fingerprint
		state.Pending, state.Reflect = false, false
		m.mu.Unlock()
		var err error
		if reflect && m.reflector != nil {
			if s.Type == ScopeGroup {
				group, gErr := m.groups.GetGroupStoreForPlatform(s.Platform, s.GroupID)
				if gErr != nil {
					err = gErr
				} else {
					err = m.reflector.ReflectGroup(context.WithValue(m.Context(), embeddingBootstrapKey{}, true), group)
				}
			} else {
				err = m.reflector.ReflectOwner(context.WithValue(m.Context(), embeddingBootstrapKey{}, true), s.Owner)
			}
			if err != nil {
				m.Log().Warn(logs.SYSTEM, fmt.Sprintf("自动反思失败，继续重建有效记忆向量: %v", err))
			}
		}
		err = m.rebuild(m.Context(), s, fp)
		m.mu.Lock()
		state = m.states[s]
		state.LastError = err
		state.RetryAt = time.Time{}
		if err == nil {
			state.Failures = 0
		} else if transientEmbeddingError(err) && m.Context().Err() == nil {
			if state.Failures < 6 {
				state.Failures++
			}
			state.RetryAt = time.Now().Add(embeddingRecoveryDelay(state.Failures))
		}
		if errors.Is(err, ErrConditionFailed) && m.Context().Err() == nil {
			state.Pending = true
		}
		m.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			m.Log().Warn(logs.SYSTEM, fmt.Sprintf("记忆向量更新失败（%v）: %v", s, err))
		}
		if errors.Is(err, ErrConditionFailed) {
			_, _ = m.ensure(s)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-m.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func embeddingRecoveryDelay(failures int) time.Duration {
	delay := 15 * time.Second
	for attempt := 1; attempt < failures && delay < 5*time.Minute; attempt++ {
		delay *= 2
	}
	return min(delay, 5*time.Minute)
}

func (m *Embeddings) entries(s SearchScope) ([]MemoryEntry, error) {
	if s.Type == ScopeGroup {
		g, err := m.groups.GetGroupStoreForPlatform(s.Platform, s.GroupID)
		if err != nil {
			return nil, err
		}
		entries, err := g.ListAll()
		return embeddingEligible(entries), err
	}
	if s.Owner == "" {
		entries, err := m.store.ListAll()
		return embeddingEligible(entries), err
	}
	entries, err := m.store.ListByOwner(s.Owner)
	return embeddingEligible(entries), err
}

func embeddingEligible(entries []MemoryEntry) []MemoryEntry {
	result := make([]MemoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.Source != SourceCompact {
			result = append(result, e)
		}
	}
	return result
}

func memoryHash(e MemoryEntry) string {
	tags := append([]string(nil), e.Tags...)
	sort.Strings(tags)
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t", CanonicalOwner(e.Owner), e.Content, e.Summary, strings.Join(tags, "\x00"), e.SummaryNeedsRefresh)
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}
func memorySnapshotHash(entries []MemoryEntry) string {
	values := make([]string, 0, len(entries))
	for _, e := range entries {
		values = append(values, e.ID+"\x00"+memoryHash(e))
	}
	sort.Strings(values)
	hash := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(hash[:])
}

func embeddingText(e MemoryEntry) string {
	text := strings.TrimSpace(e.Summary)
	if text == "" {
		text = e.Content
	}
	if len(e.Tags) > 0 {
		text += "\n标签：" + strings.Join(e.Tags, ", ")
	}
	return text
}
func snapshotMatches(before, after []MemoryEntry) bool {
	if len(before) != len(after) {
		return false
	}
	values := map[string]string{}
	for _, e := range before {
		values[e.ID] = memoryHash(e)
	}
	for _, e := range after {
		if values[e.ID] != memoryHash(e) {
			return false
		}
	}
	return true
}

func (m *Embeddings) rebuild(ctx context.Context, s SearchScope, fp string) error {
	snapshot := m.router.Snapshot()
	m.mu.Lock()
	m.states[s].CatalogRevision = snapshot.CatalogRevision()
	m.mu.Unlock()
	target, provider, err := m.router.EmbeddingSession(m.router.WithSnapshot(ctx, snapshot), modelrouter.Scope{Platform: s.Platform, GroupID: s.GroupID})
	if err != nil {
		return err
	}
	if modelrouter.EmbeddingFingerprint(target) != fp {
		return ErrConditionFailed
	}
	entries, err := m.entries(s)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.SummaryNeedsRefresh {
			summary := ""
			resp, sErr := m.router.MemoryChat(ctx, s.route(), core.ChatRequest{Messages: []core.ChatMessage{{Role: core.RoleUser,
				Content: "请把下面的记忆内容写成一段简洁、独立可理解的摘要。只返回摘要，不添加事实；图片来源仍需明确表述为图片内容。\n" + e.Content}}, MaxTokens: 512})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if sErr == nil {
				if text, ok := resp.Message.Content.(string); ok {
					summary = SanitizeSummary(text)
				}
			} else {
				m.Log().Warn(logs.SYSTEM, fmt.Sprintf("记忆摘要生成失败，使用正文: %v", sErr))
			}
			if err := m.replaceSummary(ctx, s, e, summary); err != nil {
				return err
			}
		}
	}
	entries, err = m.entries(s)
	if err != nil {
		return err
	}
	vectors := make(map[string]embeddedMemory, len(entries))
	dimensions := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		text := core.LimitInput(embeddingText(e), target.MaxInputTokens)
		if text == "" {
			m.Log().Warn(logs.SYSTEM, "记忆输入无法截断至模型窗口，跳过该条向量")
			continue
		}
		response, err := embedRetry(ctx, provider, core.EmbeddingRequest{Model: target.UpstreamModel, Input: []string{text}, Route: s.route()})
		if modelrouter.InputTooLong(err) {
			m.Log().Warn(logs.SYSTEM, fmt.Sprintf("记忆 %s 超出 Embedding 窗口，跳过该条向量", e.ID))
			continue
		}
		if err != nil {
			return err
		}
		if len(response.Vectors) != 1 || len(response.Vectors[0]) == 0 {
			return fmt.Errorf("invalid embedding response")
		}
		if dimensions == 0 {
			dimensions = len(response.Vectors[0])
		}
		if dimensions != len(response.Vectors[0]) {
			return fmt.Errorf("Embedding 模型返回了不同维度")
		}
		vectors[e.ID] = embeddedMemory{memoryHash(e), response.Vectors[0]}
	}
	current, err := m.router.Snapshot().Resolve(modelrouter.WorkloadEmbedding, modelrouter.Scope{Platform: s.Platform, GroupID: s.GroupID})
	if err != nil {
		return err
	}
	if modelrouter.EmbeddingFingerprint(current) != fp {
		return ErrConditionFailed
	}
	return m.withEntriesLocked(s, func(after []MemoryEntry) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !snapshotMatches(entries, after) {
			return ErrConditionFailed
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		state := m.states[s]
		if state.Fingerprint != fp {
			return ErrConditionFailed
		}
		if m.vectorType != "" {
			if err := m.publish(ctx, s, fp, vectors, memorySnapshotHash(entries)); err != nil {
				return err
			}
		}
		state.Vectors, state.Ready = vectors, true
		state.SourceHash = memorySnapshotHash(entries)
		state.RequestedHash = state.SourceHash
		state.Generation++
		return nil
	})
}

func embedRetry(ctx context.Context, provider core.EmbeddingProvider, req core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	for attempt := 0; ; attempt++ {
		response, err := provider.Embed(ctx, req)
		if err == nil {
			return response, nil
		}
		if attempt >= 2 || ctx.Err() != nil || !transientEmbeddingError(err) {
			return nil, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
func transientEmbeddingError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var httpErr *openai.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 429 || (httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599)
	}
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	if errors.As(err, &verification) || errors.As(err, &authority) || errors.As(err, &invalid) || errors.As(err, &hostname) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return dns.Timeout() || dns.Temporary()
	}
	var address *net.AddrError
	if errors.As(err, &address) {
		return false
	}
	var network net.Error
	if errors.As(err, &network) && (network.Timeout() || network.Temporary()) {
		return true
	}
	var operation *net.OpError
	if errors.As(err, &operation) {
		return operation.Op == "dial" || operation.Op == "read" || operation.Op == "write"
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func (m *Embeddings) Search(ctx context.Context, s SearchScope, query string, limit int) ([]MemoryEntry, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.Context(), cancel)
	defer stop()
	s = s.normalized()
	if s.Type == ScopePrivate && s.Owner == "" {
		return m.searchAllPrivate(ctx, query, limit)
	}
	if _, err := m.ensure(s); err != nil {
		return nil, err
	}
	target, provider, err := m.router.EmbeddingSession(ctx, modelrouter.Scope{Platform: s.Platform, GroupID: s.GroupID})
	if err != nil {
		return nil, err
	}
	fp := modelrouter.EmbeddingFingerprint(target)
	generation, err := m.indexGeneration(s, fp)
	if err != nil {
		return nil, err
	}
	text := core.LimitInput(query, target.MaxInputTokens)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("搜索查询为空或无法截断")
	}
	response, err := embedRetry(ctx, provider, core.EmbeddingRequest{Model: target.UpstreamModel, Input: []string{text}, Route: s.route()})
	if err != nil {
		return nil, err
	}
	queryVector := response.Vectors[0]
	var result []MemoryEntry
	err = m.withEntriesLocked(s, func(entries []MemoryEntry) error {
		scores, err := m.searchScores(ctx, s, fp, queryVector, generation)
		if err != nil {
			return err
		}
		result = rankEmbedded(entries, scores, limit)
		return nil
	})
	return result, err
}

func (m *Embeddings) indexGeneration(s SearchScope, fp string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[s]
	if state == nil || !state.Ready || state.Fingerprint != fp {
		return 0, ErrEmbeddingNotReady
	}
	return state.Generation, nil
}

func (m *Embeddings) searchScores(ctx context.Context, s SearchScope, fp string, query []float32, generation uint64) ([]embeddingScore, error) {
	m.mu.Lock()
	state := m.states[s]
	if state == nil || !state.Ready || state.Fingerprint != fp {
		m.mu.Unlock()
		return nil, ErrEmbeddingNotReady
	}
	vectors := state.Vectors
	m.mu.Unlock()
	var scores []embeddingScore
	var err error
	if m.vectorType != "" {
		scores, err = m.pgScores(ctx, s, fp, query)
	} else {
		for id, v := range vectors {
			if len(v.Vector) != len(query) {
				err = errEmbeddingDimensionsChanged
				break
			}
			scores = append(scores, embeddingScore{id, v.Hash, cosineDistance(query, v.Vector)})
		}
	}
	if errors.Is(err, errEmbeddingDimensionsChanged) {
		m.invalidateDimensions(s, fp, generation)
		return nil, fmt.Errorf("%w: %w", ErrEmbeddingNotReady, err)
	}
	return scores, err
}

func (m *Embeddings) invalidateDimensions(s SearchScope, fp string, generation uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.states[s]
	// A query against the previous publication must not invalidate a newer one.
	if state == nil || !state.Ready || state.Fingerprint != fp || state.Generation != generation {
		return
	}
	state.Ready, state.Reflect = false, true
	state.RetryAt, state.Failures = time.Time{}, 0
	m.queueLocked(s, state)
}

type embeddingScore struct {
	ID, Hash string
	Distance float64
}

func rankEmbedded(entries []MemoryEntry, scores []embeddingScore, limit int) []MemoryEntry {
	current := map[string]MemoryEntry{}
	for _, e := range entries {
		current[e.ID] = e
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Distance == scores[j].Distance {
			return scores[i].ID < scores[j].ID
		}
		return scores[i].Distance < scores[j].Distance
	})
	result := []MemoryEntry{}
	for _, score := range scores {
		if e, ok := current[score.ID]; ok && memoryHash(e) == score.Hash {
			result = append(result, e)
			if limit > 0 && len(result) == limit {
				break
			}
		}
	}
	return result
}
func cosineDistance(a, b []float32) float64 {
	var dot, na, nb float64
	for i, v := range a {
		dot += float64(v) * float64(b[i])
		na += float64(v) * float64(v)
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 2
	}
	return 1 - dot/math.Sqrt(na*nb)
}
