package llm

import (
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// tempChatToolBlocked reports whether a temporary conversation may not call the
// tool. The memory tool can write, so it is never available. Tools that read
// past conversations are also withheld when the run disabled memory.
func tempChatToolBlocked(name string, t *TempChat) bool {
	if t == nil {
		return false
	}
	switch name {
	case "memory":
		return true
	case "search_chat_history", "read_tool_history":
		return t.NoMemory
	}
	return false
}

// tempChatTools drops the tools a temporary conversation may not use.
func tempChatTools(tools []core.Tool, t *TempChat) []core.Tool {
	allowed := make([]core.Tool, 0, len(tools))
	for _, tool := range tools {
		if !tempChatToolBlocked(tool.Name, t) {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

// EphemeralMessages returns a private copy of the committed history, when
// includeHistory is set, followed by the prompt. Unlike PrepareTurnMessages it
// records nothing: neither the prompt nor the later reply reaches the session.
func (s *SessionContext) EphemeralMessages(includeHistory bool, prompt any) []ChatMessage {
	var messages []ChatMessage
	if includeHistory && s != nil {
		s.mu.Lock()
		messages = cloneMessages(s.History)
		s.unlockCheckpoint()
		// Mirror PrepareTurnMessages: request copies carry no recorded times.
		for i := range messages {
			messages[i].RecordedAt = time.Time{}
		}
	}
	return append(messages, ChatMessage{Role: "user", Content: prompt})
}
