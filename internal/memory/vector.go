package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
)

// Embedder abstracts embedding generation for semantic search.
// Implementations call an external embedding API (e.g. OpenAI /embeddings).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// vectorRecord pairs a memory ID with its embedding vector.
type vectorRecord struct {
	ID     string    `json:"id"`
	Vector []float32 `json:"vector"`
}

// vectorFile is the on-disk layout for the vector store.
type vectorFile struct {
	Dimension int            `json:"dimension,omitempty"`
	Records   []vectorRecord `json:"records"`
}

// VectorStore provides semantic (vector) search over memories.
// It stores embeddings in a separate JSON file next to brain.json and uses
// cosine similarity for retrieval. Requires an Embedder; if none is set the
// caller falls back to keyword search.
type VectorStore struct {
	path     string
	embedder Embedder
	mu       sync.RWMutex
}

// NewVectorStore creates a vector store backed by the given JSON file path.
func NewVectorStore(path string, embedder Embedder) *VectorStore {
	return &VectorStore{path: path, embedder: embedder}
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

	v.mu.Lock()
	defer v.mu.Unlock()

	file, err := v.load()
	if err != nil {
		return err
	}

	rec := vectorRecord{ID: memoryID, Vector: vecs[0]}
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

	return v.save(file)
}

// Search returns the top-k memory IDs whose embeddings are most similar
// (cosine similarity) to the query embedding.
func (v *VectorStore) Search(ctx context.Context, query string, limit int) ([]string, error) {
	if v.embedder == nil {
		return nil, fmt.Errorf("vector store has no embedder")
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
		results = append(results, scored{id: r.ID, score: cosineSim(queryVec, r.Vector)})
	}

	// Limit 0 means unlimited.
	k := limit
	if k <= 0 || k > len(results) {
		k = len(results)
	}
	top := make([]string, 0, k)
	used := make([]bool, len(results))
	for i := 0; i < k; i++ {
		bestIdx := -1
		var bestScore float32
		for j, r := range results {
			if used[j] {
				continue
			}
			if bestIdx == -1 || r.score > bestScore {
				bestIdx = j
				bestScore = r.score
			}
		}
		if bestIdx == -1 {
			break
		}
		used[bestIdx] = true
		top = append(top, results[bestIdx].id)
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
