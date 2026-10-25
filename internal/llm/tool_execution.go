package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
)

type contextualToolExecutor interface {
	ExecuteContext(ctx context.Context, args string) (string, error)
}

// executeToolResult adapts legacy executors at one compatibility boundary.
// Builtin tools implement StructuredToolExecutor and bypass text inference.
func executeToolResult(ctx context.Context, tool ToolExecutor, call ToolCall) (core.ToolExecutionResult, error) {
	if structured, ok := tool.(core.StructuredToolExecutor); ok {
		return structured.ExecuteResult(ctx, call.Function.Arguments)
	}
	var content string
	var err error
	if contextual, ok := tool.(contextualToolExecutor); ok {
		content, err = contextual.ExecuteContext(ctx, call.Function.Arguments)
	} else {
		content, err = tool.Execute(call.Function.Arguments)
	}
	result := core.ToolExecutionResult{Content: content}
	if err == nil {
		result.MemoryWritten = isMemoryWriteAction(call, content)
		if looksLikeMessagePayload(content) {
			result.MessagePayload = content
		}
	}
	return result, err
}

// Legacy memory executors retain their existing contract while migrating to
// StructuredToolExecutor. This is never used for the builtin memory tool.
func isMemoryWriteAction(call ToolCall, content string) bool {
	return isMemoryWriteRequest(call) && content == "记忆已写入"
}

func isMemoryWriteRequest(call ToolCall) bool {
	if call.Function.Name != "memory" {
		return false
	}
	var params struct {
		Action string `json:"action"`
	}
	return json.Unmarshal([]byte(call.Function.Arguments), &params) == nil && params.Action == "write"
}

func looksLikeMessagePayload(content string) bool {
	var probe struct {
		Messages []json.RawMessage `json:"messages"`
	}
	return json.Unmarshal([]byte(content), &probe) == nil && len(probe.Messages) > 0
}

// deliverToolResult owns pending message delivery and session validity checks.
// Call only after tool-result security inspection permits delivery.
func (e *Engine) deliverToolResult(ctx context.Context, result core.ToolExecutionResult, content string) (string, bool) {
	run, ok := RunContextFromContext(ctx)
	if !ok || run.SendHook == nil || result.MessagePayload == "" || len(result.MessagePayload) > MaxToolOutputBytes {
		return content, false
	}
	if runCanceled(ctx) {
		return "消息发送取消：会话已取消", false
	}
	if run.SessionID != "" && run.Epoch > 0 && e.SessionManager != nil {
		if current, ok := e.SessionManager.Get(run.SessionID); ok {
			if session, ok := current.(*SessionContext); ok && session.Epoch() != run.Epoch {
				return "消息发送取消：会话已重置", false
			}
		}
	}
	if err := awaitDelivery(ctx, func() error {
		err := run.SendHook(result.MessagePayload)
		if err == nil && !run.Mock {
			e.RecordBotMessage(hasStickerImage(result.MessagePayload))
		}
		return err
	}); err != nil {
		return fmt.Sprintf("消息发送失败：%v", err), false
	}
	return "消息已发送", true
}
