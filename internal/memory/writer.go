package memory

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/runtimescope"
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
	*runtimescope.Scope
	store *Store
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

// RememberRoute records the current owner's transient routing scope.
func (w *Writer) RememberRoute(owner string, route core.RouteContext) {
	if w == nil || w.store == nil {
		return
	}
	w.store.RememberRoute(owner, route)
}

// Write directly saves a memory entry (user explicitly said "remember this").
func (w *Writer) Write(owner string, content string, tags []string) error {
	return w.WriteByOwner(owner, OwnerUser, content, tags)
}

// WriteByOwner directly saves a memory with an explicit owner namespace.
func (w *Writer) WriteByOwner(
	owner string,
	ownerType OwnerType,
	content string,
	tags []string,
) error {
	entry := MemoryEntry{
		ID:         generateID(),
		Owner:      owner,
		OwnerType:  NormalizeOwnerType(ownerType),
		Content:    content,
		Tags:       tags,
		Source:     SourceManual,
		Visibility: VisibilityPrivate,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	return w.store.Save(entry)
}

// Extract uses LLM to analyze conversation and extract memories.
// Called asynchronously after each conversation turn.
func (w *Writer) Extract(owner string, messages []core.ChatMessage) error {
	return w.ExtractByOwner(owner, OwnerUser, messages)
}

// ExtractByOwner extracts memories into the provided owner namespace.
func (w *Writer) ExtractByOwner(
	owner string,
	ownerType OwnerType,
	messages []core.ChatMessage,
) error {
	route := core.RouteContext{}
	if w != nil && w.store != nil {
		route = w.store.RouteForOwner(owner)
	}
	return w.ExtractByOwnerWithRoute(owner, ownerType, route, messages)
}

// ExtractByOwnerWithRoute extracts memories with an explicit model route.
func (w *Writer) ExtractByOwnerWithRoute(
	owner string,
	ownerType OwnerType,
	route core.RouteContext,
	messages []core.ChatMessage,
) error {
	return w.ExtractByOwnerWithRouteContext(w.Context(), owner, ownerType, route, messages, nil)
}

// ExtractByOwnerWithRouteContext extracts memories with context and an optional validator.
// The validator (if provided) is invoked before LLM call, after LLM call, and before saving each entry into the store.
// If the context is cancelled or the validator returns false, extraction is aborted without saving.
func (w *Writer) ExtractByOwnerWithRouteContext(
	ctx context.Context,
	owner string,
	ownerType OwnerType,
	route core.RouteContext,
	messages []core.ChatMessage,
	validator func() bool,
) error {
	if ctx == nil {
		ctx = w.Context()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if validator != nil && !validator() {
		return errors.New("extraction cancelled or invalidated")
	}
	w.RememberRoute(owner, route)
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

	prompt := strings.Replace(
		extractPrompt,
		"{conversation}",
		conversation.String(),
		1,
	)
	prompt = strings.Replace(prompt, "{current_time}", CurrentTimeLabel(time.Now()), 1)

	req := core.ChatRequest{
		Model: w.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   1024,
		Temperature: 0.3,
		Route:       route,
	}

	resp, err := w.provider.Chat(ctx, req)
	if err != nil {
		if errors.Is(err, modelrouter.ErrDisabled) {
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("记忆提取LLM调用失败: %v", err))
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if validator != nil && !validator() {
		return errors.New("extraction cancelled or invalidated")
	}

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", resp.Message.Content)
	}

	return w.parseAndSave(ctx, owner, NormalizeOwnerType(ownerType), raw, validator)
}

// extractedEntry represents one item from the LLM extraction response.
type extractedEntry struct {
	Content    string   `json:"content"`
	Tags       []string `json:"tags"`
	Visibility string   `json:"visibility"`
}

// parseAndSave parses the LLM JSON response and saves entries to the store.
func (w *Writer) parseAndSave(
	ctx context.Context,
	owner string,
	ownerType OwnerType,
	raw string,
	validator func() bool,
) error {
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
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("记忆提取JSON解析失败: %v, raw: %s", err, raw))
		return err
	}

	for _, e := range entries {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if validator != nil && !validator() {
			return errors.New("extraction cancelled or invalidated")
		}
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
			OwnerType:  ownerType,
			Content:    e.Content,
			Tags:       e.Tags,
			Source:     SourceExtract,
			Visibility: vis,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := w.store.Save(entry); err != nil {
			w.Log().Error(logs.SYSTEM, fmt.Sprintf("记忆保存失败: %v", err))
			continue
		}
	}

	if len(entries) > 0 {
		w.Log().Info(logs.SYSTEM, fmt.Sprintf("从对话中提取了 %d 条记忆 (owner: %s)", len(entries), owner))
	}
	return nil
}

// generateID creates a random hex ID prefixed with "mem_".
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "mem_" + hex.EncodeToString(b)
}
