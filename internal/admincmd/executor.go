package admincmd

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/llm"
	"FrostAgent/internal/memory"
	"FrostAgent/internal/modelrouter"
	"FrostAgent/internal/runtimescope"
	"FrostAgent/internal/security"
	"context"
	"errors"
	"fmt"
	"strings"
)

const privateCompactPrompt = `你是当前私聊上下文压缩器，而不是聊天历史记录器。

你的输出是一份短暂的工作记忆，只用于帮助模型理解接下来几轮对话。它不是对话档案，不需要保留完整历史，信息允许被遗忘。
你会收到对话历史记录。请根据对话记录重新生成一份新的当前总结。

核心原则：
- 优先保留当前仍在进行、尚未结束、很可能在接下来几轮继续被引用的内容。
- 已经结束、解决、过时或明显离开当前话题的信息应主动删除。
- 对话的连续性最重要。话题已经转移时，积极遗忘旧话题。
- 控制在约 300～600 个中文字符。
- 只输出新的对话总结正文，不要 Markdown 标题或额外解释。

[对话记录]
{conversation}`

// CommandContext carries caller and session context for command execution.
type CommandContext struct {
	SessionID    string
	Owner        string
	IsGroup      bool
	CallerUserID string
	RouteScope   modelrouter.Scope
	Reply        func(ctx context.Context, text string, isIntermediate bool) error
}

// Executor orchestrates administrator message command execution.
type Executor struct {
	Engine *llm.Engine
}

// NewExecutor creates a new administrator command executor.
func NewExecutor(engine *llm.Engine) *Executor {
	return &Executor{Engine: engine}
}

func (e *Executor) scope() *runtimescope.Scope {
	if e != nil && e.Engine != nil {
		return e.Engine.Scope
	}
	return nil
}

// Execute executes a parsed administrator command.
func (e *Executor) Execute(ctx context.Context, cmdCtx CommandContext, cmd ParsedCommand) error {
	switch cmd.Type {
	case CmdReset:
		return e.executeReset(ctx, cmdCtx)
	case CmdBan:
		return e.executeBan(ctx, cmdCtx, cmd)
	case CmdUnban:
		return e.executeUnban(ctx, cmdCtx, cmd)
	case CmdCompact:
		return e.executeCompact(ctx, cmdCtx)
	case CmdReflect:
		return e.executeReflect(ctx, cmdCtx)
	default:
		return fmt.Errorf("未知指令：%s", cmd.Type)
	}
}

func (e *Executor) executeReset(ctx context.Context, cmdCtx CommandContext) error {
	if e == nil || e.Engine == nil || e.Engine.SessionManager == nil {
		return errors.New("会话管理器不可用")
	}
	e.Engine.SessionManager.ResetSession(cmdCtx.SessionID)
	if e.Engine.GroupSummaryStore != nil {
		canonicalID := memory.CanonicalSessionKey(cmdCtx.SessionID)
		if canonicalID != "" {
			_ = e.Engine.GroupSummaryStore.Delete(canonicalID)
		}
		_ = e.Engine.GroupSummaryStore.Delete(cmdCtx.SessionID)
		for _, alias := range memory.SessionKeyAliases(cmdCtx.SessionID) {
			_ = e.Engine.GroupSummaryStore.Delete(alias)
		}
	}
	return cmdCtx.Reply(ctx, "当前会话已重置。", false)
}

func (e *Executor) executeBan(ctx context.Context, cmdCtx CommandContext, cmd ParsedCommand) error {
	if len(cmd.Args) != 1 {
		return errors.New("ban 指令格式错误，需要指定一个用户ID：ban <userID>")
	}
	targetID := strings.TrimSpace(cmd.Args[0])
	if targetID == "" {
		return errors.New("ban 指令目标用户ID不能为空")
	}
	if targetID == cmdCtx.CallerUserID {
		return cmdCtx.Reply(ctx, "无法封禁当前调用者账号。", false)
	}
	scope := e.scope()
	if IsAdmin(targetID, scope) {
		return cmdCtx.Reply(ctx, "无法封禁管理员账号。", false)
	}
	if e.Engine == nil || e.Engine.Security == nil {
		return errors.New("安全控制器不可用")
	}
	principal, err := security.NewPrincipal("qq", targetID)
	if err != nil {
		return fmt.Errorf("无效的用户ID: %w", err)
	}
	if err := e.Engine.Security.Lock(principal, "Admin ban"); err != nil {
		return fmt.Errorf("封禁失败: %w", err)
	}
	return cmdCtx.Reply(ctx, fmt.Sprintf("已成功封禁用户 %s。", targetID), false)
}

func (e *Executor) executeUnban(ctx context.Context, cmdCtx CommandContext, cmd ParsedCommand) error {
	if len(cmd.Args) != 1 {
		return errors.New("unban 指令格式错误，需要指定一个用户ID：unban <userID>")
	}
	targetID := strings.TrimSpace(cmd.Args[0])
	if targetID == "" {
		return errors.New("unban 指令目标用户ID不能为空")
	}
	if e.Engine == nil || e.Engine.Security == nil {
		return errors.New("安全控制器不可用")
	}
	principal, err := security.NewPrincipal("qq", targetID)
	if err != nil {
		return fmt.Errorf("无效的用户ID: %w", err)
	}
	if err := e.Engine.Security.Unlock(principal); err != nil {
		return fmt.Errorf("解封失败: %w", err)
	}
	return cmdCtx.Reply(ctx, fmt.Sprintf("已成功解封用户 %s。", targetID), false)
}

func (e *Executor) executeCompact(ctx context.Context, cmdCtx CommandContext) error {
	if e == nil || e.Engine == nil || e.Engine.SessionManager == nil {
		return errors.New("会话管理器不可用")
	}
	session := e.Engine.SessionManager.GetOrCreate(cmdCtx.SessionID)

	if cmdCtx.IsGroup {
		if e.Engine.GroupCompactor == nil {
			return errors.New("群聊压缩器未启用")
		}
		err := e.Engine.GroupCompactor.ForceCompact(
			session,
			cmdCtx.Owner,
			cmdCtx.RouteScope,
			func(compErr error) {
				bgCtx := context.Background()
				if compErr == nil {
					_ = cmdCtx.Reply(bgCtx, "群聊上下文压缩总结完成。", false)
				} else {
					_ = cmdCtx.Reply(bgCtx, fmt.Sprintf("群聊上下文压缩总结失败：%v", compErr), false)
				}
			},
		)
		if errors.Is(err, llm.ErrAlreadyCompacting) {
			return cmdCtx.Reply(ctx, "压缩正在进行中，请勿重复触发。", false)
		}
		if errors.Is(err, llm.ErrNothingToCompact) {
			return cmdCtx.Reply(ctx, "当前会话没有需要压缩的内容。", false)
		}
		if err != nil {
			return cmdCtx.Reply(ctx, fmt.Sprintf("触发群聊上下文压缩失败：%v", err), false)
		}
		return cmdCtx.Reply(ctx, "已开始群聊上下文压缩总结...", true)
	}

	snapshot, err := session.SnapshotPrivateCompact()
	if errors.Is(err, llm.ErrAlreadyCompacting) {
		return cmdCtx.Reply(ctx, "压缩正在进行中，请勿重复触发。", false)
	}
	if errors.Is(err, llm.ErrNothingToCompact) {
		return cmdCtx.Reply(ctx, "当前会话没有需要压缩的内容。", false)
	}
	if err != nil {
		return cmdCtx.Reply(ctx, fmt.Sprintf("触发私聊上下文压缩失败：%v", err), false)
	}

	if err := cmdCtx.Reply(ctx, "已开始私聊上下文压缩总结...", true); err != nil {
		// Intermediate reply send failure does not prevent background compaction
	}

	if !e.Engine.Go(func() { e.runPrivateCompact(session, snapshot, cmdCtx) }) {
		session.CancelPrivateCompact()
		return cmdCtx.Reply(ctx, "实例未就绪，无法执行私聊压缩。", false)
	}
	return nil
}

func (e *Executor) runPrivateCompact(session *llm.SessionContext, snapshot llm.PrivateCompactSnapshot, cmdCtx CommandContext) {
	bgCtx := e.Engine.Context()
	model := e.Engine.ModelName
	if e.Engine.ModelRouter != nil {
		if target, err := e.Engine.ModelRouter.Snapshot().Resolve(modelrouter.WorkloadDialogue, cmdCtx.RouteScope); err == nil {
			model = target.UpstreamModel
		}
	}
	var b strings.Builder
	for _, msg := range snapshot.Messages {
		fmt.Fprintf(&b, "[%s]: %v\n", msg.Role, msg.Content)
	}
	conversation := b.String()
	request := core.ChatRequest{
		Model: model,
		Messages: []core.ChatMessage{{
			Role:    core.RoleUser,
			Content: strings.Replace(privateCompactPrompt, "{conversation}", conversation, 1),
		}},
		MaxTokens:   1024,
		Temperature: 0.2,
		Route: core.RouteContext{
			Platform: cmdCtx.RouteScope.Platform,
			GroupID:  cmdCtx.RouteScope.GroupID,
		},
	}
	response, err := e.Engine.Provider.Chat(bgCtx, request)
	if err != nil {
		session.CancelPrivateCompact()
		_ = cmdCtx.Reply(context.Background(), fmt.Sprintf("私聊上下文压缩总结失败：%v", err), false)
		return
	}
	summary, ok := response.Message.Content.(string)
	summary = strings.TrimSpace(summary)
	if !ok || summary == "" {
		session.CancelPrivateCompact()
		_ = cmdCtx.Reply(context.Background(), "私聊上下文压缩总结失败：模型返回了空总结", false)
		return
	}
	if !session.CommitPrivateCompact(snapshot, summary) {
		_ = cmdCtx.Reply(context.Background(), "已丢弃失效的私聊上下文压缩总结（会话已被重置或变更）。", false)
		return
	}
	_ = cmdCtx.Reply(context.Background(), "私聊上下文压缩总结完成。", false)
}

func (e *Executor) executeReflect(ctx context.Context, cmdCtx CommandContext) error {
	if e == nil || e.Engine == nil || e.Engine.MemoryReflections == nil {
		return errors.New("记忆反思未启用或不可用")
	}

	status, started, err := e.Engine.MemoryReflections.Start(
		cmdCtx.Owner,
		func(reflectErr error) {
			bgCtx := context.Background()
			if reflectErr == nil {
				_ = cmdCtx.Reply(bgCtx, "记忆反思任务已完成。", false)
			} else {
				_ = cmdCtx.Reply(bgCtx, fmt.Sprintf("记忆反思任务失败：%v", reflectErr), false)
			}
		},
	)
	if !started && status.Running {
		return cmdCtx.Reply(ctx, "记忆反思任务正在执行中，请勿重复触发。", false)
	}
	if err != nil {
		return cmdCtx.Reply(ctx, fmt.Sprintf("触发记忆反思失败：%v", err), false)
	}
	return cmdCtx.Reply(ctx, "已开始记忆反思任务...", true)
}
