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

// SendMessageRequest defines the payload accepted by the message delivery endpoint.
type SendMessageRequest struct {
	Session     string                 `json:"session,omitempty"`      // e.g. "platform:message_type:target_id"
	Platform    string                 `json:"platform,omitempty"`     // e.g. "qq", "onebot", "astrbot"
	MessageType string                 `json:"message_type,omitempty"` // "private" or "group"
	TargetID    string                 `json:"target_id,omitempty"`    // target user ID or group ID
	Content     string                 `json:"content,omitempty"`      // direct message text
	Attachments []core.Attachment      `json:"attachments,omitempty"`  // direct message attachments
	Messages    []core.OutgoingMessage `json:"messages,omitempty"`     // segmented message list
	InstanceID  string                 `json:"instance_id,omitempty"`  // optional FrostAgent multi-instance routing ID
	Metadata    map[string]any         `json:"metadata,omitempty"`

	// Element compatibility fields
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
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
		getenv:     getenv,
	}
}

func (s *Service) checkAuth(r *http.Request) error {
	token := strings.TrimSpace(s.getenv("FROSTAGENT_API_KEY"))
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

	// If no token is configured on the server, allow access (open/test mode)
	if token == "" {
		return nil
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

	return errors.New("unauthorized")
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := s.checkAuth(r); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized: invalid or missing API key"})
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

func (s *Service) normalizeMessages(req *SendMessageRequest) ([]core.OutgoingMessage, error) {
	if len(req.Messages) == 0 {
		content := req.Content
		if content == "" && req.Text != "" {
			content = req.Text
		}

		if strings.TrimSpace(content) == "" && len(req.Attachments) == 0 {
			return nil, errors.New("messages, content, or attachments cannot be empty")
		}

		platform := strings.TrimSpace(req.Platform)
		msgType := strings.TrimSpace(req.MessageType)
		targetID := strings.TrimSpace(req.TargetID)

		// Parse session format if individual fields are empty: "platform:message_type:target_id"
		if req.Session != "" {
			parts := strings.Split(req.Session, ":")
			if len(parts) >= 3 {
				if platform == "" {
					platform = parts[0]
				}
				if msgType == "" {
					msgType = parts[1]
				}
				if targetID == "" {
					targetID = parts[2]
				}
			}
		}

		if platform == "" {
			return nil, errors.New("platform is required")
		}
		if targetID == "" {
			return nil, errors.New("target_id is required")
		}
		if msgType == "" {
			msgType = "group"
		}

		return []core.OutgoingMessage{
			{
				TargetID:    targetID,
				MessageType: msgType,
				Platform:    platform,
				Content:     content,
				Attachments: req.Attachments,
				Metadata:    req.Metadata,
			},
		}, nil
	}

	normalized := make([]core.OutgoingMessage, 0, len(req.Messages))
	for i, msg := range req.Messages {
		if msg.Platform == "" {
			msg.Platform = req.Platform
		}
		if msg.TargetID == "" {
			msg.TargetID = req.TargetID
		}
		if msg.MessageType == "" {
			msg.MessageType = req.MessageType
			if msg.MessageType == "" {
				msg.MessageType = "group"
			}
		}

		if strings.TrimSpace(msg.Platform) == "" {
			return nil, fmt.Errorf("message[%d]: platform is required", i)
		}
		if strings.TrimSpace(msg.TargetID) == "" {
			return nil, fmt.Errorf("message[%d]: target_id is required", i)
		}
		if strings.TrimSpace(msg.Content) == "" && len(msg.Attachments) == 0 {
			return nil, fmt.Errorf("message[%d]: content or attachments cannot be empty", i)
		}

		normalized = append(normalized, msg)
	}

	return normalized, nil
}
