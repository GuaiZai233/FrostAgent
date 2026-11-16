package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
)

func TestGeminiRejectsURLImagesBeforeSending(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, imageURL := range []string{
		"https://images.example.invalid/photo.png?token=synthetic-secret",
		"http://images.example.invalid/photo.jpg",
		"gs://synthetic-bucket/photo.png",
		"https://generativelanguage.googleapis.com/v1beta/files/synthetic-file",
		"file:///synthetic/photo.png",
	} {
		req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser,
			Content: []core.ContentPart{{Type: "image_url", ImageURL: &core.ImageURL{URL: imageURL}}}}}}
		_, err := NewClient(server.URL, "synthetic-key", Gemini, 0).Chat(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "Gemini") || !strings.Contains(err.Error(), "base64 data URL") {
			t.Fatalf("expected actionable unsupported image URL error, got %v", err)
		}
		if strings.Contains(err.Error(), imageURL) || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("image URL leaked through error")
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d requests for unsupported images", requests.Load())
	}
}

func TestGeminiInlineImageData(t *testing.T) {
	parts, err := geminiContent([]core.ContentPart{
		{Type: "text", Text: "describe"},
		{Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/png;base64,aGVsbG8="}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].(map[string]any)["text"] != "describe" {
		t.Fatalf("lost mixed image content: %#v", parts)
	}
	image := parts[1].(map[string]any)
	inline, ok := image["inlineData"].(map[string]any)
	if !ok || inline["mimeType"] != "image/png" || inline["data"] != "aGVsbG8=" || image["fileData"] != nil {
		t.Fatalf("invalid inline image: %#v", image)
	}
	for _, value := range []string{"data:image/png;base64,%%%", "data:image/png,hello", "data:text/plain;base64,aGVsbG8="} {
		if _, err := geminiContent([]core.ContentPart{{Type: "image_url", ImageURL: &core.ImageURL{URL: value}}}); err == nil {
			t.Fatalf("accepted invalid image: %s", value)
		}
	}
}
