package core

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
			parts = append(parts, ContentPart{
				Type: string(ContentPartTypeImage),
				ImageURL: &ImageURL{
					URL: att.URL,
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
