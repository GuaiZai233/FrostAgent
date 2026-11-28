package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/security"
)

// deliverStickerPair prepares both tools before any platform send. A single
// ordered message chain makes a failed sticker selection or send fail the pair.
func (e *Engine) deliverStickerPair(ctx context.Context, run *RunContext, textCall, stickerCall ToolCall) (textResult, stickerResult ChatMessage, historyErr error) {
	textResult = ChatMessage{Role: "tool", ToolCallID: textCall.ID}
	stickerResult = ChatMessage{Role: "tool", ToolCallID: stickerCall.ID}
	finish := func(text, sticker string) (ChatMessage, ChatMessage, error) {
		textResult.Content, stickerResult.Content = text, sticker
		return textResult, stickerResult, historyErr
	}
	var textOutput, stickerOutput core.ToolExecutionResult
	defer func() {
		if runCanceled(ctx) || historyErr != nil {
			return
		}
		textResult.ToolHistoryRefID, stickerResult.ToolHistoryRefID = textOutput.HistoryRefID, stickerOutput.HistoryRefID
		content, err := e.finishToolHistory(ctx, textOutput, contentToText(textResult.Content), textOutput.HistoryRestricted)
		if err != nil {
			historyErr = err
			return
		}
		textResult.Content = content
		content, err = e.finishToolHistory(ctx, stickerOutput, contentToText(stickerResult.Content), stickerOutput.HistoryRestricted)
		if err != nil {
			historyErr = err
			return
		}
		stickerResult.Content = content
	}()
	var textError, stickerError string
	textOutput, textError, historyErr = e.prepareStickerPairTool(ctx, run, textCall)
	if historyErr != nil {
		return finish("", "")
	}
	if textError != "" {
		return finish(textError, "配对发送未执行：文字准备失败。")
	}
	stickerOutput, stickerError, historyErr = e.prepareStickerPairTool(ctx, run, stickerCall)
	if historyErr != nil {
		return finish("", "")
	}
	if stickerError != "" {
		return finish("文字未发送：配对贴纸准备失败。", stickerError)
	}
	payload, err := combineStickerPairPayloads(textOutput.MessagePayload, stickerOutput.MessagePayload)
	if err != nil {
		reply := e.Log().ReportError(logs.TOOL, "配对消息发送失败：消息无效", err, run.SessionID)
		return finish(reply, reply)
	}
	result, _ := e.deliverToolResult(ctx, core.ToolExecutionResult{MessagePayload: payload}, payload)
	return finish(result, result)
}

func (e *Engine) prepareStickerPairTool(ctx context.Context, run *RunContext, call ToolCall) (core.ToolExecutionResult, string, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolExecutionResult{}, fmt.Sprintf("消息发送取消：%v", err), nil
	}
	if blocked, decision := e.securityEvaluate(*run, security.StageToolArgument, security.SourceToolArgument, call.Function.Arguments, call.Function.Name); blocked || decision.Action == security.WatchdogFilter {
		if decision.IsFailure {
			return core.ToolExecutionResult{}, security.ReportRejection(e.Log(), "安全审查失败：服务暂时不可用，该工具调用已被阻止", decision, run.SessionID), nil
		}
		return core.ToolExecutionResult{}, security.ReportRejection(e.Log(), "安全审查拦截：该工具调用已被阻止", decision, run.SessionID), nil
	} else if decision.Action == security.WatchdogWarn {
		mergeSecurityNotice(run, decision.WarningNotice)
	}
	tool, exists := e.findToolExecutor(call.Function.Name)
	if !exists {
		return core.ToolExecutionResult{}, e.Log().ReportError(logs.TOOL, "工具调用失败：工具未找到", fmt.Errorf("tool=%s", call.Function.Name), run.SessionID), nil
	}
	e.Log().InfoWithConsoleSummary(logs.TOOL, formatToolCallLog(call.Function.Name, call.Function.Arguments), "【智能体调用工具】"+call.Function.Name, run.SessionID)
	output, err := e.executeRecordedTool(ctx, tool, call)
	if errors.Is(err, errToolHistoryStorage) {
		return output, "", err
	}
	if runCanceled(ctx) {
		return output, "消息发送取消：执行已被打断", nil
	}
	if err != nil {
		return output, e.Log().ReportError(logs.TOOL, "工具执行失败", fmt.Errorf("tool=%s: %w", call.Function.Name, err), run.SessionID), nil
	}
	if len(output.Content) > MaxToolOutputBytes || len(output.MessagePayload) > MaxToolOutputBytes {
		// This early return skips result inspection; archived originals stay quarantined.
		output.HistoryRestricted = true
		return output, e.Log().ReportError(logs.TOOL, "工具执行失败：工具结果过大，配对消息未发送", nil, run.SessionID), nil
	}
	securityContent := output.Content
	if output.MessagePayload != "" && output.MessagePayload != output.Content {
		securityContent += "\n" + output.MessagePayload
	}
	if blocked, decision := e.securityEvaluate(*run, security.StageToolResult, security.SourceToolResult, securityContent, call.Function.Name); blocked || decision.Action == security.WatchdogFilter {
		output.HistoryRestricted = true
		if decision.IsFailure {
			return output, security.ReportRejection(e.Log(), "安全审查失败：服务暂时不可用，外部工具结果已隔离", decision, run.SessionID), nil
		}
		if decision.Action == security.WatchdogFilter && decision.SanitizedContent != "" {
			return output, decision.SanitizedContent, nil
		}
		return output, security.ReportRejection(e.Log(), "安全审查拦截：外部工具结果已隔离", decision, run.SessionID), nil
	} else if decision.Action == security.WatchdogWarn {
		mergeSecurityNotice(run, decision.WarningNotice)
	}
	e.Log().InfoWithConsoleSummary(logs.TOOL, formatToolResultLog(call.Function.Name, output.Content), "【工具执行结果】...")
	if output.MessagePayload == "" {
		if output.Content != "" {
			return output, output.Content, nil
		}
		return output, "工具未准备可发送消息。", nil
	}
	return output, "", nil
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
