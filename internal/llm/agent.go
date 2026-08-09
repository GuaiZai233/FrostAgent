package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/memory"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
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
)

// AgentRunResult carries the final content and side-effect decisions from one
// agent loop. Silent is true only when the model explicitly and successfully
// invokes the terminal stay_silent tool; provider failures never set it.
type AgentRunResult struct {
	Content       string
	MemoryWritten bool
	Silent        bool
}

// Engine 结构体，用于管理智能体的执行
type Engine struct {
	MaxIterations          int
	ToolRegistry           map[string]ToolExecutor
	Provider               core.LLMProvider // LLM 供应商接口
	BaseURL                string           // API 地址
	APIKey                 string           // API 密钥
	ModelName              string           // 模型名称
	SessionManager         *SessionManager  // 会话上下文管理器
	Dispatcher             core.MessageDispatcher
	StartedAt              time.Time // 引擎启动时间
	Version                string    // 版本号
	TotalMessagesProcessed atomic.Int64

	// Memory components (optional, nil = memory disabled)
	MemoryReader      *memory.Reader
	MemoryWriter      *memory.Writer
	MemoryGateway     *memory.Gateway
	MemoryCatalog     *memory.CatalogStore
	MemoryReflections *memory.ReflectionManager
	GroupCompactor    *GroupCompactor
}

// Run 执行智能体的主循环（单次无状态调用）
func (e *Engine) Run(prompt string) string {
	systemPrompt := os.Getenv("SYSTEM_PROMPT")
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}
	result := e.runLoop(context.Background(), messages)
	return result
}

// RunMessages 执行智能体的主循环（直接传入消息数组）
func (e *Engine) RunMessages(messages []ChatMessage) string {
	// 如果消息数组中没有 system 提示词，添加一个
	if len(messages) == 0 || messages[0].Role != "system" {
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}
	return e.runLoop(context.Background(), messages)
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

	if len(messages) == 0 || messages[0].Role != "system" {
		systemPrompt := os.Getenv("SYSTEM_PROMPT")
		// 注入当前系统时间，让模型能判断对话中的相对时间（今天/明天/本周）
		systemPrompt = memory.CurrentTimeLabel(time.Now()) + "\n\n" + systemPrompt

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
				}
			}
		}

		messages = append([]ChatMessage{
			{Role: "system", Content: systemPrompt},
		}, messages...)
	}

	ctx := withRunContext(context.Background(), runContext)
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
	if maxTurns < minTurns {
		maxTurns = minTurns
	}
	batch, ready := session.EnqueuePendingTurn(items, minTurns, maxTurns)
	if !ready {
		return
	}
	go e.extractPendingBatch(batch)
}

func (e *Engine) extractPendingBatch(batch []memory.PendingExtractionItem) {
	type ownerBatch struct {
		ownerType memory.OwnerType
		messages  []core.ChatMessage
	}
	groups := make(map[string]*ownerBatch)
	order := make([]string, 0)
	for _, item := range batch {
		if item.Owner == "" {
			continue
		}
		key := string(item.OwnerType) + "\x00" + item.Owner
		group := groups[key]
		if group == nil {
			group = &ownerBatch{ownerType: memory.NormalizeOwnerType(item.OwnerType)}
			groups[key] = group
			order = append(order, key)
		}
		group.messages = append(group.messages, item.Message)
	}
	for _, key := range order {
		group := groups[key]
		owner := strings.SplitN(key, "\x00", 2)[1]
		if err := e.MemoryWriter.ExtractByOwner(owner, group.ownerType, group.messages); err != nil {
			logs.Warn(logs.SYSTEM, fmt.Sprintf("批量提取记忆失败 (owner: %s): %v", owner, err))
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

	// 主循环
	for i := 0; i < e.MaxIterations; i++ {
		e.TotalMessagesProcessed.Add(1)
		logs.Info(logs.SYSTEM, fmt.Sprintf("【第%d轮思考开始】", i+1))
		// 调用 internal/llm 包向大模型发送 HTTP 请求
		chatReq := core.ChatRequest{
			Model:    e.ModelName,
			Messages: convertToCoreMessages(messages),
			Tools:    modelTools,
		}
		resp, err := e.Provider.Chat(ctx, chatReq)
		if err != nil {
			return AgentRunResult{
				Content:       fmt.Sprintf("FrostAgent错误：LLM调用失败: %v", err),
				MemoryWritten: memoryWritten,
			}
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

		messages = append(messages, *responseMsg)

		// 是否给出最终答案
		if len(responseMsg.ToolCalls) == 0 {
			logs.Info(logs.SYSTEM, "【智能体给出最终答案】")
			contentStr, _ := responseMsg.Content.(string)
			return AgentRunResult{Content: contentStr, MemoryWritten: memoryWritten}
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
						return AgentRunResult{MemoryWritten: memoryWritten, Silent: true}
					}
					if isMemoryWriteAction(tc, res) {
						memoryWritten = true
					}
				}
			} else {
				toolResult = "FrostAgent错误：工具未找到"
			}

			logs.Info(logs.TOOL, fmt.Sprintf("【工具执行结果】%s", toolResult))

			// 只有校验通过的 send_message 才能触发实际发送。
			if runContext, ok := RunContextFromContext(ctx); tc.Function.Name == "send_message" && toolSucceeded && ok && runContext.SendHook != nil {
				runContext.SendHook(toolResult)
				toolResult = "消息已发送"
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
	}
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
