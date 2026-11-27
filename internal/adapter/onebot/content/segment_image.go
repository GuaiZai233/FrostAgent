package content

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

// SegmentImageKey returns the conversation-image cache key for an image or
// mface segment, or "" when the segment carries no trusted source.
func SegmentImageKey(seg MessageSegment) string {
	if seg.Type != "image" && seg.Type != "mface" {
		return ""
	}
	source := SegmentImageSource(seg)
	if strings.TrimSpace(source) == "" {
		return ""
	}
	return logs.SessionImageKey(source)
}

// FetchSegmentImage loads the bytes of an image or mface segment through the
// same trusted-source rules used for model-visible images.
func FetchSegmentImage(ctx context.Context, seg MessageSegment) ([]byte, error) {
	encoded, err := imageSourceToBase64(SegmentImageSource(seg), ctx)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(encoded)
}
