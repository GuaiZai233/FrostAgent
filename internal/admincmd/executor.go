package admincmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/memory"
	"github.com/GuaiZai233/FrostAgent/internal/modelrouter"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/security"
	"strings"
	"time"
)

// CommandContext carries caller and session context for command execution.
type CommandContext struct {
	SessionID    string
	Owner        string
	IsGroup      bool
	CallerUserID string
	RouteScope   modelrouter.Scope
	Reply        func(ctx context.Context, text string, isIntermediate bool) error
	ReplyPrivate func(ctx context.Context, text string) error
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
	if e != nil && e.Engine != nil && e.Engine.Security != nil && cmdCtx.CallerUserID != "" {
		platform := cmdCtx.RouteScope.Platform
		if platform == "" {
			platform = "qq"
		}
		principal, err := security.NewPrincipal(platform, cmdCtx.CallerUserID)
		if err != nil {
			return fmt.Errorf("无效的调用者ID: %w", err)
		}
		if err := e.Engine.Security.CheckAccess(principal); err != nil {
			if errors.Is(err, security.ErrLocked) {
				_ = cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, security.RejectGatewayMsg, err, cmdCtx.SessionID), false)
				return security.ErrLocked
			}
			return fmt.Errorf("安全访问检查失败: %w", err)
		}
	}

	switch cmd.Type {
	case CmdLog:
		return e.executeLog(ctx, cmdCtx, cmd)
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

func (e *Executor) executeLog(ctx context.Context, cmdCtx CommandContext, cmd ParsedCommand) error {
	if e == nil || e.Engine == nil || !IsAdmin(cmdCtx.CallerUserID, e.scope()) {
		return errors.New("log 指令仅允许管理员使用")
	}
	if len(cmd.Args) != 1 {
		return errors.New("log 指令格式错误：log <uuidv4>")
	}
	id, ok := logs.NormalizeErrorUUID(cmd.Args[0])
	if !ok {
		return errors.New("错误 UUID 必须为完整的 UUIDv4")
	}
	entry, found := e.Engine.Log().FindError(id)
	if !found {
		return cmdCtx.Reply(ctx, "未找到当前实例的错误日志，可能已清理、覆盖或重启。", false)
	}
	detail := fmt.Sprintf("Bot 错误信息\n错误 UUID：%s\n时间：%s\n类型：%s\n%s",
		entry.ErrorUUID, entry.Timestamp.Format(time.RFC3339), entry.Category, entry.Content)
	if !cmdCtx.IsGroup {
		return cmdCtx.Reply(ctx, detail, false)
	}
	if cmdCtx.ReplyPrivate == nil {
		return cmdCtx.Reply(ctx, "无法发送私聊日志，请私聊 Bot 后使用 log 指令查询。", false)
	}
	if err := cmdCtx.ReplyPrivate(ctx, detail); err != nil {
		return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.WEBSOCKET, "私聊日志发送失败或未确认送达，请私聊 Bot 后使用 log 指令查询", err, cmdCtx.SessionID), false)
	}
	return cmdCtx.Reply(ctx, "错误日志已私聊发送。", false)
}

func (e *Executor) executeReset(ctx context.Context, cmdCtx CommandContext) error {
	if e == nil || e.Engine == nil || e.Engine.SessionManager == nil {
		return errors.New("会话管理器不可用")
	}
	var errs []error
	if err := e.Engine.SessionManager.ResetSession(cmdCtx.SessionID); err != nil {
		errs = append(errs, err)
	}
	if e.Engine.GroupSummaryStore != nil {
		canonicalID := memory.CanonicalSessionKey(cmdCtx.SessionID)
		if canonicalID != "" {
			if err := e.Engine.GroupSummaryStore.Delete(canonicalID); err != nil {
				errs = append(errs, err)
			}
		}
		if err := e.Engine.GroupSummaryStore.Delete(cmdCtx.SessionID); err != nil {
			errs = append(errs, err)
		}
		for _, alias := range memory.SessionKeyAliases(cmdCtx.SessionID) {
			if err := e.Engine.GroupSummaryStore.Delete(alias); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, "持久化总结清理失败：当前会话已重置", errors.Join(errs...), cmdCtx.SessionID), false)
	}
	return cmdCtx.Reply(ctx, "当前会话已重置。", false)
}

func (e *Executor) executeBan(ctx context.Context, cmdCtx CommandContext, cmd ParsedCommand) error {
	if len(cmd.Args) != 1 {
		return errors.New("ban 指令格式错误，需要指定一个用户ID：ban <userID>")
	}
	rawTarget := strings.TrimSpace(cmd.Args[0])
	if rawTarget == "" {
		return errors.New("ban 指令目标用户ID不能为空")
	}
	platform := cmdCtx.RouteScope.Platform
	if platform == "" {
		platform = "qq"
	}
	targetID, err := CleanTargetID(rawTarget, platform)
	if err != nil {
		return fmt.Errorf("无效的目标用户ID: %w", err)
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
	principal, err := security.NewPrincipal(platform, targetID)
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
	rawTarget := strings.TrimSpace(cmd.Args[0])
	if rawTarget == "" {
		return errors.New("unban 指令目标用户ID不能为空")
	}
	platform := cmdCtx.RouteScope.Platform
	if platform == "" {
		platform = "qq"
	}
	targetID, err := CleanTargetID(rawTarget, platform)
	if err != nil {
		return fmt.Errorf("无效的目标用户ID: %w", err)
	}
	if e.Engine == nil || e.Engine.Security == nil {
		return errors.New("安全控制器不可用")
	}
	principal, err := security.NewPrincipal(platform, targetID)
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
	if e.Engine.ModelRouter != nil && e.Engine.ModelRouter.Snapshot().IsDisabled(modelrouter.WorkloadGroupCompact, cmdCtx.RouteScope) {
		return cmdCtx.Reply(ctx, "上下文压缩功能已被禁用。", false)
	}
	session := e.Engine.SessionManager.GetOrCreate(cmdCtx.SessionID)

	if cmdCtx.IsGroup {
		if e.Engine.GroupCompactor == nil {
			return errors.New("群聊压缩器未启用")
		}
		startSent := make(chan struct{})
		err := e.Engine.GroupCompactor.ForceCompact(
			session,
			cmdCtx.Owner,
			cmdCtx.RouteScope,
			func(compErr error) {
				<-startSent
				bgCtx := context.Background()
				if compErr == nil {
					_ = cmdCtx.Reply(bgCtx, "群聊上下文压缩总结完成。", false)
				} else {
					_ = cmdCtx.Reply(bgCtx, e.Engine.Log().ReportError(logs.SYSTEM, "群聊上下文压缩总结失败", compErr, cmdCtx.SessionID), false)
				}
			},
		)
		if errors.Is(err, llm.ErrAlreadyCompacting) {
			close(startSent)
			return cmdCtx.Reply(ctx, "压缩正在进行中，请勿重复触发。", false)
		}
		if errors.Is(err, llm.ErrNothingToCompact) {
			close(startSent)
			return cmdCtx.Reply(ctx, "当前会话没有需要压缩的内容。", false)
		}
		if err != nil {
			close(startSent)
			return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, "触发群聊上下文压缩失败", err, cmdCtx.SessionID), false)
		}
		replyErr := cmdCtx.Reply(ctx, "已开始群聊上下文压缩总结...", true)
		close(startSent)
		return replyErr
	}

	snapshot, err := session.SnapshotPrivateCompact()
	if errors.Is(err, llm.ErrAlreadyCompacting) {
		return cmdCtx.Reply(ctx, "压缩正在进行中，请勿重复触发。", false)
	}
	if errors.Is(err, llm.ErrNothingToCompact) {
		return cmdCtx.Reply(ctx, "当前会话没有需要压缩的内容。", false)
	}
	if err != nil {
		return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, "触发私聊上下文压缩失败", err, cmdCtx.SessionID), false)
	}

	startSent := make(chan struct{})
	if !e.Engine.Go(func() {
		<-startSent
		e.runPrivateCompact(session, snapshot, cmdCtx)
	}) {
		close(startSent)
		session.CancelPrivateCompact(snapshot)
		return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, "私聊上下文压缩失败：实例未就绪", nil, cmdCtx.SessionID), false)
	}
	replyErr := cmdCtx.Reply(ctx, "已开始私聊上下文压缩总结...", true)
	close(startSent)
	return replyErr
}

func (e *Executor) runPrivateCompact(session *llm.SessionContext, snapshot llm.PrivateCompactSnapshot, cmdCtx CommandContext) {
	if err := e.Engine.CompactPrivateSnapshot(session, snapshot, cmdCtx.RouteScope); err != nil {
		_ = cmdCtx.Reply(context.Background(), e.Engine.Log().ReportError(logs.SYSTEM, "私聊上下文压缩总结失败", err, cmdCtx.SessionID), false)
		return
	}
	_ = cmdCtx.Reply(context.Background(), "私聊上下文压缩总结完成。", false)
}

func (e *Executor) executeReflect(ctx context.Context, cmdCtx CommandContext) error {
	if e == nil || e.Engine == nil || e.Engine.MemoryReflections == nil {
		return errors.New("记忆反思未启用或不可用")
	}

	startSent := make(chan struct{})
	status, started, err := e.Engine.MemoryReflections.Start(
		cmdCtx.Owner,
		func(reflectErr error) {
			<-startSent
			bgCtx := context.Background()
			if reflectErr == nil {
				_ = cmdCtx.Reply(bgCtx, "记忆反思任务已完成。", false)
			} else {
				_ = cmdCtx.Reply(bgCtx, e.Engine.Log().ReportError(logs.SYSTEM, "记忆反思任务失败", reflectErr, cmdCtx.SessionID), false)
			}
		},
	)
	if !started && status.Running {
		close(startSent)
		return cmdCtx.Reply(ctx, "记忆反思任务正在执行中，请勿重复触发。", false)
	}
	if err != nil {
		close(startSent)
		return cmdCtx.Reply(ctx, e.Engine.Log().ReportError(logs.SYSTEM, "触发记忆反思失败", err, cmdCtx.SessionID), false)
	}
	replyErr := cmdCtx.Reply(ctx, "已开始记忆反思任务...", true)
	close(startSent)
	return replyErr
}
