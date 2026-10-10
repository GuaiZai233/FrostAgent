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

var ErrStorageUnavailable = errors.New("memory storage unavailable")

// Writer handles memory writing.
type Writer struct {
	*runtimescope.Scope
	store        *Store
	groupManager *GroupManager
	// LLM fields (set via SetLLM)
	provider core.LLMProvider
	model    string
}

// NewWriter creates a new memory writer.
func NewWriter(store *Store) *Writer {
	return &Writer{store: store}
}

// SetGroupManager configures the group manager for group chat memory extraction.
func (w *Writer) SetGroupManager(gm *GroupManager) {
	w.groupManager = gm
}

// SetLLM configures the LLM provider for automatic memory extraction.
func (w *Writer) SetLLM(provider core.LLMProvider, model string) {
	w.provider = provider
	w.model = model
}

// RememberRoute records the current owner's transient routing scope.
func (w *Writer) RememberRoute(owner string, route core.RouteContext) {
	if w == nil {
		return
	}
	if w.store != nil {
		w.store.RememberRoute(owner, route)
	}
	if w.groupManager != nil {
		groupID := route.GroupID
		if groupID == "" {
			groupID = extractLegacyGroupID(MemoryEntry{Owner: owner})
		}
		if groupID != "" {
			if gStore, err := w.groupManager.GetGroupStoreForPlatform(route.Platform, groupID); err == nil && gStore != nil {
				gStore.RememberRoute(owner, route)
			}
		}
	}
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
	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
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
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
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

	if w == nil || w.store == nil {
		return nil
	}

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
	}

	var toSave []MemoryEntry
	for _, e := range entries {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if barrier != nil && !barrier.IsValid() {
			return errors.New("extraction cancelled or invalidated")
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
		toSave = append(toSave, entry)
	}

	if len(toSave) == 0 {
		return nil
	}

	commitValidator := func() bool {
		if ctx != nil && ctx.Err() != nil {
			return false
		}
		if barrier != nil && !barrier.IsValid() {
			return false
		}
		if validator != nil && !validator() {
			return false
		}
		if ctx != nil && ctx.Err() != nil {
			return false
		}
		if barrier != nil && !barrier.IsValid() {
			return false
		}
		return true
	}

	if err := w.store.SaveEntriesConditionallyContext(ctx, toSave, commitValidator); err != nil {
		if errors.Is(err, ErrConditionFailed) || (ctx != nil && ctx.Err() != nil) {
			return errors.New("extraction cancelled or invalidated")
		}
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("记忆保存失败: %v", err))
		if w.store != nil && w.store.sql != nil {
			return fmt.Errorf("%w: save private memories: %w", ErrStorageUnavailable, err)
		}
		return err
	}

	w.Log().Info(logs.SYSTEM, fmt.Sprintf("从对话中提取了 %d 条记忆 (owner: %s)", len(toSave), owner))
	return nil
}

// GroupMessage represents a message in a group dialogue for memory extraction.
type GroupMessage struct {
	MessageID string `json:"message_id,omitempty"`
	SenderID  string `json:"sender_id,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

type groupExtractedCandidate struct {
	Content        string   `json:"content,omitempty"`
	Evidence       string   `json:"evidence"`
	Summary        string   `json:"summary,omitempty"`
	Tags           []string `json:"tags"`
	SourceMsgIndex *int     `json:"source_msg_index,omitempty"`
	IsSelf         bool     `json:"is_self"`
}

type groupExtractedEntry = groupExtractedCandidate

// ExtractGroupMemories is the canonical group memory extraction, attribution, and persistence service.
// Both awakened turn completion and passive compact buffer distillation delegate here.
func (w *Writer) ExtractGroupMemories(
	ctx context.Context,
	groupID string,
	route core.RouteContext,
	messages []GroupMessage,
	source Source,
	validator func() bool,
) error {
	if ctx == nil {
		ctx = w.Context()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
	}
	if validator != nil && !validator() {
		return errors.New("extraction cancelled or invalidated")
	}
	if w.provider == nil || w.model == "" || w.groupManager == nil || groupID == "" || len(messages) == 0 {
		return nil
	}

	groupStore, err := w.groupManager.GetGroupStoreForPlatform(route.Platform, groupID)
	if err != nil {
		return fmt.Errorf("%w: load group store: %w", ErrStorageUnavailable, err)
	}

	existing, err := groupStore.ListAll()
	if err != nil {
		return fmt.Errorf("%w: read existing group memories: %w", ErrStorageUnavailable, err)
	}
	var existingMemoriesStr strings.Builder
	if len(existing) > 0 {
		existingMemoriesStr.WriteString("已有群记忆（严禁重复提取）：\n")
		limit := min(len(existing), 50)
		for i := range limit {
			fmt.Fprintf(&existingMemoriesStr, "- %s\n", existing[i].Content)
		}
	}

	var msgItems []map[string]any
	for i, m := range messages {
		name := m.Sender
		if name == "" {
			name = "群友"
		}
		item := map[string]any{
			"msg_index": i,
			"sender":    name,
			"role":      m.Role,
			"content":   m.Content,
		}
		if m.SenderID != "" {
			item["sender_id"] = m.SenderID
		}
		msgItems = append(msgItems, item)
	}
	convJSON, _ := json.MarshalIndent(msgItems, "", "  ")

	prompt := strings.Replace(extractGroupPrompt, "{conversation}", string(convJSON), 1)
	prompt = strings.Replace(prompt, "{current_time}", CurrentTimeLabel(time.Now()), 1)
	prompt = strings.Replace(prompt, "{existing_memories}", existingMemoriesStr.String(), 1)

	req := core.ChatRequest{
		Model: w.model,
		Messages: []core.ChatMessage{
			{Role: core.RoleUser, Content: prompt},
		},
		MaxTokens:   1024,
		Temperature: 0.2,
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
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("群聊记忆提取LLM调用失败 (群 %s): %v", groupID, err))
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
	}
	if validator != nil && !validator() {
		return errors.New("extraction cancelled or invalidated")
	}

	raw, ok := resp.Message.Content.(string)
	if !ok {
		return fmt.Errorf("unexpected response type: %T", resp.Message.Content)
	}

	return w.parseAndSaveGroupCandidates(ctx, groupStore, groupID, messages, raw, source, validator)
}

func (w *Writer) parseAndSaveGroupCandidates(
	ctx context.Context,
	groupStore *GroupStore,
	groupID string,
	messages []GroupMessage,
	raw string,
	source Source,
	validator func() bool,
) error {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if raw == "" || raw == "[]" {
		return nil
	}

	var candidates []groupExtractedCandidate
	if err := json.Unmarshal([]byte(raw), &candidates); err != nil {
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("群聊记忆提取JSON解析失败: %v, raw: %s", err, raw))
		return err
	}

	barrier := core.ExtractionBarrierFromContext(ctx)
	if barrier != nil && !barrier.IsValid() {
		return errors.New("extraction cancelled or invalidated")
	}

	now := time.Now()
	var toSave []MemoryEntry
	for _, cand := range candidates {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if barrier != nil && !barrier.IsValid() {
			return errors.New("extraction cancelled or invalidated")
		}
		if validator != nil && !validator() {
			return errors.New("extraction cancelled or invalidated")
		}

		rawEvidence := strings.TrimSpace(cand.Evidence)
		if rawEvidence == "" {
			continue
		}

		// 1. Locate source message: require an explicit, valid, and unambiguous source message index.
		// Reject candidates with missing, out-of-bounds, or assistant-role indices.
		if cand.SourceMsgIndex == nil {
			continue
		}
		idx := *cand.SourceMsgIndex
		if idx < 0 || idx >= len(messages) {
			continue
		}
		candidateMsg := messages[idx]
		if candidateMsg.Role == "assistant" || candidateMsg.Role == string(core.RoleAssistant) {
			continue
		}

		validEvidence, ok := ValidateEvidence(candidateMsg.Content, rawEvidence)
		if !ok {
			continue
		}
		srcMsg := &candidateMsg

		// 2. Attribution:
		// Conservatively group-own ambiguous cases. Personal ownership (Owner = srcMsg.SenderID)
		// is granted only when cand.IsSelf is true, SenderID is non-empty, and the quoted evidence
		// contains a direct first-person self-reference (HasSelfReference).
		// Otherwise, Owner defaults to GroupOwnerExplicit ("group").
		ownerKey := GroupOwnerExplicit
		if cand.IsSelf && srcMsg.SenderID != "" && HasSelfReference(validEvidence) {
			ownerKey = srcMsg.SenderID
		}

		// 3. Option A Data Contract:
		// Authoritative Content is strictly the verbatim source quote (validEvidence).
		// Summary is the optional display description (cand.Summary or cand.Content if different).
		displaySummary := SanitizeSummary(cand.Summary)
		if displaySummary == "" && cand.Content != "" && cand.Content != validEvidence {
			displaySummary = SanitizeSummary(cand.Content)
		}

		effectiveSource := source
		if effectiveSource == "" {
			effectiveSource = SourceExtract
		}

		toSave = append(toSave, MemoryEntry{
			ID:              generateID(),
			Owner:           ownerKey,
			OwnerType:       OwnerGroup,
			ScopeType:       ScopeGroup,
			GroupID:         groupID,
			Content:         validEvidence,
			Summary:         displaySummary,
			Evidence:        validEvidence,
			SourceMessageID: srcMsg.MessageID,
			SourceSenderID:  srcMsg.SenderID,
			Tags:            SanitizeTags(cand.Tags),
			Source:          effectiveSource,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	if len(toSave) == 0 {
		return nil
	}

	if err := groupStore.SaveGroupEntriesConditionallyContext(ctx, toSave, validator); err != nil {
		if errors.Is(err, ErrConditionFailed) || (ctx != nil && ctx.Err() != nil) {
			return errors.New("extraction cancelled or invalidated")
		}
		w.Log().Error(logs.SYSTEM, fmt.Sprintf("群聊记忆保存失败 (群 %s): %v", groupID, err))
		return fmt.Errorf("%w: save group memories: %w", ErrStorageUnavailable, err)
	}

	w.Log().Info(logs.SYSTEM, fmt.Sprintf("从群聊中提取了 %d 条记忆 (群: %s)", len(toSave), groupID))
	return nil
}

// ExtractGroupTurnWithRouteContext extracts group memories from a turn with context and validator.
// It adapts legacy ChatMessage arguments to GroupMessage and delegates to ExtractGroupMemories.
func (w *Writer) ExtractGroupTurnWithRouteContext(
	ctx context.Context,
	groupID string,
	speakerID string,
	speakerName string,
	route core.RouteContext,
	messages []core.ChatMessage,
	validator func() bool,
) error {
	var groupMsgs []GroupMessage
	for _, m := range messages {
		sID := ""
		sName := ""
		if m.Role == core.RoleUser {
			sID = speakerID
			sName = speakerName
		}
		groupMsgs = append(groupMsgs, GroupMessage{
			SenderID: sID,
			Sender:   sName,
			Role:     string(m.Role),
			Content:  fmt.Sprintf("%v", m.Content),
		})
	}
	return w.ExtractGroupMemories(ctx, groupID, route, groupMsgs, SourceExtract, validator)
}

// GenerateID creates a random hex ID prefixed with "mem_".
func GenerateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "mem_" + hex.EncodeToString(b)
}

func generateID() string {
	return GenerateID()
}
