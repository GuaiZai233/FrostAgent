package memory

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Writer handles memory writing.
type Writer struct {
	store *Store
	vs    *VectorStore
	// LLM fields (set via SetLLM)
	provider core.LLMProvider
	model    string
}

// NewWriter creates a new memory writer.
func NewWriter(store *Store) *Writer {
	return &Writer{store: store}
}

// SetLLM configures the LLM provider for automatic memory extraction.
func (w *Writer) SetLLM(provider core.LLMProvider, model string) {
	w.provider = provider
	w.model = model
}

// SetVectorStore configures the vector store for semantic indexing.
func (w *Writer) SetVectorStore(vs *VectorStore) {
	w.vs = vs
}

// Write directly saves a memory entry and indexes it in the vector store
// (user explicitly said "remember this").
func (w *Writer) Write(owner string, content string, tags []string) error {
	entry := MemoryEntry{
		ID:         generateID(),
		Owner:      owner,
		Content:    content,
		Tags:       tags,
		Source:     SourceManual,
		Visibility: VisibilityPrivate,
		Importance: 0.8,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := w.store.Save(entry); err != nil {
		return err
	}
	w.indexEntry(context.Background(), entry)
	return nil
}

// Extract uses LLM to analyze conversation and extract memories.
// Called asynchronously after each conversation turn.
// Extracted memories are also indexed in the vector store.
func (w *Writer) Extract(owner string, messages []core.ChatMessage) error {
	if w.provider == nil || w.model == "" {
		return nil // LLM not configured, skip extraction
	}

	// Format recent messages for the prompt
	var conversation strings.Builder
	for _, msg := range messages {
		if msg.Role == core.RoleSystem {
			continue
		}
		content := fmt.Sprintf("%v", msg.Content)
		fmt.Fprintf(&conversation, "[%s]: %s\n", msg.Role, content)
	}

	prompt := strings.Replace(extractPrompt, "{conversation}", conversation.String(), 1)

	req := core.ChatRequest{
		Model: w.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   1024,
		Temperature: 0.3,
	}

	resp, err := w.provider.Chat(context.Background(), req)
	if err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("记忆提取LLM调用失败: %v", err))
		return err
	}

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", resp.Message.Content)
	}

	return w.parseAndSave(owner, raw)
}

// extractedEntry represents one item from the LLM extraction response.
type extractedEntry struct {
	Content    string   `json:"content"`
	Tags       []string `json:"tags"`
	Visibility string   `json:"visibility"`
}

// parseAndSave parses the LLM JSON response and saves entries to the store.
func (w *Writer) parseAndSave(owner string, raw string) error {
	// Strip markdown code fences if present
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if raw == "" || raw == "[]" {
		return nil
	}

	var entries []extractedEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		logs.Error(logs.SYSTEM, fmt.Sprintf("记忆提取JSON解析失败: %v, raw: %s", err, raw))
		return err
	}

	ctx := context.Background()
	for _, e := range entries {
		if e.Content == "" {
			continue
		}
		vis := VisibilityPrivate
		if e.Visibility == "public" {
			vis = VisibilityPublic
		}
		entry := MemoryEntry{
			ID:         generateID(),
			Owner:      owner,
			Content:    e.Content,
			Tags:       e.Tags,
			Source:     SourceExtract,
			Visibility: vis,
			Importance: 0.6,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := w.store.Save(entry); err != nil {
			logs.Error(logs.SYSTEM, fmt.Sprintf("记忆保存失败: %v", err))
			continue
		}
		w.indexEntry(ctx, entry)
	}

	if len(entries) > 0 {
		logs.Info(logs.SYSTEM, fmt.Sprintf("从对话中提取了 %d 条记忆 (owner: %s)", len(entries), owner))
	}
	return nil
}

// generateID creates a random hex ID prefixed with "mem_".
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "mem_" + hex.EncodeToString(b)
}

// indexEntry indexes a memory entry in the vector store using content + tags.
// This ensures that tag terms are also searchable via semantic search.
func (w *Writer) indexEntry(ctx context.Context, entry MemoryEntry) {
	if w.vs == nil {
		return
	}
	if err := w.vs.IndexEntry(ctx, entry); err != nil {
		logs.Warn(logs.SYSTEM, fmt.Sprintf("记忆向量索引失败 (id: %s): %v", entry.ID, err))
		if !errors.Is(err, ErrVectorIndexRebuildRequired) {
			if removeErr := w.vs.Remove(entry.ID); removeErr != nil {
				logs.Warn(logs.SYSTEM, fmt.Sprintf("移除过期记忆向量失败 (id: %s): %v", entry.ID, removeErr))
			}
		}
	}
}
