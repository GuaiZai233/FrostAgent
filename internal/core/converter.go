package core

import (
	"encoding/base64"
	"fmt"
	"net/http"
)

// ToChatMessages converts a platform-agnostic IncomingMessage to a slice of ChatMessage
// that can be consumed by LLM providers.
func ToChatMessages(incoming *IncomingMessage) []ChatMessage {
	if incoming == nil {
		return nil
	}

	var parts []ContentPart
	if incoming.Content != "" {
		parts = append(parts, ContentPart{
			Type: string(ContentPartTypeText),
			Text: incoming.Content,
		})
	}

	for _, att := range incoming.Attachments {
		if att.Type == AttachmentTypeImage {
			source := att.URL
			if len(att.Content) > 0 {
				mimeType := att.MimeType
				if mimeType == "" {
					mimeType = http.DetectContentType(att.Content)
				}
				source = fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(att.Content))
			}
			parts = append(parts, ContentPart{
				Type: string(ContentPartTypeImage),
				ImageURL: &ImageURL{
					URL: source,
				},
			})
		}
	}

	return []ChatMessage{
		{
			Role:    RoleUser,
			Content: parts,
		},
	}
}
