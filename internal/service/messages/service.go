package messages

import (
	"FrostAgent/internal/core"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var (
	ErrServerUnconfigured = errors.New("message service unconfigured: no authentication token configured")
	ErrUnauthorized       = errors.New("unauthorized: invalid or missing API key")
)

// OutgoingMessageInput represents incoming segmented message objects, supporting
// both canonical core fields and ActionsCat / adapter compatibility fields.
type OutgoingMessageInput struct {
	TargetID    string            `json:"target_id,omitempty"`
	MessageType string            `json:"message_type,omitempty"`
	Platform    string            `json:"platform,omitempty"`
	Content     string            `json:"content,omitempty"`
	Attachments []core.Attachment `json:"attachments,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`

	// Element compatibility fields (ActionsCat SDK MessageItem, OneBot/AstrBot segments)
	Type          string `json:"type,omitempty"` // "plain", "text", "image", "file", "video", "audio"
	Text          string `json:"text,omitempty"`
	MentionUserID string `json:"mention_user_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	URL           string `json:"url,omitempty"`
	Path          string `json:"path,omitempty"`
	IsSticker     bool   `json:"is_sticker,omitempty"`
}

// SendMessageRequest defines the payload accepted by the message delivery endpoint.
type SendMessageRequest struct {
	Session     string                 `json:"session,omitempty"`      // e.g. "platform:message_type:target_id"
	Platform    string                 `json:"platform,omitempty"`     // e.g. "qq", "onebot", "astrbot"
	MessageType string                 `json:"message_type,omitempty"` // "private" or "group"
	TargetID    string                 `json:"target_id,omitempty"`    // target user ID or group ID
	Content     string                 `json:"content,omitempty"`      // direct message text
	Attachments []core.Attachment      `json:"attachments,omitempty"`  // direct message attachments
	Messages    []OutgoingMessageInput `json:"messages,omitempty"`     // segmented message list
	InstanceID  string                 `json:"instance_id,omitempty"`  // optional FrostAgent multi-instance routing ID
	Metadata    map[string]any         `json:"metadata,omitempty"`

	// Element compatibility fields at top-level
	Type          string `json:"type,omitempty"`
	Text          string `json:"text,omitempty"`
	URL           string `json:"url,omitempty"`
	Path          string `json:"path,omitempty"`
	MentionUserID string `json:"mention_user_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
}

// SendMessageResponse represents the JSON response returned upon successful message dispatch.
type SendMessageResponse struct {
	Status    string `json:"status"`
	Delivered bool   `json:"delivered"`
	Count     int    `json:"count"`
}

// Service provides HTTP handling for outbound message delivery.
type Service struct {
	dispatcher core.MessageDispatcher
	instanceID string
	getenv     func(string) string
}

// New creates a new message delivery service bound to a dispatcher and optional instance ID.
func New(dispatcher core.MessageDispatcher, instanceID string, getenv func(string) string) *Service {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Service{
		dispatcher: dispatcher,
		instanceID: instanceID,
		getenv: func(k string) string {
			if v := getenv(k); v != "" {
				return v
			}
			return os.Getenv(k)
		},
	}
}

func (s *Service) checkAuth(r *http.Request) error {
	token := strings.TrimSpace(s.getenv("FROSTAGENT_ACTIONSCAT_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(s.getenv("FROSTAGENT_API_KEY"))
	}
	if token == "" {
		token = strings.TrimSpace(s.getenv("ACTIONSCAT_API_KEY"))
	}
	if token == "" {
		token = strings.TrimSpace(s.getenv("API_KEY"))
	}
	if token == "" {
		token = strings.TrimSpace(s.getenv("ADMIN_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(s.getenv("MCP_CONTROL_TOKEN"))
	}

	// FAIL-CLOSED INVARIANT:
	// Production side-effecting endpoints must NEVER fail open.
	// If no token is configured on the server, fail closed with HTTP 503.
	if token == "" {
		return ErrServerUnconfigured
	}

	// If token IS configured, client MUST provide valid credentials
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	frostKey := strings.TrimSpace(r.Header.Get("X-FrostAgent-Key"))
	authToken := strings.TrimSpace(r.Header.Get("X-Auth-Token"))

	if authHeader != "" {
		expectedBearer := "Bearer " + token
		if subtle.ConstantTimeCompare([]byte(authHeader), []byte(expectedBearer)) == 1 ||
			subtle.ConstantTimeCompare([]byte(authHeader), []byte(token)) == 1 {
			return nil
		}
	}

	if frostKey != "" && subtle.ConstantTimeCompare([]byte(frostKey), []byte(token)) == 1 {
		return nil
	}

	if authToken != "" && subtle.ConstantTimeCompare([]byte(authToken), []byte(token)) == 1 {
		return nil
	}

	return ErrUnauthorized
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := s.checkAuth(r); err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, ErrServerUnconfigured) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "failed to read request body: " + err.Error()})
		return
	}

	var req SendMessageRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid json body: " + err.Error()})
		return
	}

	// Server-side instance binding invariant:
	// Sandboxed workers cannot choose or override instance context.
	// If the service is bound to a specific instanceID, verify req.InstanceID matches or is empty.
	if s.instanceID != "" && req.InstanceID != "" && req.InstanceID != s.instanceID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": fmt.Sprintf("instance_id mismatch: request targets %q but service is bound to %q", req.InstanceID, s.instanceID),
		})
		return
	}

	// Normalize messages
	messages, err := s.normalizeMessages(&req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	if s.dispatcher == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "message dispatcher unavailable"})
		return
	}

	// Dispatch each outgoing message through the core dispatcher
	ctx := r.Context()
	for _, msg := range messages {
		targetPlatform := msg.Platform
		// Platform fallback: if target platform adapter is not found, attempt common alias mapping
		if _, ok := s.dispatcher.GetAdapter(targetPlatform); !ok {
			if targetPlatform == "qq" {
				if _, ok2 := s.dispatcher.GetAdapter("onebot"); ok2 {
					targetPlatform = "onebot"
				}
			}
		}

		if err := s.dispatcher.Dispatch(ctx, targetPlatform, msg); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": fmt.Sprintf("dispatch failed for platform %s: %s", msg.Platform, err.Error()),
			})
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(SendMessageResponse{
		Status:    "ok",
		Delivered: true,
		Count:     len(messages),
	})
}

func parseSession(session string) (platform, msgType, targetID string) {
	session = strings.TrimSpace(session)
	if session == "" {
		return "", "", ""
	}
	parts := strings.Split(session, ":")
	if len(parts) >= 3 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(strings.Join(parts[2:], ":"))
	}
	return "", "", ""
}

func isValidMessageType(t string) bool {
	return t == "group" || t == "private"
}

func (s *Service) normalizeMessages(req *SendMessageRequest) ([]core.OutgoingMessage, error) {
	// Parse session format if present: "platform:message_type:target_id"
	sessPlatform, sessMsgType, sessTargetID := parseSession(req.Session)
	if req.Platform == "" {
		req.Platform = sessPlatform
	}
	if req.MessageType == "" {
		req.MessageType = sessMsgType
	}
	if req.TargetID == "" {
		req.TargetID = sessTargetID
	}
	if req.MessageType == "" {
		req.MessageType = "group"
	}

	if len(req.Messages) == 0 {
		content := req.Content
		if content == "" && req.Text != "" {
			content = req.Text
		}

		attachments := req.Attachments
		if (req.Type == "image" || req.Type == "image_url" || req.Type == "file" || req.Type == "video" || req.Type == "audio") && len(attachments) == 0 {
			attType := core.AttachmentType(req.Type)
			if req.Type == "image_url" {
				attType = core.AttachmentType("image")
			}
			attachments = append(attachments, core.Attachment{
				Type: attType,
				URL:  req.URL,
				Name: req.Path,
			})
		}

		if strings.TrimSpace(content) == "" && len(attachments) == 0 {
			return nil, errors.New("messages, content, or attachments cannot be empty")
		}

		platform := strings.TrimSpace(req.Platform)
		msgType := strings.TrimSpace(req.MessageType)
		targetID := strings.TrimSpace(req.TargetID)

		if platform == "" {
			return nil, errors.New("platform is required")
		}
		if targetID == "" {
			return nil, errors.New("target_id is required")
		}
		if msgType == "" {
			msgType = "group"
		}
		if !isValidMessageType(msgType) {
			return nil, fmt.Errorf("invalid message_type %q: must be 'group' or 'private'", msgType)
		}

		return []core.OutgoingMessage{
			{
				TargetID:    targetID,
				MessageType: msgType,
				Platform:    platform,
				Content:     content,
				Attachments: attachments,
				Metadata:    req.Metadata,
			},
		}, nil
	}

	normalized := make([]core.OutgoingMessage, 0, len(req.Messages))
	for i, msg := range req.Messages {
		platform := strings.TrimSpace(msg.Platform)
		if platform == "" {
			platform = strings.TrimSpace(req.Platform)
		}

		msgType := strings.TrimSpace(msg.MessageType)
		if msgType == "" {
			msgType = strings.TrimSpace(req.MessageType)
		}
		if msgType == "" {
			msgType = "group"
		}
		if !isValidMessageType(msgType) {
			return nil, fmt.Errorf("message[%d]: invalid message_type %q: must be 'group' or 'private'", i, msgType)
		}

		targetID := strings.TrimSpace(msg.TargetID)
		if targetID == "" {
			targetID = strings.TrimSpace(req.TargetID)
		}

		content := msg.Content
		if content == "" && msg.Text != "" {
			content = msg.Text
		}

		attachments := msg.Attachments
		if msg.Type == "image" || msg.Type == "image_url" || msg.Type == "file" || msg.Type == "video" || msg.Type == "audio" {
			if msg.URL != "" || msg.Path != "" || len(attachments) == 0 {
				attType := core.AttachmentType(msg.Type)
				if msg.Type == "image_url" {
					attType = core.AttachmentType("image")
				}
				attachments = append(attachments, core.Attachment{
					Type: attType,
					URL:  msg.URL,
					Name: msg.Path,
				})
			}
		}

		if platform == "" {
			return nil, fmt.Errorf("message[%d]: platform is required", i)
		}
		if targetID == "" {
			return nil, fmt.Errorf("message[%d]: target_id is required", i)
		}
		if strings.TrimSpace(content) == "" && len(attachments) == 0 {
			return nil, fmt.Errorf("message[%d]: content or attachments cannot be empty", i)
		}

		normalized = append(normalized, core.OutgoingMessage{
			TargetID:    targetID,
			MessageType: msgType,
			Platform:    platform,
			Content:     content,
			Attachments: attachments,
			Metadata:    msg.Metadata,
		})
	}

	return normalized, nil
}
