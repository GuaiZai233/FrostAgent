package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/chathistory"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/toolhistory"
	"github.com/google/uuid"
)

var errToolHistoryStorage = errors.New("工具历史存储失败，已停止执行；请检查操作状态，勿自动重复执行")

// ToolHistoryScope derives archive authority exclusively from trusted adapter state.
func ToolHistoryScope(run RunContext) (chathistory.Scope, bool) {
	if run.Mock || run.ActorPlatform == "" || run.ActorUserID == "" {
		return chathistory.Scope{}, false
	}
	scope := chathistory.Scope{Platform: run.ActorPlatform, MessageType: "private", TargetID: run.ActorUserID}.Canonical()
	if scope.Platform == "system" {
		// Timer-triggered group work has its own system identity. Only the trusted
		// awareness route may supply its conversation scope, never a member actor.
		scope = (chathistory.Scope{Platform: run.RouteScope.Platform, MessageType: "group", TargetID: run.RouteScope.GroupID}).Canonical()
		if !run.Awareness || !run.Proactive || strings.TrimSpace(run.RouteScope.Platform) == "" || scope.Platform == "system" || scope.Validate() != nil ||
			run.ActorUserID != run.SessionID || run.SessionID != AwarenessTargetFor(scope.Platform, "group", scope.TargetID).SessionID {
			return chathistory.Scope{}, false
		}
		return scope, true
	}
	if run.RouteScope.GroupID != "" {
		if (chathistory.Scope{Platform: run.RouteScope.Platform}).Canonical().Platform != scope.Platform {
			return chathistory.Scope{}, false
		}
		scope.MessageType, scope.TargetID = "group", run.RouteScope.GroupID
	}
	return scope, scope.Validate() == nil
}

type recordedTool struct {
	ToolExecutor
	execute func(context.Context) (core.ToolExecutionResult, error)
}

func (t recordedTool) ExecuteResult(ctx context.Context, _ string) (core.ToolExecutionResult, error) {
	return t.execute(ctx)
}

func (e *Engine) saveToolHistory(ctx context.Context, record toolhistory.Record) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := e.ToolHistory.Save(writeCtx, record); err != nil {
		e.PauseOnStorageError(fmt.Errorf("工具历史写入失败: %w", err))
		return errToolHistoryStorage
	}
	return nil
}

func (e *Engine) executeRecordedTool(ctx context.Context, tool ToolExecutor, call ToolCall) (core.ToolExecutionResult, error) {
	run, _ := RunContextFromContext(ctx)
	scope, allowed := ToolHistoryScope(run)
	if e.ToolHistory == nil || !allowed {
		return executeInterruptibleTool(ctx, tool, call)
	}
	runID := run.ToolHistoryRunID
	if runID == "" {
		runID = uuid.NewString()
	}
	record := toolhistory.Record{Scope: scope, RefID: toolhistory.NewRef(), RunID: runID,
		SourceMessageID: run.MemorySource.MessageID, ActorID: run.ActorUserID, Tool: call.Function.Name,
		ArgumentsRaw: call.Function.Arguments, OccurredAt: time.Now().UTC(), State: "running", Restricted: true}
	if err := e.saveToolHistory(ctx, record); err != nil {
		return core.ToolExecutionResult{}, err
	}
	var claimed atomic.Bool
	wrapped := recordedTool{ToolExecutor: tool, execute: func(execCtx context.Context) (core.ToolExecutionResult, error) {
		if runCanceled(execCtx) || !claimed.CompareAndSwap(false, true) {
			return core.ToolExecutionResult{}, context.Canceled
		}
		output, execErr := executeToolResult(execCtx, tool, call)
		var envelope struct {
			IsError bool `json:"isError"`
		}
		_ = json.Unmarshal([]byte(output.RawResult), &envelope)
		output.HistoryRestricted = output.HistoryRestricted || execErr != nil || envelope.IsError
		record.ResultRaw, record.MCPResultRaw = output.Content, output.RawResult
		record.State, record.CompletedAt = "executed", time.Now().UTC()
		if execErr != nil {
			record.Error = execErr.Error()
		}
		if err := e.saveToolHistory(execCtx, record); err != nil {
			return output, err
		}
		return output, execErr
	}}
	output, err := executeInterruptibleTool(ctx, wrapped, call)
	// Cancellation can win while the initial write is in flight or before the
	// worker starts. Claim the pending record before discarding it so a late
	// worker cannot execute without its archive. Started tools own their record.
	if claimed.CompareAndSwap(false, true) {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if discardErr := e.ToolHistory.DiscardUnstarted(writeCtx, scope, record.RefID); discardErr != nil {
			e.PauseOnStorageError(fmt.Errorf("未启动的工具历史清理失败: %w", discardErr))
			return output, errToolHistoryStorage
		}
		return output, err
	}
	output.HistoryRefID = record.RefID
	return output, err
}

// finishToolHistory captures the actual model result after inspection and delivery.
func (e *Engine) finishToolHistory(ctx context.Context, output core.ToolExecutionResult, content string, restricted bool) (string, error) {
	if output.HistoryRefID == "" || e.ToolHistory == nil {
		return content, nil
	}
	run, _ := RunContextFromContext(ctx)
	scope, allowed := ToolHistoryScope(run)
	if !allowed {
		return content, nil
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	record, err := e.ToolHistory.Read(readCtx, scope, output.HistoryRefID)
	if err != nil {
		e.PauseOnStorageError(err)
		return "", errToolHistoryStorage
	}
	content += "\n(ref_id: " + record.RefID + ")"
	record.ModelResultRaw, record.Restricted, record.State = content, restricted || output.HistoryRestricted, "complete"
	if err := e.saveToolHistory(ctx, record); err != nil {
		return "", err
	}
	return content, nil
}

// Keep native call/result pairs intact while bounding their archived bodies.
func compactArchivedToolMessages(messages []ChatMessage) {
	remaining := MaxToolOutputBytes
	newest := true
	for i := len(messages) - 1; i >= 0; i-- {
		msg := &messages[i]
		if msg.Role != "tool" || !toolhistory.ValidRef(msg.ToolHistoryRefID) {
			continue
		}
		body := contentToText(msg.Content)
		if newest {
			newest = false
			remaining = max(0, remaining-len(body))
			continue
		}
		if len(body) <= remaining {
			remaining -= len(body)
			continue
		}
		msg.Content = "【历史工具结果已归档，请用 read_tool_history 读取原文】 (ref_id: " + msg.ToolHistoryRefID + ")"
	}
}
