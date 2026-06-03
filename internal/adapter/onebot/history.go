package onebot

import (
	"sync"

	"FrostAgent/internal/llm"
)

type messageHistory struct {
	mu    sync.RWMutex
	limit int
	data  map[string][]llm.ChatMessage
}

func newMessageHistory(limit int) *messageHistory {
	if limit <= 0 {
		limit = llm.DefaultMaxMessages
	}
	return &messageHistory{
		limit: limit,
		data:  make(map[string][]llm.ChatMessage),
	}
}

func (h *messageHistory) Append(key string, msg llm.ChatMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.data[key] = append(h.data[key], msg)
	h.data[key] = llm.TrimMessages(h.data[key], h.limit)
}

func (h *messageHistory) Messages(key string) []llm.ChatMessage {
	h.mu.RLock()
	defer h.mu.RUnlock()

	messages := h.data[key]
	copied := make([]llm.ChatMessage, len(messages))
	copy(copied, messages)
	return copied
}
