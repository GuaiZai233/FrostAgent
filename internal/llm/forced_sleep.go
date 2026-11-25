package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
	"github.com/GuaiZai233/FrostAgent/internal/timesleep"
	"github.com/google/uuid"
)

// ForcedMentionSleepSeconds is the fixed wait the backend applies before the
// model sees a group message that only @mentions the bot while replying to one
// of the bot's own messages. The model neither chooses nor can skip this wait.
const ForcedMentionSleepSeconds = 10

// runForcedMentionSleep executes time_sleep on the model's behalf and records the
// exchange in messages exactly as a model-issued call would be recorded, so
// history, tool activity and interruption handling stay identical.
//
// executed reports whether the exchange was recorded. When time_sleep is not
// registered (for example disabled by an administrator), nothing is recorded and
// the turn proceeds without the wait. A non-nil abort must be returned as the run result.
func (e *Engine) runForcedMentionSleep(ctx context.Context, runCtx RunContext, messages []ChatMessage, startedTools map[string]bool) (updated []ChatMessage, executed bool, abort *AgentRunResult) {
	tool, ok := e.findToolExecutor(timesleep.ToolName)
	if !ok {
		e.Log().WarnWithConsoleSummary(logs.TOOL, "后端强制挂起已跳过：time_sleep 工具未启用", "强制挂起已跳过")
		return messages, false, nil
	}
	call := ToolCall{
		ID:   "forced_time_sleep_" + uuid.NewString(),
		Type: "function",
		Function: ToolCallFunction{
			Name:      timesleep.ToolName,
			Arguments: fmt.Sprintf(`{"seconds":%d}`, ForcedMentionSleepSeconds),
		},
	}
	messages = append(messages, ChatMessage{Role: "assistant", ToolCalls: []ToolCall{call}})
	startedTools[call.ID] = true
	e.Log().InfoWithConsoleSummary(logs.TOOL, fmt.Sprintf("后端强制挂起 %d 秒：引用 Bot 消息且仅 @Bot", ForcedMentionSleepSeconds), "【后端强制挂起】")

	execution, err := e.executeRecordedTool(ctx, tool, call)
	if errors.Is(err, errToolHistoryStorage) {
		return messages, false, &AgentRunResult{Error: err}
	}
	if runCanceled(ctx) {
		return messages, false, &AgentRunResult{Silent: true, SilenceReason: "canceled", Error: ctx.Err()}
	}

	var toolResult string
	switch {
	case err == nil:
		toolResult = execution.Content
	case errors.Is(err, timesleep.ErrTimeout):
		toolResult = e.Log().ReportError(logs.TOOL, "Timeout", err, runCtx.SessionID)
	default:
		toolResult = e.Log().ReportError(logs.TOOL, "工具执行失败", fmt.Errorf("tool=%s: %w", call.Function.Name, err), runCtx.SessionID)
	}
	toolResult, archiveErr := e.finishToolHistory(ctx, execution, toolResult, false)
	if archiveErr != nil {
		return messages, false, &AgentRunResult{Error: archiveErr}
	}
	messages = append(messages, ChatMessage{Role: "tool", Content: toolResult, ToolCallID: call.ID, ToolHistoryRefID: execution.HistoryRefID})
	return messages, true, nil
}
