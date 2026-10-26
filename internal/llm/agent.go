package llm

import (
	"context"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/actionscat"
	"github.com/GuaiZai233/FrostAgent/internal/billing"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/groupsummary"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/mcp"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/poke"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sandbox"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"github.com/GuaiZai233/FrostAgent/internal/skills"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

type ToolExecutor interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(args string) (string, error)
}

const (
	StaySilentToolName    = "stay_silent"
	AssistantSilentMarker = "<assistant_silent />"

	// DefaultMaxIterations is the fallback loop iteration limit for LLM execution.
	DefaultMaxIterations = 35

	MaxSingleInputTokens = 32000
	MaxContextTokens     = 128000
	MaxToolOutputBytes   = 65536 // 64KB
)

// ErrMaxIterationsReached indicates the agent exhausted its allowed iterations without a final response.
var ErrMaxIterationsReached = errors.New("达到最大迭代次数，未能得出最终答案")

// IsStaySilentTool reports whether the tool name matches stay_silent.
func IsStaySilentTool(name string) bool {
	return name == StaySilentToolName
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// AgentRunResult carries the final content and side-effect decisions from one
// agent loop. Silent is true when the model successfully invokes the terminal
// stay_silent tool or returns the standalone internal silence marker. Provider
// failures never set it. Banned is true when the turn was terminated by an
// autonomous ban action (ban_user tool). OutputBlocked is true when the final
// answer was blocked or failed-closed by the security watchdog.
type AgentRunResult struct {
	Content          string
	MemoryWritten    bool
	Silent           bool
	StaySilentCalled bool
	SilenceReason    string // "stay_silent", "security_block", "route_disabled", "canceled", "epoch_changed", "provider_fallback"
	Banned           bool
	OutputBlocked    bool
	Usage            core.Usage
	Error            error
	LLMFailed        bool          // Dialogue route resolution or provider call failed; excludes security, billing, and tool errors.
	Transcript       []ChatMessage // Request-private checkpoint; never committed as public history.
}

// IsProactiveAllowedTool reports whether a tool is safe to invoke during an unaddressed proactive turn.
// Proactive turns allow terminal silence and memory. Sticker delivery and
// other external or privileged tools remain disallowed.
func IsProactiveAllowedTool(name string) bool {
	switch name {
	case StaySilentToolName:
		return true
	case "memory":
		return true
	default:
		return false
	}
}

func (e *Engine) isToolAllowedForProactive(name string) bool {
	// The sticker ban cannot be overridden by a tool's proactive opt-in.
	if name == "send_sticker" {
		return false
	}
	if IsProactiveAllowedTool(name) {
		return true
	}
	if tool, exists := e.findToolExecutor(name); exists {
		if pa, ok := tool.(interface{ IsProactiveAllowed() bool }); ok {
			return pa.IsProactiveAllowed()
		}
	}
	return false
}

// Engine 结构体，用于管理智能体的执行
type Engine struct {
	PokeGate poke.Gate
	*runtimescope.Scope
	MaxIterations          int
	ToolRegistry           map[string]ToolExecutor
	Provider               core.LLMProvider // LLM 供应商接口
	BaseURL                string           // API 地址
	APIKey                 string           // API 密钥
	ModelName              string           // 模型名称
	ModelRouter            *modelrouter.Manager
	VisionProvider         core.LLMProvider
	SessionManager         *SessionManager // 会话上下文管理器
	Dispatcher             core.MessageDispatcher
	StartedAt              time.Time // 引擎启动时间
	Version                string    // 版本号
	TotalMessagesProcessed atomic.Int64

	// Billing integration (optional, nil = billing disabled)
	BillingClient *billing.Client
	BillingConfig billing.Config

	// Memory components (optional, nil = memory disabled)
	MemoryEmbeddings  *memory.Embeddings
	MemoryReader      *memory.Reader
	MemoryWriter      *memory.Writer
	MemoryGateway     *memory.Gateway
	MemoryCatalog     *memory.CatalogStore
	MemoryReflections *memory.ReflectionManager
	GroupManager      *memory.GroupManager
	GroupCompactor    *GroupCompactor
	GroupSummaryStore *groupsummary.Store

	// DialoguePrompt carries formatted few-shot persona examples (optional)
	DialoguePrompt string
	dialogueMu     sync.RWMutex

	// MCP Manager (optional, nil = MCP disabled)
	MCPManager *mcp.Manager
	Skills     *skills.Store

	// Sandbox integration (optional, nil = sandbox disabled)
	SandboxBackend sandbox.Backend

	// Security is shared by every runtime owned by the Control Plane.
	Security        *security.Controller
	InstanceID      string
	stickerActivity stickerActivityState
}

// DropRejectedGroupMessage keeps a failed SQL purge from being ignored by the
// adapter. Canceling the runtime scope stops new work until it is rebuilt.
func (e *Engine) DropRejectedGroupMessage(session *SessionContext, guard *StagedGroupCompactGuard, messageID, senderID string) {
	if session == nil {
		return
	}
	var err error
	if guard != nil {
		_, err = guard.DropChecked()
	} else {
		_, err = session.DropGroupCompactMessageChecked(messageID, senderID)
	}
	e.PauseOnStorageError(err)
}

func (e *Engine) PauseOnStorageError(err error) {
	if err == nil {
		return
	}
	e.Log().Error(logs.SYSTEM, fmt.Sprintf("数据库状态更新失败，实例已暂停: %v", err))
	if e.Scope != nil {
		e.Scope.Cancel()
	}
}

func (e *Engine) memoryFailure(operation string, err error) AgentRunResult {
	wrapped := fmt.Errorf("%s: %w", operation, err)
	if e.Scope != nil && e.Scope.Config != nil && e.Scope.Config.IsDatabase() {
		e.PauseOnStorageError(wrapped)
	}
	return AgentRunResult{Error: wrapped}
}

// EffectiveMaxIterations resolves the effective iteration limit captured
// on Engine.MaxIterations, falling back to DefaultMaxIterations (35) if unset.
// It preserves restart-required semantics so running engines do not mutate
// mid-flight before an instance restart.
func (e *Engine) EffectiveMaxIterations() int {
	if e != nil && e.MaxIterations > 0 {
		return e.MaxIterations
	}
	return DefaultMaxIterations
}

// Run 执行智能体的主循环（单次无状态调用）
func (e *Engine) Run(prompt string) string {
	systemPrompt := e.Getenv("SYSTEM_PROMPT")
	if e.PersonaDialogue() != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n" + e.PersonaDialogue()
		} else {
			systemPrompt = e.PersonaDialogue()
		}
	}
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}
	result := e.runLoop(e.newRoutingContext(), messages)
	return result
}

// RunMessages 执行智能体的主循环（直接传入消息数组）
func (e *Engine) RunMessages(messages []ChatMessage) string {
	// 如果消息数组中没有 system 提示词，添加一个
	if len(messages) == 0 || messages[0].Role != "system" {
		systemPrompt := e.Getenv("SYSTEM_PROMPT")
		if e.PersonaDialogue() != "" {
			if systemPrompt != "" {
				systemPrompt += "\n\n" + e.PersonaDialogue()
			} else {
				systemPrompt = e.PersonaDialogue()
			}
		}
		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}
	return e.runLoop(e.newRoutingContext(), messages)
}

func (e *Engine) newRoutingContext() context.Context {
	ctx := runtimescope.WithContext(e.Context(), e.Scope)
	if e.ModelRouter == nil {
		return ctx
	}
	snapshot := e.ModelRouter.Snapshot()
	ctx = e.ModelRouter.WithSnapshot(ctx, snapshot)
	return withRunContext(ctx, RunContext{RouteSnapshot: snapshot})
}

// RunMessagesWithUser 执行智能体的主循环（带记忆上下文）。
// owner 是私聊 userID 或 group:<群号>；传空则跳过记忆。
func (e *Engine) RunMessagesWithUser(
	messages []ChatMessage,
	owner string,
	ownerType memory.OwnerType,
) AgentRunResult {
	return e.RunMessagesWithContext(messages, RunContext{
		Owner:     owner,
		OwnerType: ownerType,
	})
}

// buildContextSystemPrompt keeps stable instructions before request-local data
// so minute-level time changes do not invalidate their prompt-cache prefix.
func buildContextSystemPrompt(systemPrompt, dialoguePrompt string, now time.Time) string {
	var parts []string
	if systemPrompt != "" {
		parts = append(parts, systemPrompt)
	}
	if dialoguePrompt != "" {
		parts = append(parts, dialoguePrompt)
	}
	// Keep the current time available for relative dates (today/tomorrow/this week).
	parts = append(parts, "当前系统时间："+memory.CurrentTimeLabel(now))
	return strings.Join(parts, "\n\n")
}

// RunMessagesWithContext runs one agent request with owner and transport hooks
// scoped to that request instead of mutable Engine fields.
func (e *Engine) RunMessagesWithContext(
	messages []ChatMessage,
	runContext RunContext,
) AgentRunResult {
	if runContext.Turn != nil {
		runContext.SkillState = runContext.Turn.SkillSession()
	}
	if !runContext.Mock {
		e.RecordBotTurn()
	}
	if err := e.securityAccess(runContext); err != nil {
		return AgentRunResult{Silent: true, SilenceReason: "security_block", Error: err}
	}
	owner := runContext.Owner
	if owner != "" && e.MemoryWriter != nil {
		e.MemoryWriter.RememberRoute(owner, core.RouteContext{
			Platform: runContext.RouteScope.Platform,
			GroupID:  runContext.RouteScope.GroupID,
		})
	}
	ctx := e.Context()
	if runContext.Context != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(runContext.Context, cancel)
		defer stop()
	}
	if runContext.Turn != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(runContext.Turn.Context(), cancel)
		defer stop()
		if runContext.Turn.Interrupted() {
			cancel()
		}
	}
	if e.ModelRouter != nil {
		if runContext.RouteSnapshot == nil {
			runContext.RouteSnapshot = e.ModelRouter.Snapshot()
		}
		ctx = e.ModelRouter.WithSnapshot(ctx, runContext.RouteSnapshot)
	}
	ctx = withRunContext(ctx, runContext)

	if len(messages) == 0 || messages[0].Role != "system" {
		systemPrompt := buildContextSystemPrompt(e.Getenv("SYSTEM_PROMPT"), e.PersonaDialogue(), time.Now())

		isGroupChat := runContext.RouteScope.GroupID != "" || runContext.OwnerType == memory.OwnerGroup || strings.HasPrefix(owner, "group:")
		if isGroupChat {
			groupID := runContext.RouteScope.GroupID
			if groupID == "" && strings.HasPrefix(owner, "group:") {
				groupID = strings.TrimPrefix(owner, "group:")
			}
			senderID := runContext.ActorUserID

			if e.GroupManager != nil && groupID != "" {
				groupStore, gErr := e.GroupManager.GetGroupStoreForPlatform(runContext.RouteScope.Platform, groupID)
				if gErr != nil {
					return e.memoryFailure("open group memory", gErr)
				}
				if groupStore != nil {
					profile, pErr := groupStore.GetProfile()
					if pErr != nil {
						return e.memoryFailure("read group profile", pErr)
					}
					var senderProfile *memory.MemberProfile
					if profile != nil {
						senderProfile = profile.GetMember(senderID)
						if memberPrompt := memory.MemberContextPrompt(senderProfile); memberPrompt != "" {
							systemPrompt += "\n\n" + memberPrompt
						}
					}

					if groupStore.CatalogStore() != nil {
						catalogContext, cErr := groupStore.CatalogStore().FormatForGroupPrompt(groupID)
						if cErr != nil {
							return e.memoryFailure("read group memory catalog", cErr)
						} else if catalogContext != "" {
							systemPrompt += "\n\n" + catalogContext
						}
					}

					if e.MemoryGateway != nil {
						lastUserMsg := extractLastUserMessage(messages)
						var raw []memory.MemoryEntry
						var sErr error
						if e.MemoryEmbeddings != nil {
							raw, sErr = e.MemoryEmbeddings.Search(ctx, memory.SearchScope{Type: memory.ScopeGroup, Platform: runContext.RouteScope.Platform, GroupID: groupID}, lastUserMsg, 20)
						} else {
							raw, sErr = groupStore.Search(lastUserMsg, 0)
						}
						if sErr != nil {
							if e.MemoryEmbeddings != nil {
								e.Log().Warn(logs.SYSTEM, fmt.Sprintf("群记忆向量召回失败，继续对话: %v", sErr))
								raw = nil
							} else {
								return e.memoryFailure("search group memory", sErr)
							}
						}
						if len(raw) > 0 {
							filtered := e.MemoryGateway.FilterGroup(raw)
							if len(filtered) > 20 {
								filtered = filtered[:20]
							}
							if len(filtered) > 0 {
								groupMemContext := e.MemoryGateway.FormatForGroupContext(filtered, groupID, senderID, senderProfile)
								systemPrompt += "\n\n" + groupMemContext
								if !runContext.Mock {
									if err := groupStore.RecordRecall(filtered); err != nil {
										return e.memoryFailure("record group memory recall", err)
									}
								}
							}
						}
					}
				}
			}
		} else {
			if owner != "" && e.MemoryCatalog != nil {
				catalogContext, err := e.MemoryCatalog.FormatForPrompt(owner)
				if err != nil {
					return e.memoryFailure("read memory catalog", err)
				} else if catalogContext != "" {
					systemPrompt += "\n\n" + catalogContext
				}
			}

			// 召回 → 网关过滤 → 注入
			if owner != "" && e.MemoryReader != nil && e.MemoryGateway != nil {
				lastUserMsg := extractLastUserMessage(messages)
				var raw []memory.MemoryEntry
				var err error
				if e.MemoryEmbeddings != nil {
					raw, err = e.MemoryEmbeddings.Search(ctx, memory.SearchScope{Type: memory.ScopePrivate, Owner: owner}, lastUserMsg, 20)
				} else {
					raw, err = e.MemoryReader.Recall(ctx, lastUserMsg)
				}
				if err != nil {
					if e.MemoryEmbeddings != nil {
						e.Log().Warn(logs.SYSTEM, fmt.Sprintf("记忆向量召回失败，继续对话: %v", err))
						raw = nil
					} else {
						return e.memoryFailure("recall memory", err)
					}
				}
				filtered := e.MemoryGateway.FilterPrivate(raw, owner)
				filtered = e.MemoryReader.Limit(filtered)
				if len(filtered) > 0 {
					memoryContext := e.MemoryGateway.FormatForPrivateContext(filtered, owner)
					systemPrompt += "\n\n" + memoryContext
					if !runContext.Mock {
						if err := e.MemoryReader.RecordRecall(filtered); err != nil {
							return e.memoryFailure("record memory recall", err)
						}
					}
				}
			}
		}

		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}

	// Persist the assembled system prompt so Session Context Inspector can show
	// the real dynamically-built prompt (instructions + dialogue + time + memory) rather than a
	// static reconstruction. SessionID is intentionally separate from the memory
	// owner namespace (for example private:123 versus owner 123).
	sessionID := strings.TrimSpace(runContext.SessionID)
	if sessionID == "" {
		// Backward compatibility for direct callers whose owner is also the
		// session key. Adapters should always provide SessionID explicitly.
		sessionID = owner
	}
	if sessionID != "" && e.SessionManager != nil {
		if sessCore, ok := e.SessionManager.Get(sessionID); ok {
			if sess, isSess := sessCore.(*SessionContext); isSess {
				if sysContent, ok := messages[0].Content.(string); ok {
					modelName := e.ModelName
					if e.ModelRouter != nil {
						if target, err := runContext.RouteSnapshot.Resolve(modelrouter.WorkloadDialogue, runContext.RouteScope); err == nil {
							modelName = target.UpstreamModel
						}
					}
					sess.SetLastPromptTrace(sysContent, modelName)
				}
				var cancelRun func()
				var currentEpoch uint64
				if runContext.Turn == nil {
					ctx, currentEpoch, cancelRun = sess.BeginRun(ctx)
					defer cancelRun()
				} else {
					currentEpoch = sess.Epoch()
				}
				if runContext.Epoch == 0 {
					runContext.Epoch = currentEpoch
				}
				ctx = withRunContext(ctx, runContext)
			}
		}
	}

	if !runContext.Mock && !runContext.Proactive {
		if notice := e.stickerActivityPrompt(); notice != "" {
			messages = append([]ChatMessage(nil), messages...)
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "user" {
					if content, ok := messages[i].Content.(string); ok {
						messages[i].Content = notice + "\n\n" + content
					}
					break
				}
			}
		}
	}
	return e.runLoopWithResult(ctx, messages)
}

// EnqueueExtractionTurn buffers one completed turn. Extraction runs only when
// the session-specific random threshold is reached.
func (e *Engine) EnqueueExtractionTurn(
	session *SessionContext,
	items []memory.PendingExtractionItem,
) {
	if e == nil || e.MemoryWriter == nil || session == nil || len(items) == 0 {
		return
	}
	minTurns := positiveIntEnv("MEMORY_EXTRACT_BATCH_MIN", 3, e.Scope)
	maxTurns := positiveIntEnv("MEMORY_EXTRACT_BATCH_MAX", 5, e.Scope)
	maxTurns = max(maxTurns, minTurns)
	batch, ready := session.EnqueuePendingTurn(items, minTurns, maxTurns)
	if !ready {
		return
	}
	e.Go(func() { e.extractPendingBatch(batch) })
}

func (e *Engine) extractPendingBatch(batch PendingExtractionBatch) {
	if batch.Session == nil || len(batch.Items) == 0 {
		return
	}
	ctx, _, cleanup := batch.Session.BeginExtraction(e.Context())
	defer cleanup()

	validator := func() bool {
		return ctx.Err() == nil && batch.Session.Epoch() == batch.Epoch
	}

	if !validator() {
		return
	}

	type itemBatch struct {
		isGroup       bool
		groupID       string
		speakerID     string
		speakerName   string
		owner         string
		ownerType     memory.OwnerType
		route         core.RouteContext
		messages      []core.ChatMessage
		groupMessages []memory.GroupMessage
	}
	groups := make(map[string]*itemBatch)
	order := make([]string, 0)
	for _, item := range batch.Items {
		if item.ScopeType == memory.ScopeGroup || item.GroupID != "" {
			groupID := item.GroupID
			if groupID == "" && strings.HasPrefix(item.Owner, "group:") {
				groupID = strings.TrimPrefix(item.Owner, "group:")
			}
			key := "group\x00" + groupID + "\x00" + item.SpeakerID + "\x00" + item.Route.Platform
			group := groups[key]
			if group == nil {
				group = &itemBatch{
					isGroup:     true,
					groupID:     groupID,
					speakerID:   item.SpeakerID,
					speakerName: item.SpeakerName,
					route:       item.Route,
				}
				groups[key] = group
				order = append(order, key)
			}
			group.messages = append(group.messages, item.Message)
			sID := ""
			sName := ""
			if item.Message.Role == core.RoleUser {
				sID = item.SpeakerID
				sName = item.SpeakerName
			}
			group.groupMessages = append(group.groupMessages, memory.GroupMessage{
				MessageID:        item.MessageID,
				SenderID:         sID,
				Sender:           sName,
				Role:             string(item.Message.Role),
				Content:          fmt.Sprintf("%v", item.Message.Content),
				ImageDescription: item.ImageDescription,
			})
		} else {
			if item.Owner == "" {
				continue
			}
			key := "private\x00" + string(item.OwnerType) + "\x00" + item.Owner + "\x00" + item.Route.Platform + "\x00" + item.Route.GroupID
			group := groups[key]
			if group == nil {
				group = &itemBatch{
					isGroup:   false,
					owner:     item.Owner,
					ownerType: memory.NormalizeOwnerType(item.OwnerType),
					route:     item.Route,
				}
				groups[key] = group
				order = append(order, key)
			}
			group.messages = append(group.messages, item.Message)
		}
	}
	for _, key := range order {
		if !validator() {
			return
		}
		group := groups[key]
		if group.isGroup {
			if err := e.MemoryWriter.ExtractGroupMemories(ctx, group.groupID, group.route, group.groupMessages, memory.SourceExtract, validator); err != nil {
				if errors.Is(err, context.Canceled) || !validator() {
					return
				}
				if errors.Is(err, memory.ErrStorageUnavailable) {
					e.PauseOnStorageError(err)
					return
				}
				e.Log().Warn(logs.SYSTEM, fmt.Sprintf("群聊批量提取记忆失败 (群: %s): %v", group.groupID, err))
			}
		} else {
			if err := e.MemoryWriter.ExtractByOwnerWithRouteContext(ctx, group.owner, group.ownerType, group.route, group.messages, validator); err != nil {
				if errors.Is(err, context.Canceled) || !validator() {
					return
				}
				if errors.Is(err, memory.ErrStorageUnavailable) {
					e.PauseOnStorageError(err)
					return
				}
				e.Log().Warn(logs.SYSTEM, fmt.Sprintf("批量提取记忆失败 (owner: %s): %v", group.owner, err))
			}
		}
	}
}

func positiveIntEnv(name string, fallback int, scopes ...*runtimescope.Scope) int {
	scope := runtimescope.First(scopes)
	raw := strings.TrimSpace(scope.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// extractLastUserMessage 从消息数组中提取最后一条用户消息的内容。
func extractLastUserMessage(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			if s, ok := messages[i].Content.(string); ok {
				return s
			}
		}
	}
	return ""
}

// Deprecated: automatic extraction now uses per-session pending turns.
// buildExtractionContext remains for compatibility with existing callers and
// tests that validate the previous two-turn extraction window.
//
// buildExtractionContext returns the recent user/assistant text exchanges
// that the background extractor needs to detect implicit corrections and
// updates to previously saved memories. It anchors on the agent's final
// reply for the current turn and walks backward through the conversation,
// collecting up to two user messages and any assistant text replies
// between them so the LLM can see both the prior claim and the correction.
//
// System, tool, and tool-call assistant messages (no text content) are
// filtered out — the extractor only needs the conversation narrative.
// The window is intentionally small: it preserves the 9ad77fe guarantee
// that older session history is not re-extracted, while giving the
// extractor enough context to recognise when the user is correcting
// a previous fact.
func buildExtractionContext(messages []ChatMessage, finalReply string) []core.ChatMessage {
	if finalReply == "" {
		return nil
	}
	type pick struct {
		role core.MessageRole
		body any
	}
	var picked []pick
	// Anchor with the agent's final reply for the current turn.
	picked = append(picked, pick{core.RoleAssistant, finalReply})
	// Walk backward, collect up to 2 user messages plus the assistant text
	// replies that follow them. Assistant messages with empty or non-string
	// content (tool-call turns) are skipped.
	userCount := 0
	for i := len(messages) - 1; i >= 0 && userCount < 2; i-- {
		m := messages[i]
		switch m.Role {
		case "user":
			picked = append(picked, pick{core.RoleUser, m.Content})
			userCount++
		case "assistant":
			if s, ok := m.Content.(string); ok && s != "" {
				picked = append(picked, pick{core.RoleAssistant, s})
			}
		}
	}
	// Reverse to chronological order.
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}
	out := make([]core.ChatMessage, len(picked))
	for i, p := range picked {
		out[i] = core.ChatMessage{Role: p.role, Content: p.body}
	}
	return out
}

// RunWithSession 执行智能体的主循环（带会话上下文）
func (e *Engine) RunWithSession(sessionID string, prompt string) string {
	session := e.SessionManager.GetOrCreate(sessionID)

	// 加锁保护会话内部状态
	session.Lock()
	defer session.Unlock()

	// get history msg
	messages := session.History

	// if new session, add system prompt
	if len(messages) == 0 {
		systemPrompt := e.Getenv("SYSTEM_PROMPT")
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	// add user input
	messages = append(messages, ChatMessage{Role: "user", Content: prompt})

	result := e.runLoop(e.Context(), messages)

	// 修改后的 messages 写回
	session.History = e.trimMessagesForSession(messages)
	session.UpdatedAt = time.Now()

	return result
}

// runLoop 核心循环逻辑
func (e *Engine) runLoop(ctx context.Context, messages []ChatMessage) string {
	return e.runLoopWithResult(ctx, messages).Content
}

// runLoopWithResult executes the agent loop and reports terminal decisions and
// memory side effects. A stay_silent call must be the only tool call in its
// assistant response; conflicts are returned to the model for another choice.
func (e *Engine) runLoopWithResult(ctx context.Context, messages []ChatMessage) (result AgentRunResult) {
	memoryWritten := false
	var totalUsage core.Usage

	runCtx, hasRunCtx := RunContextFromContext(ctx)
	billingActive := hasRunCtx && !runCtx.Mock && runCtx.Billing != nil && runCtx.Billing.BillingActive && e.BillingClient != nil && e.BillingConfig.Enabled
	startedTools := make(map[string]bool)
	defer func() {
		result.MemoryWritten = runCtx.Turn.memoryState(result.MemoryWritten || memoryWritten)
		if runCtx.Turn != nil {
			result.Transcript = completeInterruptedTranscript(messages, startedTools)
			runCtx.Turn.saveTranscript(result.Transcript)
		}
		if billingActive {
			if e.waitBilling(ctx, runCtx.Billing) {
				result.Usage = runCtx.Billing.Usage()
			}
		}
		if runCanceled(ctx) {
			result.Content = ""
			result.Silent = true
			result.SilenceReason = "canceled"
			result.Error = context.Canceled
			result.LLMFailed = false
		}
	}()
	memoryWritten = runCtx.Turn.memoryState(false)
	modelName := e.ModelName
	if e.ModelRouter != nil {
		var snapshot *modelrouter.Snapshot
		if hasRunCtx {
			snapshot = runCtx.RouteSnapshot
		}
		if snapshot == nil {
			snapshot = e.ModelRouter.Snapshot()
		}
		target, err := snapshot.Resolve(modelrouter.WorkloadDialogue, runCtx.RouteScope)
		if err != nil {
			if errors.Is(err, modelrouter.ErrDisabled) {
				return AgentRunResult{Silent: true, SilenceReason: "route_disabled"}
			}
			return AgentRunResult{
				Content:   fmt.Sprintf("FrostAgent 错误：LLM 响应失败：%s", truncateRunes(err.Error(), 100)),
				Error:     err,
				LLMFailed: true,
			}
		}
		modelName = target.UpstreamModel
	}

	if err := e.activateRequestedSkill(ctx, messages); err != nil {
		return AgentRunResult{Content: err.Error(), Error: err}
	}

	// 主循环
	maxIterations := e.EffectiveMaxIterations()
	baseMaxIterations := maxIterations
	addStickerIteration := func() {
		if maxIterations-baseMaxIterations < 4 {
			maxIterations++
		}
	}
	stickerEnforcing := false
	stickerAttempts := 0
	enforceStickerActivity := hasRunCtx && !runCtx.Mock && !runCtx.Proactive
	if interval, remaining, grace := e.stickerActivitySnapshot(); enforceStickerActivity && interval > 0 && remaining == 0 && grace == 0 {
		stickerEnforcing = true
		messages = append(messages, ChatMessage{Role: "user", Content: stickerEnforcementPrompt})
	}
	for i := 0; i < maxIterations; i++ {
		if runCanceled(ctx) {
			return AgentRunResult{Silent: true, Error: context.Canceled}
		}
		if err := e.securityAccess(runCtx); err != nil {
			return AgentRunResult{Silent: true, SilenceReason: "security_block", Error: err, Usage: totalUsage}
		}
		if runCanceled(ctx) {
			return AgentRunResult{Silent: true, SilenceReason: "canceled", Error: context.Canceled}
		}
		if hasRunCtx && runCtx.SessionID != "" && runCtx.Epoch > 0 && e.SessionManager != nil {
			if sessCore, ok := e.SessionManager.Get(runCtx.SessionID); ok {
				if sess, isSess := sessCore.(*SessionContext); isSess {
					if sess.Epoch() != runCtx.Epoch {
						return AgentRunResult{Silent: true, SilenceReason: "epoch_changed", Error: errors.New("session epoch invalidated")}
					}
				}
			}
		}
		e.TotalMessagesProcessed.Add(1)
		iterationSummary := fmt.Sprintf("【第%d轮思考开始】", i+1)
		e.Log().InfoWithConsoleSummary(logs.SYSTEM, iterationSummary, iterationSummary)

		// 每一轮刷新当前有效工具集合（包含运行时热开关的 MCP 工具）
		if e.Skills != nil {
			skillMessages, skillErr := e.skillMessages(ctx, messages)
			if skillErr != nil {
				return AgentRunResult{Error: skillErr, Content: "技能上下文读取失败", Usage: totalUsage}
			}
			messages = skillMessages
			if sess := e.promptTraceSession(ctx); sess != nil {
				var system []string
				for _, message := range messages {
					if message.Role == "system" {
						if content, ok := message.Content.(string); ok {
							system = append(system, content)
						}
					}
				}
				sess.SetLastPromptTrace(strings.Join(system, "\n\n"), modelName)
			}
		}
		modelTools := e.EffectiveTools()
		if hasRunCtx && runCtx.Proactive {
			var proactiveTools []core.Tool
			for _, t := range modelTools {
				if e.isToolAllowedForProactive(t.Name) {
					proactiveTools = append(proactiveTools, t)
				}
			}
			modelTools = proactiveTools
		}

		coreMsgs := convertToCoreMessages(messages)
		if hasRunCtx && runCtx.SecurityNotice != "" {
			noticeMsg := core.ChatMessage{
				Role:    core.RoleSystem,
				Content: runCtx.SecurityNotice,
			}
			if len(coreMsgs) > 0 && coreMsgs[0].Role == core.RoleSystem {
				coreMsgs = append([]core.ChatMessage{coreMsgs[0], noticeMsg}, coreMsgs[1:]...)
			} else {
				coreMsgs = append([]core.ChatMessage{noticeMsg}, coreMsgs...)
			}
		}

		// 检查上下文总 Token 是否超出模型硬上限 (包含 tools 定义开销)
		contextTokens, err := billing.EstimateMessagesTokens(coreMsgs, modelTools)
		if err != nil {
			contextTokens = billing.EstimateTokens(coreMsgs)
		}
		if contextTokens > MaxContextTokens {
			content := fmt.Sprintf("上下文长度 (%d tokens) 超出硬上限 (%d tokens)", contextTokens, MaxContextTokens)
			e.Log().WarnWithConsoleSummary(logs.SYSTEM, content, "上下文长度超出硬上限")
			return AgentRunResult{
				Content:       "FrostAgent错误：对话上下文过长，超出模型处理上限。",
				MemoryWritten: memoryWritten,
				Usage:         totalUsage,
				Error:         fmt.Errorf("context tokens %d exceeded limit %d", contextTokens, MaxContextTokens),
			}
		}

		var reservationID string
		if billingActive {
			amountMinor, err := billing.EstimateReservationAmount(
				modelName,
				coreMsgs,
				modelTools,
				e.BillingConfig.MaxOutputTokens,
				e.BillingConfig.SafetyMultiplier,
				e.BillingConfig.Price(modelName),
			)
			if err != nil {
				e.Log().Error(logs.SYSTEM, fmt.Sprintf("计费预估失败 (fail-closed, iter %d): %v", i+1, err))
				return AgentRunResult{
					Content:       billing.FormatBillingUnavailableMessage(),
					MemoryWritten: memoryWritten,
					Usage:         totalUsage,
					Error:         err,
				}
			}
			if amountMinor < 1 {
				amountMinor = 1
			}

			platform := strings.TrimSpace(runCtx.Billing.Platform)
			if platform == "" {
				platform = "qq"
			}
			taskID := strings.TrimSpace(runCtx.Billing.TaskID)
			if taskID == "" {
				if runCtx.Billing.ExternalID != "" {
					taskID = fmt.Sprintf("%s_%s_%d", platform, runCtx.Billing.ExternalID, time.Now().UnixNano())
				} else {
					taskID = fmt.Sprintf("%s_anon_%d", platform, time.Now().UnixNano())
				}
			}
			callIndex := runCtx.Billing.NextCall()
			callID := fmt.Sprintf("call_%s_%d", taskID, callIndex)
			idempotencyKey := fmt.Sprintf("res_%s_%s_%d", platform, taskID, callIndex)

			reserveReq := billing.LLMReserveRequest{
				Platform:       platform,
				ExternalID:     runCtx.Billing.ExternalID,
				DisplayName:    runCtx.Billing.DisplayName,
				TaskID:         taskID,
				CallID:         callID,
				AmountMinor:    amountMinor,
				IdempotencyKey: idempotencyKey,
			}

			bCtx, bCancel := context.WithTimeout(e.Context(), e.BillingConfig.Timeout)
			res, err := e.BillingClient.ReserveLLM(bCtx, reserveReq)
			bCancel()

			if err != nil {
				if errors.Is(err, billing.ErrInsufficientFunds) {
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("用户 [%s] 雪花余额不足，停止思考循环 (iter %d)", runCtx.Billing.ExternalID, i+1))
					var balMinor int64 = 0
					if cCtx, cCancel := context.WithTimeout(context.WithoutCancel(e.Context()), e.BillingConfig.Timeout); cCancel != nil {
						if bRes, bErr := e.BillingClient.Balance(cCtx, runCtx.Billing.Platform, runCtx.Billing.ExternalID); bErr == nil && bRes != nil {
							balMinor = bRes.BalanceMinor
						}
						cCancel()
					}
					runCtx.Billing.setBalance(balMinor, false)
					var replyMsg string
					if i == 0 {
						replyMsg = billing.FormatInsufficientFundsMessage(balMinor)
					} else {
						replyMsg = fmt.Sprintf("对话因雪花余额不足中断。\n%s", billing.FormatInsufficientFundsMessage(balMinor))
					}
					return AgentRunResult{
						Content:       replyMsg,
						MemoryWritten: memoryWritten,
						Usage:         totalUsage,
						Error:         billing.ErrInsufficientFunds,
					}
				}
				e.Log().Error(logs.SYSTEM, fmt.Sprintf("Alcyone 计费服务预扣款失败 (fail-closed, iter %d): %v", i+1, err))
				return AgentRunResult{
					Content:       billing.FormatBillingUnavailableMessage(),
					MemoryWritten: memoryWritten,
					Usage:         totalUsage,
					Error:         err,
				}
			}

			if res.Decision == billing.DecisionInsufficient {
				e.Log().Warn(logs.SYSTEM, fmt.Sprintf("用户 [%s] 雪花余额不足 (%d minor)，停止思考循环 (iter %d)", runCtx.Billing.ExternalID, res.BalanceMinor, i+1))
				var replyMsg string
				if i == 0 {
					replyMsg = billing.FormatInsufficientFundsMessage(res.BalanceMinor)
				} else {
					replyMsg = fmt.Sprintf("对话因雪花余额不足中断。\n%s", billing.FormatInsufficientFundsMessage(res.BalanceMinor))
				}
				return AgentRunResult{
					Content:       replyMsg,
					MemoryWritten: memoryWritten,
					Usage:         totalUsage,
					Error:         billing.ErrInsufficientFunds,
				}
			}

			if res.Decision == billing.DecisionReserved {
				reservationID = res.ReservationID
			}
			if res.Decision == billing.DecisionWelcome {
				reservationID = ""
			}
			runCtx.Billing.setBalance(res.BalanceMinor, res.Decision == billing.DecisionWelcome)
		}

		// 调用 internal/llm 包向大模型发送 HTTP 请求
		chatReq := core.ChatRequest{
			Model:     modelName,
			Messages:  coreMsgs,
			Tools:     modelTools,
			MaxTokens: e.BillingConfig.MaxOutputTokens,
			Route: core.RouteContext{
				Platform: runCtx.RouteScope.Platform,
				GroupID:  runCtx.RouteScope.GroupID,
			},
			TraceID: runCtx.SessionID,
		}
		var callBilling *BillingRunState
		if billingActive {
			callBilling = runCtx.Billing
		}
		resp, err, commitErr := e.chatAndSettle(ctx, chatReq, reservationID, callBilling)
		if err != nil {
			e.Log().Error(logs.LLM_RESPONSE, fmt.Sprintf("LLM调用失败: %v", err))
			return AgentRunResult{
				Content:       fmt.Sprintf("FrostAgent 错误：LLM 响应失败：%s", truncateRunes(err.Error(), 100)),
				LLMFailed:     true,
				MemoryWritten: memoryWritten,
				Usage:         totalUsage,
				Error:         err,
			}
		}
		if runCanceled(ctx) {
			return AgentRunResult{Silent: true, SilenceReason: "canceled", Error: context.Canceled}
		}

		if resp.Usage != nil {
			totalUsage.PromptTokens += resp.Usage.PromptTokens
			totalUsage.CompletionTokens += resp.Usage.CompletionTokens
			totalUsage.TotalTokens += resp.Usage.TotalTokens
		}

		// Map back to internal message for now to maintain compatibility
		responseMsg := &ChatMessage{
			Role:    string(resp.Message.Role),
			Content: resp.Message.Content,
		}
		if len(resp.Message.ToolCalls) > 0 {
			responseMsg.ToolCalls = make([]ToolCall, len(resp.Message.ToolCalls))
			for j, tc := range resp.Message.ToolCalls {
				responseMsg.ToolCalls[j] = ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}

		if commitErr != nil && len(responseMsg.ToolCalls) > 0 {
			return AgentRunResult{Content: "FrostAgent错误：计费结算失败，已终止后续工具执行。", MemoryWritten: memoryWritten, Usage: totalUsage, Error: commitErr}
		}

		messages = append(messages, *responseMsg)
		startedTools = make(map[string]bool)
		if stickerEnforcing {
			if interval, remaining, grace := e.stickerActivitySnapshot(); interval == 0 || grace > 0 || remaining == interval {
				stickerEnforcing = false
				stickerAttempts = 0
			}
		}

		// 是否给出最终答案
		if len(responseMsg.ToolCalls) == 0 {
			contentStr, _ := responseMsg.Content.(string)
			outputBlocked := false
			if blocked, decision := e.securityEvaluate(runCtx, security.StageModelOutput, security.SourceModelOutput, contentStr, ""); blocked || decision.Action == security.WatchdogFilter {
				if decision.IsFailure {
					contentStr = "FrostAgent安全控制：安全审查服务暂时不可用，模型输出已拦截。"
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("模型输出因安全审查服务异常被拦截: eval_id=%s", decision.EvaluationID))
					outputBlocked = true
				} else if decision.Action == security.WatchdogFilter && decision.SanitizedContent != "" {
					contentStr = decision.SanitizedContent
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("模型输出被安全控制过滤脱敏: category=%s eval_id=%s", decision.Classification.Category, decision.EvaluationID))
				} else {
					contentStr = "FrostAgent安全控制：模型输出已拦截。"
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("模型输出被安全控制拦截: reason=%s eval_id=%s", decision.Reason, decision.EvaluationID))
					outputBlocked = true
				}
			}
			if hasRunCtx && runCtx.Proactive {
				contentStr, _ = filterStickerMessages(contentStr)
			}
			messages[len(messages)-1].Content = contentStr
			if runCanceled(ctx) {
				messages = messages[:len(messages)-1]
				return AgentRunResult{Silent: true, Error: context.Canceled}
			}
			if isStandaloneAssistantSilentMarker(contentStr) {
				if stickerEnforcing {
					// Treat an internal silence marker like any other missed enforcement attempt.
					contentStr = ""
				} else {
					e.Log().WarnWithConsoleSummary(logs.SYSTEM, "模型以纯文本返回内部静默标记，已按保持沉默处理", "模型以纯文本返回内部静默标记，已按保持沉默处理")
					return AgentRunResult{
						MemoryWritten:    memoryWritten,
						Silent:           true,
						StaySilentCalled: false,
						SilenceReason:    "provider_fallback",
						Usage:            totalUsage,
					}
				}
			}
			finalSticker := hasRunCtx && runCtx.AdapterID == "onebot" && hasStickerImage(contentStr)
			willSendFinalMessage := contentStr != "" || (hasRunCtx && runCtx.Billing != nil && runCtx.Billing.BillingActive)
			if enforceStickerActivity && !outputBlocked {
				if strings.TrimSpace(contentStr) == ignoreEnforceStickerMarker && (stickerEnforcing || e.stickerNeededBeforeReply()) {
					messages = messages[:len(messages)-1]
					messages = appendStickerForgiveness(messages, i)
					e.grantStickerGrace()
					stickerEnforcing = false
					stickerAttempts = 0
					addStickerIteration()
					continue
				}
				if !finalSticker && (stickerEnforcing || (willSendFinalMessage && e.stickerNeededBeforeReply())) {
					stickerEnforcing = true
					stickerAttempts++
					if stickerAttempts >= 3 {
						messages = messages[:len(messages)-1]
						messages = appendStickerForgiveness(messages, i)
						e.grantStickerGrace()
						stickerEnforcing = false
						stickerAttempts = 0
					} else {
						messages = append(messages, ChatMessage{Role: "user", Content: stickerEnforcementPrompt})
					}
					addStickerIteration()
					continue
				}
			}
			e.Log().InfoWithConsoleSummary(logs.SYSTEM, "【智能体给出最终答案】", "【智能体给出最终答案】")
			return AgentRunResult{
				Content:       contentStr,
				MemoryWritten: memoryWritten,
				OutputBlocked: outputBlocked,
				Usage:         totalUsage,
			}
		}

		if conflict := staySilentConflict(responseMsg.ToolCalls); conflict != "" {
			e.Log().WarnWithConsoleSummary(logs.TOOL, conflict, "工具调用冲突")
			for _, tc := range responseMsg.ToolCalls {
				messages = append(messages, ChatMessage{
					Role:       "tool",
					Content:    conflict,
					ToolCallID: tc.ID,
				})
			}
			if stickerEnforcing {
				stickerAttempts++
				if stickerAttempts >= 3 {
					messages = appendStickerForgiveness(messages, i)
					e.grantStickerGrace()
					stickerEnforcing = false
					stickerAttempts = 0
				} else {
					messages = append(messages, ChatMessage{Role: "user", Content: stickerEnforcementPrompt})
				}
				addStickerIteration()
			}
			continue
		}

		for tcIndex := 0; tcIndex < len(responseMsg.ToolCalls); tcIndex++ {
			tc := responseMsg.ToolCalls[tcIndex]
			if runCanceled(ctx) {
				return AgentRunResult{Silent: true, Error: context.Canceled}
			}
			if err := ctx.Err(); err != nil {
				return AgentRunResult{Silent: true, SilenceReason: "canceled", Error: err, Usage: totalUsage}
			}
			if hasRunCtx && runCtx.Proactive && !e.isToolAllowedForProactive(tc.Function.Name) {
				e.Log().WarnWithConsoleSummary(logs.SYSTEM, fmt.Sprintf("主动回复轮次禁止调用副作用工具 [%s]", tc.Function.Name), "主动回复禁止副作用工具")
				return AgentRunResult{
					MemoryWritten: memoryWritten,
					Silent:        true,
					Usage:         totalUsage,
				}
			}
			if hasRunCtx && runCtx.SessionID != "" && runCtx.Epoch > 0 && e.SessionManager != nil {
				if sessCore, ok := e.SessionManager.Get(runCtx.SessionID); ok {
					if sess, isSess := sessCore.(*SessionContext); isSess {
						if sess.Epoch() != runCtx.Epoch {
							return AgentRunResult{Silent: true, SilenceReason: "epoch_changed", Error: errors.New("session epoch invalidated"), Usage: totalUsage}
						}
					}
				}
			}
			if enforceStickerActivity && tc.Function.Name == "send_message" && tcIndex+1 < len(responseMsg.ToolCalls) && responseMsg.ToolCalls[tcIndex+1].Function.Name == "send_sticker" {
				if e.stickerNeededBeforeReply() {
					stickerCall := responseMsg.ToolCalls[tcIndex+1]
					startedTools[tc.ID], startedTools[stickerCall.ID] = true, true
					textResult, stickerResult := e.deliverStickerPair(ctx, &runCtx, tc, stickerCall)
					if runCanceled(ctx) {
						return AgentRunResult{Silent: true, Error: context.Canceled}
					}
					messages = append(messages,
						ChatMessage{Role: "tool", Content: textResult, ToolCallID: tc.ID},
						ChatMessage{Role: "tool", Content: stickerResult, ToolCallID: stickerCall.ID},
					)
					tcIndex++
					continue
				}
			}
			if blocked, decision := e.securityEvaluate(runCtx, security.StageToolArgument, security.SourceToolArgument, tc.Function.Arguments, tc.Function.Name); blocked || decision.Action == security.WatchdogFilter {
				if decision.IsFailure {
					messages = append(messages, ChatMessage{Role: "tool", Content: "FrostAgent安全控制：安全审查服务暂时不可用，该工具调用已被阻止。", ToolCallID: tc.ID})
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("工具入参因安全审查服务异常被阻止: tool=%s eval_id=%s", tc.Function.Name, decision.EvaluationID))
				} else {
					messages = append(messages, ChatMessage{Role: "tool", Content: "FrostAgent安全控制：该工具调用已被阻止。", ToolCallID: tc.ID})
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("工具入参被安全控制拦截: tool=%s reason=%s eval_id=%s", tc.Function.Name, decision.Reason, decision.EvaluationID))
				}
				continue
			} else if decision.Action == security.WatchdogWarn && hasRunCtx {
				mergeSecurityNotice(&runCtx, decision.WarningNotice)
			}
			toolCallLog := formatToolCallLog(tc.Function.Name, tc.Function.Arguments)
			e.Log().InfoWithConsoleSummary(logs.TOOL, toolCallLog, "【智能体调用工具】"+tc.Function.Name)

			var toolResult string
			var execution core.ToolExecutionResult
			toolSucceeded := false
			// 从 map 或 MCP Manager 中找到工具执行
			if tool, exists := e.findToolExecutor(tc.Function.Name); exists {
				var err error
				startedTools[tc.ID] = true
				execution, err = executeInterruptibleTool(ctx, tool, tc)
				if runCanceled(ctx) {
					if isMemoryWriteRequest(tc) {
						memoryWritten = true
					}
					return AgentRunResult{Silent: true, SilenceReason: "canceled", Error: ctx.Err(), Usage: totalUsage}
				}
				if err != nil {
					if errors.Is(err, security.ErrBanUserSuccess) {
						if hasRunCtx && runCtx.Proactive {
							return AgentRunResult{
								MemoryWritten: memoryWritten,
								Silent:        true,
								Usage:         totalUsage,
							}
						}
						e.Log().WarnWithConsoleSummary(logs.SYSTEM, "Bot 自主封禁用户成功，终止思考循环", "Bot 封禁用户")
						return AgentRunResult{
							Content:       security.RejectGatewayMsg,
							MemoryWritten: memoryWritten,
							Banned:        true,
							Usage:         totalUsage,
						}
					}
					if errors.Is(err, actionscat.ErrDisabled) {
						toolResult = err.Error()
					} else {
						toolResult = fmt.Sprintf("FrostAgent错误：工具执行失败: %v", err)
					}
				} else {
					toolSucceeded = true
					toolResult = execution.Content
					if IsStaySilentTool(tc.Function.Name) {
						if stickerEnforcing {
							toolResult = stickerEnforcementPrompt
						} else {
							e.Log().InfoWithConsoleSummary(logs.SYSTEM, "【智能体选择保持沉默】", "【智能体选择保持沉默】")
							return AgentRunResult{
								MemoryWritten:    memoryWritten,
								Silent:           true,
								StaySilentCalled: true,
								SilenceReason:    "stay_silent",
								Usage:            totalUsage,
							}
						}
					}
					if execution.MemoryWritten {
						memoryWritten = true
					}
				}
			} else {
				toolResult = "FrostAgent错误：工具未找到"
			}

			// 单条工具结果过大保护
			if len(toolResult) > MaxToolOutputBytes {
				largeOutputLog := fmt.Sprintf("工具 [%s] 输出过大 (%d 字节)，已截断至 %d 字节", tc.Function.Name, len(toolResult), MaxToolOutputBytes)
				e.Log().WarnWithConsoleSummary(logs.TOOL, largeOutputLog, "工具输出过大，已截断")
				cut := MaxToolOutputBytes
				for cut > 0 && !utf8.RuneStart(toolResult[cut]) {
					cut--
				}
				toolResult = toolResult[:cut] + "\n...(工具输出过长，已截断)"
			}

			securityContent := toolResult
			if execution.MessagePayload != "" && execution.MessagePayload != toolResult && len(execution.MessagePayload) <= MaxToolOutputBytes {
				securityContent += "\n" + execution.MessagePayload
			}
			if blocked, decision := e.securityEvaluate(runCtx, security.StageToolResult, security.SourceToolResult, securityContent, tc.Function.Name); blocked || decision.Action == security.WatchdogFilter {
				if decision.IsFailure {
					toolResult = "FrostAgent安全控制：安全审查服务暂时不可用，外部工具结果已隔离。"
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("工具结果因安全审查服务异常被隔离: tool=%s eval_id=%s", tc.Function.Name, decision.EvaluationID))
				} else if decision.Action == security.WatchdogFilter && decision.SanitizedContent != "" {
					toolResult = decision.SanitizedContent
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("工具结果被安全控制过滤脱敏: tool=%s category=%s eval_id=%s", tc.Function.Name, decision.Classification.Category, decision.EvaluationID))
				} else {
					toolResult = "FrostAgent安全控制：外部工具结果已隔离。"
					e.Log().Warn(logs.SYSTEM, fmt.Sprintf("工具结果被安全控制拦截: tool=%s reason=%s eval_id=%s", tc.Function.Name, decision.Reason, decision.EvaluationID))
				}
				toolSucceeded = false
			} else if decision.Action == security.WatchdogWarn && hasRunCtx {
				mergeSecurityNotice(&runCtx, decision.WarningNotice)
			}
			toolResultLog := formatToolResultLog(tc.Function.Name, toolResult)
			e.Log().InfoWithConsoleSummary(logs.TOOL, toolResultLog, "【工具执行结果】...")

			if toolSucceeded {
				deliverySticker := hasStickerImage(execution.MessagePayload)
				if enforceStickerActivity && execution.MessagePayload != "" && !deliverySticker {
					if e.stickerNeededBeforeReply() {
						toolResult = "表情包额度已到期，请先发送表情包或输出 <ignore_enforce_sticker /> 请求宽容。"
						messages = append(messages, ChatMessage{Role: "tool", Content: toolResult, ToolCallID: tc.ID})
						continue
					}
				}
				toolResult, _ = e.deliverToolResult(ctx, execution, toolResult)
			}

			if runCanceled(ctx) {
				return AgentRunResult{Silent: true, Error: context.Canceled}
			}
			toolMsg := ChatMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolMsg)
		}
		if enforceStickerActivity && (stickerEnforcing || e.stickerNeededBeforeReply()) {
			if interval, remaining, grace := e.stickerActivitySnapshot(); interval > 0 && grace == 0 && (remaining == 0 || stickerEnforcing) {
				if remaining == interval {
					// A sticker was sent in this batch; the quota is satisfied.
					stickerEnforcing = false
					stickerAttempts = 0
				} else if grace == 0 {
					stickerEnforcing = true
					stickerAttempts++
					if stickerAttempts >= 3 {
						messages = appendStickerForgiveness(messages, i)
						e.grantStickerGrace()
						stickerEnforcing = false
						stickerAttempts = 0
					} else {
						messages = append(messages, ChatMessage{Role: "user", Content: stickerEnforcementPrompt})
					}
					addStickerIteration()
				}
			}
		}
	}
	if hasRunCtx && runCtx.Proactive {
		return AgentRunResult{
			MemoryWritten: memoryWritten,
			Silent:        true,
			Error:         ErrMaxIterationsReached,
			Usage:         totalUsage,
		}
	}
	return AgentRunResult{
		Content:       "FrostAgent错误：达到最大迭代次数，未能得出最终答案",
		MemoryWritten: memoryWritten,
		Usage:         totalUsage,
	}
}

// isStandaloneAssistantSilentMarker prevents the internal history marker from
// leaking to adapters when a model imitates a previous silent assistant turn.
// Embedded references remain ordinary user-visible content.
func isStandaloneAssistantSilentMarker(content string) bool {
	return strings.TrimSpace(content) == AssistantSilentMarker
}

// staySilentConflict rejects ambiguous parallel tool batches before any tool
// executes. The terminal decision must be exclusive so the model cannot both
// request side effects and terminate the same assistant response.
func staySilentConflict(toolCalls []ToolCall) string {
	if len(toolCalls) <= 1 {
		return ""
	}
	for _, toolCall := range toolCalls {
		if IsStaySilentTool(toolCall.Function.Name) {
			return "工具调用冲突：stay_silent 必须单独调用，不能与其他工具同时使用；请重新选择要执行的动作"
		}
	}
	return ""
}

// effectiveMaxHistory 返回当前生效的历史消息上限：
// 优先读 MAX_CONTEXT_MESSAGES env（运行期修改即时生效），否则回退到 fallback。
func effectiveMaxHistory(fallback int, scopes ...*runtimescope.Scope) int {
	scope := runtimescope.First(scopes)
	if v := scope.Getenv("MAX_CONTEXT_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= minHistory {
			return n
		}
	}
	return fallback
}

// TrimSession 将会话持久化的历史裁剪到配置的消息上限
// （MAX_CONTEXT_MESSAGES env，未配置时回退 SessionManager.MaxHistory）。
func (e *Engine) TrimSession(session *SessionContext) {
	if session == nil {
		return
	}
	session.TrimHistory(effectiveMaxHistory(e.SessionManager.MaxHistory, e.Scope))
}

// trimMessagesForSession 改进的裁剪逻辑，确保工具链完整
func (e *Engine) trimMessagesForSession(messages []ChatMessage) []ChatMessage {
	maxHistory := effectiveMaxHistory(e.SessionManager.MaxHistory, e.Scope)
	if len(messages) <= maxHistory+1 {
		return messages
	}

	// 始终保留第一条 system prompt
	startIdx := len(messages) - maxHistory

	// 如果起始位置是一条 tool 消息，必须向前追溯到对应的 assistant 消息
	// 否则 API 会报错：tool message must follow assistant message with tool_calls
	for startIdx > 1 && messages[startIdx].Role == "tool" {
		startIdx--
	}

	trimmed := make([]ChatMessage, 0, len(messages)-startIdx+1)
	trimmed = append(trimmed, messages[0])
	trimmed = append(trimmed, messages[startIdx:]...)
	return trimmed
}

func (e *Engine) findToolExecutor(name string) (ToolExecutor, bool) {
	if e == nil {
		return nil, false
	}
	if tool, exists := e.ToolRegistry[name]; exists {
		return tool, true
	}
	if e.MCPManager != nil {
		if adapter, ok := e.MCPManager.LookupAdapter(name); ok {
			return adapter, true
		}
	}
	return nil, false
}

// EffectiveTools returns the current effective tool definitions (builtins + enabled MCP tools).
func (e *Engine) EffectiveTools() []core.Tool {
	if e == nil {
		return nil
	}
	var modelTools []core.Tool
	if len(e.ToolRegistry) > 0 {
		for _, t := range e.ToolRegistry {
			if availability, ok := t.(interface{ IsEnabled() bool }); ok && !availability.IsEnabled() {
				continue
			}
			modelTools = append(modelTools, core.Tool{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			})
		}
	}
	if e.MCPManager != nil {
		modelTools = append(modelTools, e.MCPManager.EffectiveTools()...)
	}
	sort.SliceStable(modelTools, func(i, j int) bool {
		return modelTools[i].Name < modelTools[j].Name
	})
	return modelTools
}

// ModelTools returns the current effective tools sorted by name.
func (e *Engine) ModelTools() []core.Tool {
	return e.EffectiveTools()
}

// ConvertToCoreMessages converts internal ChatMessage to core.ChatMessage
func ConvertToCoreMessages(msgs []ChatMessage) []core.ChatMessage {
	return convertToCoreMessages(msgs)
}

// GroupRawLimit returns the maximum number of recent uncompacted group messages
// to include in the context. This strictly matches the current GroupCompactor's buffer size.
func (e *Engine) GroupRawLimit() int {
	if e != nil && e.GroupCompactor != nil {
		if size := e.GroupCompactor.BufferSize(); size > 0 {
			return size
		}
	}
	return DefaultGroupCompactBufferSize
}

// GroupRawMaxChars dynamically returns the total character budget for uncompacted
// group messages from the current runtime environment.
func (e *Engine) GroupRawMaxChars() int {
	if e == nil {
		return LoadGroupRawContextConfigFromEnv().MaxChars
	}
	cfg := LoadGroupRawContextConfigFromEnv(e.Scope)
	return cfg.MaxChars
}

// convertToCoreMessages converts internal ChatMessage to core.ChatMessage
func convertToCoreMessages(msgs []ChatMessage) []core.ChatMessage {
	res := make([]core.ChatMessage, len(msgs))
	for i, m := range msgs {
		coreMsg := core.ChatMessage{
			Role:       core.MessageRole(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			coreMsg.ToolCalls = make([]core.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				coreMsg.ToolCalls[j] = core.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: core.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
		}
		res[i] = coreMsg
	}
	return res
}

func (e *Engine) SetDialoguePrompt(prompt string) {
	e.dialogueMu.Lock()
	defer e.dialogueMu.Unlock()
	e.DialoguePrompt = prompt
}

func (e *Engine) PersonaDialogue() string {
	e.dialogueMu.RLock()
	defer e.dialogueMu.RUnlock()
	return e.DialoguePrompt
}

func formatToolCallLog(name string, args string) string {
	return fmt.Sprintf("【智能体调用工具】%s，参数: %s", name, args)
}

func formatToolResultLog(_ string, result string) string {
	return fmt.Sprintf("【工具执行结果】%s", result)
}

func mergeSecurityNotice(runCtx *RunContext, notice string) {
	if runCtx == nil || strings.TrimSpace(notice) == "" {
		return
	}
	notice = strings.TrimSpace(notice)
	if runCtx.SecurityNotice == "" {
		runCtx.SecurityNotice = notice
		return
	}
	if !strings.Contains(runCtx.SecurityNotice, notice) {
		runCtx.SecurityNotice = runCtx.SecurityNotice + "\n\n" + notice
	}
}
