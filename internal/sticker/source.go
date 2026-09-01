package sticker

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const maxImageSize = 10 << 20 // 10 MiB

var (
	trustedQQImageURL = regexp.MustCompile(`^https://(?:gchat\.qpic\.cn|c2cpicdw\.qpic\.cn|p\.qpic\.cn|multimedia\.nt\.qq\.com\.cn|gxh\.vip\.qq\.com)(?:[/?#]|$)`)
	httpClient        = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if !trustedQQImageURL.MatchString(req.URL.String()) {
				return errors.New("image redirect target is not an allowed QQ media URL")
			}
			return nil
		},
	}
)

// LoadImageSource normalizes a trusted platform image source into bytes at the
// adapter boundary. Downstream sticker selection and storage use bytes only.
func LoadImageSource(ctx context.Context, source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, errors.New("image source is empty")
	}

	switch {
	case strings.HasPrefix(source, "base64://"):
		return decodeBase64Image(strings.TrimPrefix(source, "base64://"))
	case strings.HasPrefix(source, "data:"):
		comma := strings.IndexByte(source, ',')
		if comma < 0 || !strings.Contains(strings.ToLower(source[:comma]), ";base64") {
			return nil, errors.New("image data URI is not base64 encoded")
		}
		return decodeBase64Image(source[comma+1:])
	case strings.HasPrefix(source, "http://"), strings.HasPrefix(source, "https://"):
		return downloadImageContext(ctx, source)
	default:
		return nil, errors.New("unsupported image source")
	}
}

func decodeBase64Image(encoded string) ([]byte, error) {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	data, err := readLimitedImage(decoder)
	if err != nil {
		return nil, fmt.Errorf("decode base64 image: %w", err)
	}
	return data, nil
}

func downloadImage(url string) ([]byte, error) {
	return downloadImageContext(context.Background(), url)
}

func downloadImageContext(ctx context.Context, url string) ([]byte, error) {
	if !trustedQQImageURL.MatchString(url) {
		return nil, errors.New("image source is not an allowed QQ media URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return readLimitedImage(resp.Body)
}

func readLimitedImage(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxImageSize+1))
	if err != nil {
		return nil, err
	}
	if err := validateImageData(data); err != nil {
		return nil, err
	}
	return data, nil
}
