package llm

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
	"FrostAgent/internal/security"
	"context"
	"encoding/json"
	"fmt"
)

// deliverStickerPair prepares both tools before any platform send. A single
// ordered message chain makes a failed sticker selection or send fail the pair.
func (e *Engine) deliverStickerPair(ctx context.Context, run *RunContext, textCall, stickerCall ToolCall) (string, string) {
	textOutput, textError := e.prepareStickerPairTool(ctx, run, textCall)
	if textError != "" {
		return textError, "配对发送未执行：文字准备失败。"
	}
	stickerOutput, stickerError := e.prepareStickerPairTool(ctx, run, stickerCall)
	if stickerError != "" {
		return "文字未发送：配对贴纸准备失败。", stickerError
	}
	payload, err := combineStickerPairPayloads(textOutput.MessagePayload, stickerOutput.MessagePayload)
	if err != nil {
		return "文字未发送：配对贴纸消息无效。", fmt.Sprintf("表情包未发送：%v", err)
	}
	result, _ := e.deliverToolResult(ctx, core.ToolExecutionResult{MessagePayload: payload}, payload)
	return result, result
}

func (e *Engine) prepareStickerPairTool(ctx context.Context, run *RunContext, call ToolCall) (core.ToolExecutionResult, string) {
	if err := ctx.Err(); err != nil {
		return core.ToolExecutionResult{}, fmt.Sprintf("消息发送取消：%v", err)
	}
	if blocked, decision := e.securityEvaluate(*run, security.StageToolArgument, security.SourceToolArgument, call.Function.Arguments, call.Function.Name); blocked || decision.Action == security.WatchdogFilter {
		if decision.IsFailure {
			return core.ToolExecutionResult{}, "FrostAgent安全控制：安全审查服务暂时不可用，该工具调用已被阻止。"
		}
		return core.ToolExecutionResult{}, "FrostAgent安全控制：该工具调用已被阻止。"
	} else if decision.Action == security.WatchdogWarn {
		mergeSecurityNotice(run, decision.WarningNotice)
	}
	tool, exists := e.findToolExecutor(call.Function.Name)
	if !exists {
		return core.ToolExecutionResult{}, "FrostAgent错误：工具未找到"
	}
	e.Log().InfoWithConsoleSummary(logs.TOOL, formatToolCallLog(call.Function.Name, call.Function.Arguments), "【智能体调用工具】"+call.Function.Name)
	output, err := executeInterruptibleTool(ctx, tool, call)
	if runCanceled(ctx) {
		return core.ToolExecutionResult{}, "消息发送取消：执行已被打断"
	}
	if err != nil {
		return core.ToolExecutionResult{}, fmt.Sprintf("FrostAgent错误：工具执行失败: %v", err)
	}
	if len(output.Content) > MaxToolOutputBytes || len(output.MessagePayload) > MaxToolOutputBytes {
		return core.ToolExecutionResult{}, "FrostAgent错误：工具结果过大，配对消息未发送。"
	}
	securityContent := output.Content
	if output.MessagePayload != "" && output.MessagePayload != output.Content {
		securityContent += "\n" + output.MessagePayload
	}
	if blocked, decision := e.securityEvaluate(*run, security.StageToolResult, security.SourceToolResult, securityContent, call.Function.Name); blocked || decision.Action == security.WatchdogFilter {
		if decision.IsFailure {
			return core.ToolExecutionResult{}, "FrostAgent安全控制：安全审查服务暂时不可用，外部工具结果已隔离。"
		}
		if decision.Action == security.WatchdogFilter && decision.SanitizedContent != "" {
			return core.ToolExecutionResult{}, decision.SanitizedContent
		}
		return core.ToolExecutionResult{}, "FrostAgent安全控制：外部工具结果已隔离。"
	} else if decision.Action == security.WatchdogWarn {
		mergeSecurityNotice(run, decision.WarningNotice)
	}
	e.Log().InfoWithConsoleSummary(logs.TOOL, formatToolResultLog(call.Function.Name, output.Content), "【工具执行结果】...")
	if output.MessagePayload == "" {
		if output.Content != "" {
			return core.ToolExecutionResult{}, output.Content
		}
		return core.ToolExecutionResult{}, "工具未准备可发送消息。"
	}
	return output, ""
}

func combineStickerPairPayloads(textPayload, stickerPayload string) (string, error) {
	var textMessages, stickerMessages struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(textPayload), &textMessages); err != nil || len(textMessages.Messages) == 0 {
		return "", fmt.Errorf("文字消息无效")
	}
	if err := json.Unmarshal([]byte(stickerPayload), &stickerMessages); err != nil || len(stickerMessages.Messages) != 1 || !hasStickerImage(stickerPayload) {
		return "", fmt.Errorf("贴纸消息无效")
	}
	combined := struct {
		Messages []json.RawMessage `json:"messages"`
	}{Messages: append(textMessages.Messages, stickerMessages.Messages...)}
	data, err := json.Marshal(combined)
	if err != nil || len(data) > MaxToolOutputBytes {
		return "", fmt.Errorf("配对消息过大或无法序列化")
	}
	return string(data), nil
}
