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

// runForcedMentionSleep performs the backend-owned wait and records it in tool
// history like a model-issued time_sleep. It deliberately does not add a
// synthetic assistant tool call to the model conversation: providers such as
// Gemini require a thought signature for model-generated function calls, which a
// backend-issued call cannot carry. Messages that arrived during the wait reach
// the model through the adapter's RefreshPrompt, as for a model-issued sleep.
//
// ran reports whether the wait completed (including a Timeout result). When
// time_sleep is not registered the wait is skipped and ran is false. A non-nil
// abort must be returned as the run result.
func (e *Engine) runForcedMentionSleep(ctx context.Context) (ran bool, abort *AgentRunResult) {
	// Re-check interruption immediately before anything is persisted: an
	// interrupted turn must not leave a tool record for a wait that never started.
	if runCanceled(ctx) {
		return false, &AgentRunResult{Silent: true, SilenceReason: "canceled", Error: context.Canceled}
	}
	tool, ok := e.findToolExecutor(timesleep.ToolName)
	if !ok {
		e.Log().WarnWithConsoleSummary(logs.TOOL, "后端强制挂起已跳过：time_sleep 工具未启用", "强制挂起已跳过")
		return false, nil
	}
	call := ToolCall{
		ID:   "forced_time_sleep_" + uuid.NewString(),
		Type: "function",
		Function: ToolCallFunction{
			Name:      timesleep.ToolName,
			Arguments: fmt.Sprintf(`{"seconds":%d}`, ForcedMentionSleepSeconds),
		},
	}
	e.Log().InfoWithConsoleSummary(logs.TOOL, fmt.Sprintf("后端强制挂起 %d 秒：引用 Bot 消息且仅 @Bot", ForcedMentionSleepSeconds), "【后端强制挂起】")

	execution, err := e.executeRecordedTool(ctx, tool, call)
	if errors.Is(err, errToolHistoryStorage) {
		return false, &AgentRunResult{Error: err}
	}
	if runCanceled(ctx) {
		return false, &AgentRunResult{Silent: true, SilenceReason: "canceled", Error: context.Canceled}
	}
	content := execution.Content
	if err != nil {
		content = "Timeout"
		if !errors.Is(err, timesleep.ErrTimeout) {
			content = fmt.Sprintf("后端强制挂起失败: %v", err)
			e.Log().WarnWithConsoleSummary(logs.TOOL, content, "强制挂起失败")
		}
	}
	if _, archiveErr := e.finishToolHistory(ctx, execution, content, false); archiveErr != nil {
		return false, &AgentRunResult{Error: archiveErr}
	}
	return true, nil
}
