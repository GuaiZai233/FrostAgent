package llm

import (
	"context"
	"errors"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

// staySilentForInboundSticker supplies one terminal tool decision without a
// provider request. Adapters consume the same result as a model's stay_silent.
func (e *Engine) staySilentForInboundSticker(ctx context.Context, messages []ChatMessage, run RunContext) AgentRunResult {
	if runCanceled(ctx) || run.Turn.Interrupted() || (run.Context != nil && run.Context.Err() != nil) {
		return AgentRunResult{Silent: true, SilenceReason: "canceled", Error: context.Canceled}
	}
	if run.Epoch > 0 && e.SessionManager != nil {
		if session, ok := e.SessionManager.Get(run.SessionID); ok {
			if session, ok := session.(*SessionContext); ok && session.Epoch() != run.Epoch {
				return AgentRunResult{Silent: true, SilenceReason: "epoch_changed", Error: errors.New("session epoch invalidated")}
			}
		}
	}
	const callID = "backend_sticker_stay_silent"
	transcript := append([]ChatMessage(nil), messages...)
	transcript = append(transcript,
		ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: callID, Type: "function", Function: ToolCallFunction{Name: StaySilentToolName, Arguments: "{}"}}}},
		ChatMessage{Role: "tool", ToolCallID: callID, Content: "已选择保持沉默"},
	)
	run.Turn.saveTranscript(transcript)
	e.Log().InfoWithConsoleSummary(logs.SYSTEM, "【纯表情包主动回复：后端代行 stay_silent】", "【纯表情包主动回复：后端代行 stay_silent】")
	return AgentRunResult{Silent: true, StaySilentCalled: true, SilenceReason: "stay_silent", Transcript: transcript, MemoryWritten: run.Turn.memoryState(false)}
}
