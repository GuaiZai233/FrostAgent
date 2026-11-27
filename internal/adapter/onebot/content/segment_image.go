package content

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

var errImageTooLarge = errors.New("图片超过预览大小上限")

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

// FetchSegmentImage loads the bytes of an image or mface segment for console
// previews. It follows the same trusted-source rules as model-visible images
// but never reads or decodes more than logs.MaxSessionImageBytes.
func FetchSegmentImage(ctx context.Context, seg MessageSegment) ([]byte, error) {
	return fetchImageSourceLimited(ctx, SegmentImageSource(seg), logs.MaxSessionImageBytes)
}

func fetchImageSourceLimited(ctx context.Context, source string, limit int64) ([]byte, error) {
	switch {
	case strings.HasPrefix(source, "base64://"):
		return decodeLimitedBase64(strings.TrimPrefix(source, "base64://"), limit)
	case strings.HasPrefix(source, "data:"):
		comma := strings.IndexByte(source, ',')
		if comma < 0 || !strings.Contains(strings.ToLower(source[:comma]), ";base64") {
			return nil, fmt.Errorf("图片 data URI 不是 Base64")
		}
		return decodeLimitedBase64(source[comma+1:], limit)
	}
	if !trustedQQImageURL.MatchString(source) {
		return nil, fmt.Errorf("图片来源不是允许的 QQ 媒体地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := imageHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("图片下载失败，状态码: %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errImageTooLarge
	}
	// Content-Length can be absent or wrong, so bound the actual read too.
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errImageTooLarge
	}
	return data, nil
}

func decodeLimitedBase64(encoded string, limit int64) ([]byte, error) {
	if int64(base64.StdEncoding.DecodedLen(len(encoded))) > limit {
		return nil, errImageTooLarge
	}
	return base64.StdEncoding.DecodeString(encoded)
}
