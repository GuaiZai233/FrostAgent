package logs

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// MessageRefPrefix marks identifiers that point at a message the bot delivered.
const MessageRefPrefix = "fa_msgid_"

// NewMessageRefID returns a fresh fa_msgid_<uuidv4> identifier.
func NewMessageRefID() string { return MessageRefPrefix + uuid.NewString() }

// NormalizeMessageRefID accepts only the canonical fa_msgid_<uuidv4> form.
func NormalizeMessageRefID(value string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(value), MessageRefPrefix)
	if !ok {
		return "", false
	}
	id, ok := NormalizeErrorUUID(rest)
	if !ok {
		return "", false
	}
	return MessageRefPrefix + id, true
}

// MessageRef describes one message the bot delivered to a real platform. It is
// stored as the content of a log entry whose MessageRefID is RefID, so it
// follows the log store's retention and persistence.
type MessageRef struct {
	RefID     string `json:"ref_id"`
	Platform  string `json:"platform"`
	SessionID string `json:"session_id"`
	// MessageID is the platform identifier returned by the delivery ACK.
	MessageID string `json:"message_id"`
	Action    string `json:"action"`
	// LLMResponseLogID is the latest LLM_RESPONSE entry of SessionID when the
	// message was delivered. Zero when unknown.
	LLMResponseLogID uint64 `json:"llm_response_log_id,omitempty"`
	// ToolLogID is the latest TOOL entry of SessionID when a tool call (such as
	// send_message) delivered the message. Zero for plain final replies.
	ToolLogID uint64 `json:"tool_log_id,omitempty"`
	// Message is the outbound payload exactly as handed to the platform.
	Message json.RawMessage `json:"message,omitempty"`
}

// RecordMessageRef retains ref as an indexed log entry and returns its RefID.
// A RefID is generated when ref.RefID is empty. It returns "" when the store
// cannot record it.
func (s *Store) RecordMessageRef(ref MessageRef) string {
	if s == nil || ref.SessionID == "" || ref.MessageID == "" {
		return ""
	}
	if ref.RefID == "" {
		ref.RefID = NewMessageRefID()
	}
	content, err := json.Marshal(ref)
	if err != nil {
		return ""
	}
	s.logEntry(INFO, WEBSOCKET, string(content), ref.SessionID, "OUTBOUND", nil, "...", ref.RefID)
	return ref.RefID
}

// FindMessageRef returns the retained record for a RefID.
func (s *Store) FindMessageRef(value string) (LogEntry, bool) {
	id, ok := NormalizeMessageRefID(value)
	if !ok || s == nil {
		return LogEntry{}, false
	}
	for _, entry := range s.Snapshot() {
		if entry.MessageRefID == id {
			return entry, true
		}
	}
	return LogEntry{}, false
}

// FindMessageRefByMessageID returns the RefID recorded for a platform message
// in sessionID. The newest record wins.
func (s *Store) FindMessageRefByMessageID(sessionID, messageID string) (string, bool) {
	if s == nil || sessionID == "" || messageID == "" {
		return "", false
	}
	var found LogEntry
	for _, entry := range s.Snapshot() {
		if entry.MessageRefID == "" || entry.TraceID != sessionID || (found.ID != 0 && entry.ID < found.ID) {
			continue
		}
		var ref MessageRef
		if json.Unmarshal([]byte(entry.Content), &ref) == nil && ref.MessageID == messageID {
			found = entry
		}
	}
	return found.MessageRefID, found.MessageRefID != ""
}

// LatestEntryID returns the newest retained in-memory entry of category whose
// trace ID is traceID, or 0.
func (s *Store) LatestEntryID(category Category, traceID string) uint64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.buffer == nil {
		return 0
	}
	node := s.buffer.Prev()
	for i := 0; i < s.size; i++ {
		if entry, ok := node.Value.(LogEntry); ok && entry.Category == category && entry.TraceID == traceID {
			return entry.ID
		}
		node = node.Prev()
	}
	return 0
}
