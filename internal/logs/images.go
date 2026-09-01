package logs

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const LogImagePathPrefix = "/api/log-images/"

var inlineImagePattern = regexp.MustCompile(`data:(image/[A-Za-z0-9.+-]+);base64,([A-Za-z0-9+/=]+)`)

type retainedImage struct {
	hash        string
	contentType string
	data        []byte
}

// InlineImage is image data that should be retained for the lifetime of a log
// entry and exposed through the log image handler.
type InlineImage struct {
	ContentType string
	Data        []byte
}

type cachedImage struct {
	contentType string
	data        []byte
	refCount    int
}

var imageCache = make(map[string]*cachedImage)

func prepareInlineImages(images []InlineImage) ([]retainedImage, []string) {
	retainedImages := make([]retainedImage, 0, len(images))
	placeholders := make([]string, 0, len(images))
	for _, image := range images {
		if len(image.Data) == 0 {
			continue
		}

		contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(image.ContentType, ";", 2)[0]))
		if !strings.HasPrefix(contentType, "image/") {
			contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(http.DetectContentType(image.Data), ";", 2)[0]))
		}
		if !strings.HasPrefix(contentType, "image/") {
			continue
		}

		data := append([]byte(nil), image.Data...)
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		retainedImages = append(retainedImages, retainedImage{
			hash:        hash,
			contentType: contentType,
			data:        data,
		})
		placeholders = append(placeholders, fmt.Sprintf(
			"[image omitted: type=%s, size=%d bytes, sha256=%s]",
			contentType,
			len(data),
			hash,
		))
	}
	return retainedImages, placeholders
}

func redactInlineImages(content string) (string, []retainedImage) {
	retainedImages := make([]retainedImage, 0)
	redacted := inlineImagePattern.ReplaceAllStringFunc(content, func(dataURL string) string {
		parts := inlineImagePattern.FindStringSubmatch(dataURL)
		if len(parts) != 3 {
			return dataURL
		}

		contentType := strings.ToLower(parts[1])
		encoded := parts[2]
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			hash := sha256.Sum256([]byte(encoded))
			return fmt.Sprintf(
				"[image omitted: type=%s, encoded_size=%d chars, sha256=%x, invalid_base64]",
				contentType,
				len(encoded),
				hash,
			)
		}

		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		retainedImages = append(retainedImages, retainedImage{
			hash:        hash,
			contentType: contentType,
			data:        data,
		})
		return fmt.Sprintf(
			"[image omitted: type=%s, size=%d bytes, sha256=%s]",
			contentType,
			len(data),
			hash,
		)
	})

	return redacted, retainedImages
}

func retainImagesLocked(images []retainedImage) []string {
	if len(images) == 0 {
		return nil
	}

	refs := make([]string, 0, len(images))
	for _, image := range images {
		cached, ok := imageCache[image.hash]
		if !ok {
			cached = &cachedImage{
				contentType: image.contentType,
				data:        image.data,
			}
			imageCache[image.hash] = cached
		}
		cached.refCount++
		refs = append(refs, image.hash)
	}
	return refs
}

func releaseImagesLocked(refs []string) {
	for _, hash := range refs {
		cached, ok := imageCache[hash]
		if !ok {
			continue
		}
		cached.refCount--
		if cached.refCount <= 0 {
			delete(imageCache, hash)
		}
	}
}

// ImageHandler serves an inline image retained by a currently buffered log entry.
func ImageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	hash := strings.TrimPrefix(r.URL.Path, LogImagePathPrefix)
	if len(hash) != sha256.Size*2 {
		http.NotFound(w, r)
		return
	}
	if _, err := hex.DecodeString(hash); err != nil {
		http.NotFound(w, r)
		return
	}

	mu.RLock()
	image, ok := imageCache[hash]
	if ok {
		image = &cachedImage{
			contentType: image.contentType,
			data:        image.data,
		}
	}
	mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Type", image.contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(image.data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(image.data)
}
