package logs

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// NormalizeErrorUUID accepts only the canonical UUIDv4 form used by error logs.
func NormalizeErrorUUID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	id, err := uuid.Parse(value)
	if err != nil || len(value) != 36 || id.Version() != 4 || id.Variant() != uuid.RFC4122 {
		return "", false
	}
	return id.String(), true
}

// FindError searches only this store's retained error entries.
func (s *Store) FindError(value string) (LogEntry, bool) {
	id, ok := NormalizeErrorUUID(value)
	if !ok || s == nil {
		return LogEntry{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found LogEntry
	if s.buffer != nil {
		s.buffer.Do(func(value any) {
			if entry, ok := value.(LogEntry); ok && entry.Level == ERROR && entry.ErrorUUID == id {
				found = entry
			}
		})
	}
	return found, found.ErrorUUID != ""
}

// ReportError retains diagnostics and returns only an explicitly safe summary
// and its lookup UUID. Callers must not include raw errors in summary.
func (s *Store) ReportError(category Category, summary string, err error, traceID ...string) string {
	content := summary
	if err != nil {
		content += fmt.Sprintf("：%v", err)
	}
	id := s.Error(category, content, traceID...)
	return fmt.Sprintf("[FrostAgent 错误] %s (Ref_ID: %s)", summary, id)
}
