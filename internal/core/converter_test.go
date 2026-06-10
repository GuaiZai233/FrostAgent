package core

import (
	"testing"
)

func TestToChatMessages(t *testing.T) {
	tests := []struct {
		name     string
		incoming *IncomingMessage
		wantLen  int
		check    func(*testing.T, []ChatMessage)
	}{
		{
			name: "Pure text message",
			incoming: &IncomingMessage{
				Content: "Hello FrostAgent",
			},
			wantLen: 1,
			check: func(t *testing.T, msgs []ChatMessage) {
				parts, ok := msgs[0].Content.([]ContentPart)
				if !ok {
					t.Fatalf("expected []ContentPart, got %T", msgs[0].Content)
				}
				if len(parts) != 1 || parts[0].Text != "Hello FrostAgent" {
					t.Errorf("expected text part, got %v", parts)
				}
			},
		},
		{
			name: "Message with image attachments",
			incoming: &IncomingMessage{
				Content: "Look at this",
				Attachments: []Attachment{
					{
						Type: AttachmentTypeImage,
						URL:  "http://example.com/image.png",
					},
				},
			},
			wantLen: 1,
			check: func(t *testing.T, msgs []ChatMessage) {
				parts, ok := msgs[0].Content.([]ContentPart)
				if !ok {
					t.Fatalf("expected []ContentPart, got %T", msgs[0].Content)
				}
				if len(parts) != 2 {
					t.Fatalf("expected 2 parts, got %d", len(parts))
				}
				if parts[0].Type != ContentPartTypeText || parts[1].Type != ContentPartTypeImage {
					t.Errorf("unexpected part types: %s, %s", parts[0].Type, parts[1].Type)
				}
			},
		},
		{
			name:     "Empty message",
			incoming: &IncomingMessage{},
			wantLen:  1,
			check: func(t *testing.T, msgs []ChatMessage) {
				parts, ok := msgs[0].Content.([]ContentPart)
				if !ok {
					t.Fatalf("expected []ContentPart, got %T", msgs[0].Content)
				}
				if len(parts) != 0 {
					t.Errorf("expected 0 parts, got %d", len(parts))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToChatMessages(tt.incoming)
			if len(got) != tt.wantLen {
				t.Errorf("ToChatMessages() len = %v, want %v", len(got), tt.wantLen)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
