package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
)

const (
	currentVectorIndexVersion = 1
	defaultMinSimilarity      = 0.5
)

// Embedder abstracts embedding generation for semantic search.
// Implementations call an external embedding API (e.g. OpenAI /embeddings).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// vectorRecord pairs a memory ID with its embedding vector.
type vectorRecord struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Vector      []float32 `json:"vector"`
}

// vectorFile is the on-disk layout for the vector store.
type vectorFile struct {
	Version   int            `json:"version,omitempty"`
	Model     string         `json:"model,omitempty"`
	Dimension int            `json:"dimension,omitempty"`
	Records   []vectorRecord `json:"records"`
}

// VectorStore provides semantic (vector) search over memories.
// It stores embeddings in a separate JSON file next to brain.json and uses
// cosine similarity for retrieval. Requires an Embedder; if none is set the
// caller falls back to keyword search.
type VectorStore struct {
	path          string
	embedder      Embedder
	indexIdentity string
	minSimilarity float32
	mu            sync.RWMutex
}

// NewVectorStore creates a vector store backed by the given JSON file path.
func NewVectorStore(path string, embedder Embedder) *VectorStore {
	return &VectorStore{
		path:          path,
		embedder:      embedder,
		minSimilarity: defaultMinSimilarity,
	}
}

// SetIndexIdentity records the embedding model/configuration used to create
// vectors so a model change triggers a rebuild even when dimensions match.
func (v *VectorStore) SetIndexIdentity(identity string) {
	v.indexIdentity = identity
}

// SetMinSimilarity configures the minimum cosine similarity accepted by Search.
func (v *VectorStore) SetMinSimilarity(minSimilarity float32) {
	if minSimilarity >= -1 && minSimilarity <= 1 {
		v.minSimilarity = minSimilarity
	}
}

// Index generates an embedding for the given memory and stores it.
// If a vector for memoryID already exists it is replaced.
func (v *VectorStore) Index(ctx context.Context, memoryID, content string) error {
	if v.embedder == nil {
		return fmt.Errorf("vector store has no embedder")
	}
	vecs, err := v.embedder.Embed(ctx, []string{content})
	if err != nil {
		return fmt.Errorf("embed memory %s: %w", memoryID, err)
	}
	if len(vecs) == 0 {
		return fmt.Errorf("embedder returned no vector for %s", memoryID)
	}
	if len(vecs[0]) == 0 {
		return fmt.Errorf("embedder returned an empty vector for %s", memoryID)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	file, err := v.load()
	if err != nil {
		return err
	}
	if file.Dimension != 0 && file.Dimension != len(vecs[0]) {
		return fmt.Errorf(
			"vector dimension changed from %d to %d; rebuild required",
			file.Dimension,
			len(vecs[0]),
		)
	}

	rec := vectorRecord{
		ID:          memoryID,
		Fingerprint: textFingerprint(content),
		Vector:      vecs[0],
	}
	replaced := false
	for i, r := range file.Records {
		if r.ID == memoryID {
			file.Records[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		file.Records = append(file.Records, rec)
	}
	if file.Dimension == 0 && len(vecs[0]) > 0 {
		file.Dimension = len(vecs[0])
	}
	file.Version = currentVectorIndexVersion
	file.Model = v.indexIdentity

	return v.save(file)
}

// IndexEntry indexes the fields used for memory retrieval.
func (v *VectorStore) IndexEntry(ctx context.Context, entry MemoryEntry) error {
	return v.Index(ctx, entry.ID, entryIndexText(entry))
}

// NeedsRebuild reports whether the stored vectors were created with an older
// schema or no longer match the current memory contents.
func (v *VectorStore) NeedsRebuild(entries []MemoryEntry) (bool, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	file, err := v.load()
	if err != nil {
		return false, err
	}
	if file.Version != currentVectorIndexVersion ||
		file.Model != v.indexIdentity ||
		len(file.Records) != len(entries) {
		return true, nil
	}

	fingerprints := make(map[string]string, len(file.Records))
	for _, record := range file.Records {
		fingerprints[record.ID] = record.Fingerprint
	}
	for _, entry := range entries {
		if fingerprints[entry.ID] != textFingerprint(entryIndexText(entry)) {
			return true, nil
		}
	}
	return false, nil
}

// Rebuild replaces the complete vector index using the current content+tags
// representation. Embeddings are generated in one batch to avoid one request
// per memory during migration.
func (v *VectorStore) Rebuild(ctx context.Context, entries []MemoryEntry) error {
	if v.embedder == nil {
		return fmt.Errorf("vector store has no embedder")
	}

	file := &vectorFile{
		Version: currentVectorIndexVersion,
		Model:   v.indexIdentity,
		Records: make([]vectorRecord, 0, len(entries)),
	}
	if len(entries) == 0 {
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.save(file)
	}

	texts := make([]string, len(entries))
	for i, entry := range entries {
		texts[i] = entryIndexText(entry)
	}
	vectors, err := v.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("rebuild vector index: %w", err)
	}
	if len(vectors) != len(entries) {
		return fmt.Errorf("rebuild vector index: embedder returned %d vectors for %d memories", len(vectors), len(entries))
	}

	for i, entry := range entries {
		if len(vectors[i]) == 0 {
			return fmt.Errorf("rebuild vector index: empty vector for %s", entry.ID)
		}
		if file.Dimension == 0 {
			file.Dimension = len(vectors[i])
		} else if len(vectors[i]) != file.Dimension {
			return fmt.Errorf("rebuild vector index: inconsistent dimension for %s", entry.ID)
		}
		file.Records = append(file.Records, vectorRecord{
			ID:          entry.ID,
			Fingerprint: textFingerprint(texts[i]),
			Vector:      vectors[i],
		})
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	return v.save(file)
}

// Search returns the top-k memory IDs whose embeddings are most similar
// (cosine similarity) to the query embedding and meet the relevance threshold.
func (v *VectorStore) Search(ctx context.Context, query string, limit int) ([]string, error) {
	if v.embedder == nil {
		return nil, fmt.Errorf("vector store has no embedder")
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	vecs, err := v.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedder returned no query vector")
	}
	queryVec := vecs[0]

	v.mu.RLock()
	defer v.mu.RUnlock()

	file, err := v.load()
	if err != nil {
		return nil, err
	}
	if len(file.Records) == 0 {
		return nil, nil
	}

	type scored struct {
		id    string
		score float32
	}
	results := make([]scored, 0, len(file.Records))
	for _, r := range file.Records {
		if len(r.Vector) != len(queryVec) {
			continue
		}
		score := cosineSim(queryVec, r.Vector)
		if !math.IsNaN(float64(score)) && score >= v.minSimilarity {
			results = append(results, scored{id: r.ID, score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Limit 0 means unlimited.
	k := limit
	if k <= 0 || k > len(results) {
		k = len(results)
	}
	top := make([]string, 0, k)
	for _, result := range results[:k] {
		top = append(top, result.id)
	}
	return top, nil
}

// Remove deletes the embedding for a memory ID.
func (v *VectorStore) Remove(memoryID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	file, err := v.load()
	if err != nil {
		return err
	}
	for i, r := range file.Records {
		if r.ID == memoryID {
			file.Records = append(file.Records[:i], file.Records[i+1:]...)
			return v.save(file)
		}
	}
	return nil // not found is a no-op
}

// Clear removes all vectors while retaining current index metadata. The brain
// remains the source of truth and can rebuild this derived index later.
func (v *VectorStore) Clear() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.save(&vectorFile{
		Version: currentVectorIndexVersion,
		Model:   v.indexIdentity,
		Records: []vectorRecord{},
	})
}

func (v *VectorStore) load() (*vectorFile, error) {
	raw, err := os.ReadFile(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &vectorFile{Records: []vectorRecord{}}, nil
		}
		return nil, fmt.Errorf("read vectors: %w", err)
	}
	var f vectorFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse vectors: %w", err)
	}
	if f.Records == nil {
		f.Records = []vectorRecord{}
	}
	return &f, nil
}

func (v *VectorStore) save(f *vectorFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vectors: %w", err)
	}
	return os.WriteFile(v.path, data, 0644)
}

func entryIndexText(entry MemoryEntry) string {
	parts := make([]string, 0, len(entry.Tags)+1)
	parts = append(parts, entry.Content)
	parts = append(parts, entry.Tags...)
	return strings.Join(parts, " ")
}

func textFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", sum)
}

// cosineSim computes the cosine similarity between two equal-length vectors.
func cosineSim(a, b []float32) float32 {
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (sqrt32(na) * sqrt32(nb))
}

// sqrt32 is a float32 square root helper.
func sqrt32(x float32) float32 {
	return float32(math.Sqrt(float64(x)))
}
