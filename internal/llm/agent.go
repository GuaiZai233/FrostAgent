package llm

import (
	"FrostAgent/internal/billing"
	"FrostAgent/internal/core"
	"FrostAgent/internal/groupsummary"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
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

type contextualToolExecutor interface {
	ExecuteContext(ctx context.Context, args string) (string, error)
}

const (
	StaySilentToolName    = "stay_silent"
	AssistantSilentMarker = "<assistant_silent />"

	MaxSingleInputTokens = 32000
	MaxContextTokens     = 128000
	MaxToolOutputBytes   = 65536 // 64KB
)

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
// failures never set it.
type AgentRunResult struct {
	Content       string
	MemoryWritten bool
	Silent        bool
	Usage         core.Usage
	Error         error
}

// Engine 结构体，用于管理智能体的执行
type Engine struct {
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
	MemoryReader      *memory.Reader
	MemoryWriter      *memory.Writer
	MemoryGateway     *memory.Gateway
	MemoryCatalog     *memory.CatalogStore
	MemoryReflections *memory.ReflectionManager
	GroupCompactor    *GroupCompactor
	GroupSummaryStore *groupsummary.Store

	// DialoguePrompt carries formatted few-shot persona examples (optional)
	DialoguePrompt string
}

// Run 执行智能体的主循环（单次无状态调用）
func (e *Engine) Run(prompt string) string {
	systemPrompt := os.Getenv("SYSTEM_PROMPT")
	if e.DialoguePrompt != "" {
		if systemPrompt != "" {
			systemPrompt += "\n\n" + e.DialoguePrompt
		} else {
			systemPrompt = e.DialoguePrompt
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
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		if e.DialoguePrompt != "" {
			if systemPrompt != "" {
				systemPrompt += "\n\n" + e.DialoguePrompt
			} else {
				systemPrompt = e.DialoguePrompt
			}
		}
		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}
	return e.runLoop(e.newRoutingContext(), messages)
}

func (e *Engine) newRoutingContext() context.Context {
	ctx := context.Background()
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

// RunMessagesWithContext runs one agent request with owner and transport hooks
// scoped to that request instead of mutable Engine fields.
func (e *Engine) RunMessagesWithContext(
	messages []ChatMessage,
	runContext RunContext,
) AgentRunResult {
	owner := runContext.Owner
	if owner != "" && e.MemoryWriter != nil {
		e.MemoryWriter.RememberRoute(owner, core.RouteContext{
			Platform: runContext.RouteScope.Platform,
			GroupID:  runContext.RouteScope.GroupID,
		})
	}
	ctx := context.Background()
	if e.ModelRouter != nil {
		if runContext.RouteSnapshot == nil {
			runContext.RouteSnapshot = e.ModelRouter.Snapshot()
		}
		ctx = e.ModelRouter.WithSnapshot(ctx, runContext.RouteSnapshot)
	}
	ctx = withRunContext(ctx, runContext)

	if len(messages) == 0 || messages[0].Role != "system" {
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		// 注入当前系统时间，让模型能判断对话中的相对时间（今天/明天/本周）
		systemPrompt = "当前系统时间：" + memory.CurrentTimeLabel(time.Now()) + "\n\n" + systemPrompt

		if e.DialoguePrompt != "" {
			systemPrompt += "\n\n" + e.DialoguePrompt
		}

		if owner != "" && e.MemoryCatalog != nil {
			catalogContext, err := e.MemoryCatalog.FormatForPrompt(owner)
			if err != nil {
				logs.Error(logs.SYSTEM, fmt.Sprintf("读取记忆主题索引失败: %v", err))
			} else if catalogContext != "" {
				systemPrompt += "\n\n" + catalogContext
			}
		}

		// 召回 → 网关过滤 → 注入
		if owner != "" && e.MemoryReader != nil && e.MemoryGateway != nil {
			lastUserMsg := extractLastUserMessage(messages)
			raw, err := e.MemoryReader.Recall(context.Background(), lastUserMsg)
			if err == nil {
				filtered := e.MemoryGateway.Filter(raw, owner)
				filtered = e.MemoryReader.Limit(filtered)
				if len(filtered) > 0 {
					memoryContext := e.MemoryGateway.FormatForContext(filtered, owner)
					systemPrompt += "\n\n" + memoryContext
					if err := e.MemoryReader.RecordRecall(filtered); err != nil {
						logs.Warn(logs.SYSTEM, fmt.Sprintf("记录主动召回记忆次数失败: %v", err))
					}
				}
			}
		}

		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}

	// Persist the assembled system prompt so Session Context Inspector can show
	// the real dynamically-built prompt (time + dialogue + memory) rather than a
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
	minTurns := positiveIntEnv("MEMORY_EXTRACT_BATCH_MIN", 3)
	maxTurns := positiveIntEnv("MEMORY_EXTRACT_BATCH_MAX", 5)
	maxTurns = max(maxTurns, minTurns)
	batch, ready := session.EnqueuePendingTurn(items, minTurns, maxTurns)
	if !ready {
		return
	}
	go e.extractPendingBatch(batch)
}

func (e *Engine) extractPendingBatch(batch []memory.PendingExtractionItem) {
	type ownerBatch struct {
		owner     string
		ownerType memory.OwnerType
		route     core.RouteContext
		messages  []core.ChatMessage
	}
	groups := make(map[string]*ownerBatch)
	order := make([]string, 0)
	for _, item := range batch {
		if item.Owner == "" {
			continue
		}
		key := string(item.OwnerType) + "\x00" + item.Owner + "\x00" + item.Route.Platform + "\x00" + item.Route.GroupID
		group := groups[key]
		if group == nil {
			group = &ownerBatch{
				owner:     item.Owner,
				ownerType: memory.NormalizeOwnerType(item.OwnerType),
				route:     item.Route,
			}
			groups[key] = group
			order = append(order, key)
		}
		group.messages = append(group.messages, item.Message)
	}
	for _, key := range order {
		group := groups[key]
		if err := e.MemoryWriter.ExtractByOwnerWithRoute(group.owner, group.ownerType, group.route, group.messages); err != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("批量提取记忆失败 (owner: %s): %v", group.owner, err))
		}
	}
}

func positiveIntEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
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
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		messages = append(messages, ChatMessage{Role: "system", Content: systemPrompt})
	}

	// add user input
	messages = append(messages, ChatMessage{Role: "user", Content: prompt})

	result := e.runLoop(context.Background(), messages)

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
func (e *Engine) runLoopWithResult(ctx context.Context, messages []ChatMessage) AgentRunResult {
	memoryWritten := false
	var totalUsage core.Usage
	modelTools := e.ModelTools()

	runCtx, hasRunCtx := RunContextFromContext(ctx)
	billingActive := hasRunCtx && runCtx.Billing != nil && runCtx.Billing.BillingActive && e.BillingClient != nil && e.BillingConfig.Enabled
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
				return AgentRunResult{Silent: true}
			}
			return AgentRunResult{
				Content: fmt.Sprintf("FrostAgent 错误：LLM 响应失败：%s", truncateRunes(err.Error(), 100)),
				Error:   err,
			}
		}
		modelName = target.UpstreamModel
	}

	// 主循环
	for i := 0; i < e.MaxIterations; i++ {
		e.TotalMessagesProcessed.Add(1)
		logs.Info(logs.SYSTEM, fmt.Sprintf("【第%d轮思考开始】", i+1))

		coreMsgs := convertToCoreMessages(messages)

		// 检查上下文总 Token 是否超出模型硬上限 (包含 tools 定义开销)
		contextTokens, err := billing.EstimateMessagesTokens(coreMsgs, modelTools)
		if err != nil {
			contextTokens = billing.EstimateTokens(coreMsgs)
		}
		if contextTokens > MaxContextTokens {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("上下文长度 (%d tokens) 超出硬上限 (%d tokens)", contextTokens, MaxContextTokens))
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
			)
			if err != nil {
				logs.Error(logs.SYSTEM, fmt.Sprintf("计费预估失败 (fail-closed, iter %d): %v", i+1, err))
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
			callID := fmt.Sprintf("call_%s_%d", taskID, i)
			idempotencyKey := fmt.Sprintf("res_%s_%s_%d", platform, taskID, i)

			reserveReq := billing.LLMReserveRequest{
				Platform:       platform,
				ExternalID:     runCtx.Billing.ExternalID,
				DisplayName:    runCtx.Billing.DisplayName,
				TaskID:         taskID,
				CallID:         callID,
				AmountMinor:    amountMinor,
				IdempotencyKey: idempotencyKey,
			}

			bCtx, bCancel := context.WithTimeout(context.Background(), e.BillingConfig.Timeout)
			res, err := e.BillingClient.ReserveLLM(bCtx, reserveReq)
			bCancel()

			if err != nil {
				if errors.Is(err, billing.ErrInsufficientFunds) {
					logs.Warn(logs.SYSTEM, fmt.Sprintf("用户 [%s] 雪花余额不足，停止思考循环 (iter %d)", runCtx.Billing.ExternalID, i+1))
					var balMinor int64 = 0
					if cCtx, cCancel := context.WithTimeout(context.Background(), e.BillingConfig.Timeout); cCancel != nil {
						if bRes, bErr := e.BillingClient.Balance(cCtx, runCtx.Billing.Platform, runCtx.Billing.ExternalID); bErr == nil && bRes != nil {
							balMinor = bRes.BalanceMinor
						}
						cCancel()
					}
					runCtx.Billing.LastBalanceMinor = balMinor
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
				logs.Error(logs.SYSTEM, fmt.Sprintf("Alcyone 计费服务预扣款失败 (fail-closed, iter %d): %v", i+1, err))
				return AgentRunResult{
					Content:       billing.FormatBillingUnavailableMessage(),
					MemoryWritten: memoryWritten,
					Usage:         totalUsage,
					Error:         err,
				}
			}

			if res.Decision == billing.DecisionInsufficient {
				logs.Warn(logs.SYSTEM, fmt.Sprintf("用户 [%s] 雪花余额不足 (%d minor)，停止思考循环 (iter %d)", runCtx.Billing.ExternalID, res.BalanceMinor, i+1))
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
				runCtx.Billing.WelcomeGranted = true
				reservationID = ""
			}
			runCtx.Billing.LastBalanceMinor = res.BalanceMinor
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
		}
		resp, err := e.Provider.Chat(ctx, chatReq)
		if err != nil {
			logs.Error(logs.LLM_RESPONSE, fmt.Sprintf("LLM调用失败: %v", err))
			if billingActive && reservationID != "" {
				rCtx, rCancel := context.WithTimeout(context.Background(), e.BillingConfig.Timeout)
				if _, relErr := e.BillingClient.ReleaseLLM(rCtx, reservationID, billing.ReasonModelFailed); relErr != nil {
					logs.Error(logs.SYSTEM, fmt.Sprintf("释放预扣款失败 (iter %d): %v", i+1, relErr))
				}
				rCancel()
			}
			return AgentRunResult{
				Content:       fmt.Sprintf("FrostAgent 错误：LLM 响应失败：%s", truncateRunes(err.Error(), 100)),
				MemoryWritten: memoryWritten,
				Usage:         totalUsage,
				Error:         err,
			}
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

		// 结算本轮调用
		if billingActive && reservationID != "" {
			promptTok := 0
			compTok := 0
			if resp.Usage != nil {
				promptTok = resp.Usage.PromptTokens
				compTok = resp.Usage.CompletionTokens
			}
			price, _ := billing.GetPrice(modelName)
			actualMinor := billing.CalculateMinorUnits(promptTok, compTok, price)

			cCtx, cCancel := context.WithTimeout(context.Background(), e.BillingConfig.Timeout)
			commitRes, commitErr := e.BillingClient.CommitLLM(cCtx, reservationID, actualMinor)
			cCancel()

			if commitErr != nil {
				logs.Error(logs.SYSTEM, fmt.Sprintf("计费结算提交失败 (reservation %s, iter %d): %v", reservationID, i+1, commitErr))
				// 如果是 Tool Call 且 commit 失败，禁止执行工具以防止免费副作用
				if len(responseMsg.ToolCalls) > 0 {
					logs.Warn(logs.SYSTEM, "Tool Call 阶段 commit 失败，终止本轮工具执行")
					return AgentRunResult{
						Content:       "FrostAgent错误：计费结算失败，已终止后续工具执行。",
						MemoryWritten: memoryWritten,
						Usage:         totalUsage,
						Error:         commitErr,
					}
				}
				// 若是最终回答且 commit 失败，本轮按免费处理，不影响回复输出
			} else if commitRes != nil {
				runCtx.Billing.TotalBilledMinor += actualMinor
				runCtx.Billing.LastBalanceMinor = commitRes.BalanceMinor
				runCtx.Billing.IterationsBilled++
			}
		}

		messages = append(messages, *responseMsg)

		// 是否给出最终答案
		if len(responseMsg.ToolCalls) == 0 {
			contentStr, _ := responseMsg.Content.(string)
			if isStandaloneAssistantSilentMarker(contentStr) {
				logs.Warn(logs.SYSTEM, "模型以纯文本返回内部静默标记，已按保持沉默处理")
				return AgentRunResult{
					MemoryWritten: memoryWritten,
					Silent:        true,
					Usage:         totalUsage,
				}
			}
			logs.Info(logs.SYSTEM, "【智能体给出最终答案】")
			return AgentRunResult{
				Content:       contentStr,
				MemoryWritten: memoryWritten,
				Usage:         totalUsage,
			}
		}

		if conflict := staySilentConflict(responseMsg.ToolCalls); conflict != "" {
			logs.Warn(logs.TOOL, conflict)
			for _, tc := range responseMsg.ToolCalls {
				messages = append(messages, ChatMessage{
					Role:       "tool",
					Content:    conflict,
					ToolCallID: tc.ID,
				})
			}
			continue
		}

		for _, tc := range responseMsg.ToolCalls {
			logs.Info(logs.TOOL, fmt.Sprintf("【智能体调用工具】%s，参数: %s", tc.Function.Name, tc.Function.Arguments))

			var toolResult string
			toolSucceeded := false
			// 从 map 中找到工具执行
			if tool, exists := e.ToolRegistry[tc.Function.Name]; exists {
				var res string
				var err error
				if contextualTool, ok := tool.(contextualToolExecutor); ok {
					res, err = contextualTool.ExecuteContext(ctx, tc.Function.Arguments)
				} else {
					res, err = tool.Execute(tc.Function.Arguments)
				}
				if err != nil {
					toolResult = fmt.Sprintf("FrostAgent错误：工具执行失败: %v", err)
				} else {
					toolSucceeded = true
					toolResult = res
					if tc.Function.Name == StaySilentToolName {
						logs.Info(logs.SYSTEM, "【智能体选择保持沉默】")
						return AgentRunResult{
							MemoryWritten: memoryWritten,
							Silent:        true,
							Usage:         totalUsage,
						}
					}
					if isMemoryWriteAction(tc, res) {
						memoryWritten = true
					}
				}
			} else {
				toolResult = "FrostAgent错误：工具未找到"
			}

			// 单条工具结果过大保护
			if len(toolResult) > MaxToolOutputBytes {
				logs.Warn(logs.TOOL, fmt.Sprintf("工具 [%s] 输出过大 (%d 字节)，已截断至 %d 字节", tc.Function.Name, len(toolResult), MaxToolOutputBytes))
				cut := MaxToolOutputBytes
				for cut > 0 && !utf8.RuneStart(toolResult[cut]) {
					cut--
				}
				toolResult = toolResult[:cut] + "\n...(工具输出过长，已截断)"
			}

			logs.Info(logs.TOOL, fmt.Sprintf("【工具执行结果】%s", toolResult))

			// 只有校验通过的 send_message 才能触发实际发送。
			if runContext, ok := RunContextFromContext(ctx); tc.Function.Name == "send_message" && toolSucceeded && ok && runContext.SendHook != nil {
				if err := runContext.SendHook(toolResult); err != nil {
					toolResult = fmt.Sprintf("消息发送失败：%v", err)
				} else {
					toolResult = "消息已发送"
				}
			}

			toolMsg := ChatMessage{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolMsg)
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
		if toolCall.Function.Name == StaySilentToolName {
			return "工具调用冲突：stay_silent 必须单独调用，不能与其他工具同时使用；请重新选择要执行的动作"
		}
	}
	return ""
}

// isMemoryWriteAction identifies explicit writes that must not be extracted a
// second time. Background reflection does not count as handling this turn.
func isMemoryWriteAction(toolCall ToolCall, result string) bool {
	if toolCall.Function.Name != "memory" {
		return false
	}

	var params struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &params); err != nil {
		return false
	}
	return params.Action == "write" && result == "记忆已写入"
}

// effectiveMaxHistory 返回当前生效的历史消息上限：
// 优先读 MAX_CONTEXT_MESSAGES env（运行期修改即时生效），否则回退到 fallback。
func effectiveMaxHistory(fallback int) int {
	if v := os.Getenv("MAX_CONTEXT_MESSAGES"); v != "" {
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
	session.TrimHistory(effectiveMaxHistory(e.SessionManager.MaxHistory))
}

// trimMessagesForSession 改进的裁剪逻辑，确保工具链完整
func (e *Engine) trimMessagesForSession(messages []ChatMessage) []ChatMessage {
	maxHistory := effectiveMaxHistory(e.SessionManager.MaxHistory)
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

// ModelTools returns the registered tools as []core.Tool sorted by name.
func (e *Engine) ModelTools() []core.Tool {
	if e == nil || len(e.ToolRegistry) == 0 {
		return nil
	}
	var modelTools []core.Tool
	for _, t := range e.ToolRegistry {
		modelTools = append(modelTools, core.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	sort.SliceStable(modelTools, func(i, j int) bool {
		return modelTools[i].Name < modelTools[j].Name
	})
	return modelTools
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
	cfg := LoadGroupRawContextConfigFromEnv()
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
