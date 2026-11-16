// Package chathistory stores searchable conversation originals independently of
// model context, summaries and extracted memories.
package chathistory

import (
	"fmt"
	"strings"
	"time"

	"github.com/GuaiZai233/FrostAgent/internal/memory"
)

const (
	PrivateEnabledKey = "CHAT_HISTORY_PRIVATE_ENABLED"
	GroupWhitelistKey = "CHAT_HISTORY_GROUP_WHITELIST"
	MaxContentBytes   = 1 << 20
	MaxSearchResults  = 20
)

type Scope struct {
	Platform    string `json:"platform"`
	MessageType string `json:"message_type"`
	TargetID    string `json:"target_id"`
}

func (s Scope) Canonical() Scope {
	s.Platform = memory.CanonicalPlatform(s.Platform)
	s.TargetID = strings.TrimSpace(s.TargetID)
	return s
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.Platform) == "" || s.TargetID == "" || (s.MessageType != "group" && s.MessageType != "private") {
		return fmt.Errorf("invalid chat history conversation scope")
	}
	return nil
}

type Record struct {
	Scope
	ID         string    `json:"id"`
	MessageID  string    `json:"message_id,omitempty"`
	SenderID   string    `json:"sender_id,omitempty"`
	SenderName string    `json:"sender_name,omitempty"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	ReplyTo    string    `json:"reply_to,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	// received: inbound; delivered: platform ACK; sent: transport write only.
	DeliveryStatus string `json:"delivery_status"`
}

func (r Record) Validate() error {
	if err := r.Scope.Validate(); err != nil {
		return err
	}
	if r.ID == "" || r.OccurredAt.IsZero() || len(r.Content) > MaxContentBytes {
		return fmt.Errorf("invalid chat history record identity, time or size")
	}
	if r.Role != "user" && r.Role != "assistant" {
		return fmt.Errorf("invalid chat history role")
	}
	if (r.Role == "user" && r.DeliveryStatus != "received") ||
		(r.Role == "assistant" && r.DeliveryStatus != "delivered" && r.DeliveryStatus != "sent") {
		return fmt.Errorf("invalid chat history delivery status")
	}
	return nil
}

// RecordingAllowed governs new collection only. Removing a whitelist entry
// neither deletes existing records nor changes their conversation permissions.
func RecordingAllowed(getenv func(string) string, scope Scope) bool {
	if getenv == nil {
		return false
	}
	scope = scope.Canonical()
	if scope.Validate() != nil {
		return false
	}
	if scope.MessageType == "private" {
		return strings.TrimSpace(getenv(PrivateEnabledKey)) != "false"
	}
	for _, entry := range strings.FieldsFunc(getenv(GroupWhitelistKey), func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		// Bare IDs are QQ-only; other platforms require platform:group:id.
		if scope.Platform == "qq" && entry == scope.TargetID {
			return true
		}
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) == 3 && parts[1] == "group" && memory.CanonicalPlatform(parts[0]) == scope.Platform && parts[2] == scope.TargetID {
			return true
		}
	}
	return false
}

type Query struct {
	Keywords []string  `json:"keywords"`
	Match    string    `json:"match,omitempty"`
	After    time.Time `json:"after,omitempty"`
	Before   time.Time `json:"before,omitempty"`
	Limit    int       `json:"limit,omitempty"`
}

func ValidateWhitelist(value string) error {
	if len(value) > 65536 {
		return fmt.Errorf("聊天档案白名单过长")
	}
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if len(entry) > 1024 || strings.ContainsAny(entry, " \t") {
			return fmt.Errorf("聊天档案白名单条目无效")
		}
		if strings.Contains(entry, ":") {
			parts := strings.SplitN(entry, ":", 3)
			if len(parts) != 3 || parts[0] == "" || parts[1] != "group" || parts[2] == "" {
				return fmt.Errorf("白名单请填写 QQ 群号或 platform:group:id")
			}
		}
	}
	return nil
}
