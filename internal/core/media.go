package core

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
)

// IsInlineMediaPart reports whether a content part carries audio or video
// bytes. Such media is request-local: it must never be logged, checkpointed or
// otherwise persisted.
func IsInlineMediaPart(part ContentPart) bool {
	return part.InputAudio != nil || part.VideoURL != nil
}

// WithoutInlineMedia returns messages whose audio and video parts are replaced
// by short text placeholders. Messages without such parts are shared, not
// copied, so callers must treat the result as read-only.
func WithoutInlineMedia(messages []ChatMessage) []ChatMessage {
	var result []ChatMessage
	for i, message := range messages {
		parts, ok := message.Content.([]ContentPart)
		if !ok || !containsInlineMedia(parts) {
			if result != nil {
				result[i] = message
			}
			continue
		}
		if result == nil {
			result = make([]ChatMessage, len(messages))
			copy(result, messages[:i])
		}
		redacted := make([]ContentPart, len(parts))
		for j, part := range parts {
			if IsInlineMediaPart(part) {
				part = ContentPart{Type: string(ContentPartTypeText), Text: InlineMediaPlaceholder(part)}
			}
			redacted[j] = part
		}
		message.Content = redacted
		result[i] = message
	}
	if result == nil {
		return messages
	}
	return result
}

// HasInlineMedia reports whether any message carries audio or video parts.
func HasInlineMedia(messages []ChatMessage) bool {
	for _, message := range messages {
		if parts, ok := message.Content.([]ContentPart); ok && containsInlineMedia(parts) {
			return true
		}
	}
	return false
}

func containsInlineMedia(parts []ContentPart) bool {
	return slices.ContainsFunc(parts, IsInlineMediaPart)
}

// InlineMediaPlaceholder describes an audio or video part without its bytes.
func InlineMediaPlaceholder(part ContentPart) string {
	switch {
	case part.InputAudio != nil:
		return mediaPlaceholder("audio", "format="+part.InputAudio.Format, part.InputAudio.Data)
	case part.VideoURL != nil:
		mimeType, encoded := "unknown", part.VideoURL.URL
		if header, data, ok := strings.Cut(part.VideoURL.URL, ","); ok && strings.HasPrefix(header, "data:") {
			mimeType = strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
			encoded = data
		}
		return mediaPlaceholder("video", "type="+mimeType, encoded)
	}
	return ""
}

func mediaPlaceholder(kind, detail, encoded string) string {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Sprintf("[%s omitted: %s, encoded_size=%d chars, invalid_base64]", kind, detail, len(encoded))
	}
	return fmt.Sprintf("[%s omitted: %s, size=%d bytes, sha256=%x]", kind, detail, len(data), sha256.Sum256(data))
}

// AudioMIMEType maps an InputAudio format name to its MIME type.
func AudioMIMEType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "ogg", "opus":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "aac":
		return "audio/aac"
	case "m4a", "mp4":
		return "audio/mp4"
	case "aiff":
		return "audio/aiff"
	case "webm":
		return "audio/webm"
	}
	return "audio/" + strings.ToLower(strings.TrimSpace(format))
}
