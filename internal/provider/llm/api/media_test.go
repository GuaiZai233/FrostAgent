package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

func mediaParts() ([]core.ContentPart, string, string) {
	audio := base64.StdEncoding.EncodeToString([]byte("synthetic-audio-payload-bytes"))
	video := base64.StdEncoding.EncodeToString([]byte("synthetic-video-payload-bytes"))
	return []core.ContentPart{
		{Type: "text", Text: "transcribe"},
		{Type: "input_audio", InputAudio: &core.InputAudio{Data: audio, Format: "mp3"}},
		{Type: "video_url", VideoURL: &core.VideoURL{URL: "data:video/mp4;base64," + video}},
	}, audio, video
}

func TestGeminiInlineMediaData(t *testing.T) {
	parts, audio, video := mediaParts()
	encoded, err := geminiContent(parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 3 {
		t.Fatalf("lost media parts: %#v", encoded)
	}
	audioPart := encoded[1].(map[string]any)["inlineData"].(map[string]any)
	videoPart := encoded[2].(map[string]any)["inlineData"].(map[string]any)
	if audioPart["mimeType"] != "audio/mpeg" || audioPart["data"] != audio {
		t.Fatalf("invalid inline audio: %#v", audioPart)
	}
	if videoPart["mimeType"] != "video/mp4" || videoPart["data"] != video {
		t.Fatalf("invalid inline video: %#v", videoPart)
	}
	for _, bad := range []core.ContentPart{
		{Type: "input_audio", InputAudio: &core.InputAudio{Data: "%%%", Format: "mp3"}},
		{Type: "video_url", VideoURL: &core.VideoURL{URL: "https://videos.example.invalid/clip.mp4"}},
		{Type: "video_url", VideoURL: &core.VideoURL{URL: "data:image/png;base64,aGVsbG8="}},
	} {
		if _, err := geminiContent([]core.ContentPart{bad}); err == nil {
			t.Fatalf("accepted invalid media: %+v", bad)
		}
	}
}

func TestUnsupportedFormatsRejectMediaBeforeSending(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	parts, _, _ := mediaParts()
	for _, format := range []Format{Anthropic, Responses} {
		req := core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: parts}}}
		if _, err := NewClient(server.URL, "synthetic-key", format, 0).Chat(context.Background(), req); err == nil || !strings.Contains(err.Error(), "音频/视频") {
			t.Fatalf("%s: expected unsupported media error, got %v", format, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d requests with unsupported media", requests.Load())
	}
}

func TestRequestLogsOmitMediaBytes(t *testing.T) {
	for _, format := range []Format{ChatCompletions, Gemini} {
		t.Run(string(format), func(t *testing.T) {
			var wire string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				wire = string(body)
				w.Header().Set("Content-Type", "application/json")
				if format == Gemini {
					_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": "ok"}}}}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}}})
			}))
			defer server.Close()
			parts, audio, video := mediaParts()
			store := logs.New("synthetic-instance", "synthetic", 100)
			client := NewClient(server.URL, "synthetic-key", format, 0)
			client.Logger = store
			if _, err := client.Chat(context.Background(), core.ChatRequest{Model: "test-model", Messages: []core.ChatMessage{{Role: core.RoleUser, Content: parts}}}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(wire, audio) || !strings.Contains(wire, video) {
				t.Fatalf("media missing from the upstream request: %s", wire)
			}
			if format == ChatCompletions && (!strings.Contains(wire, `"input_audio"`) || !strings.Contains(wire, `"video_url"`)) {
				t.Fatalf("chat completions media shape changed: %s", wire)
			}
			var logged bool
			for _, entry := range store.Snapshot() {
				if entry.Category != logs.LLM_REQUEST {
					continue
				}
				logged = true
				if strings.Contains(entry.Content, audio) || strings.Contains(entry.Content, video) {
					t.Fatalf("request log kept media bytes: %s", entry.Content)
				}
				if !strings.Contains(entry.Content, "[audio omitted: format=mp3") || !strings.Contains(entry.Content, "[video omitted: type=video/mp4") {
					t.Fatalf("request log lacks media placeholders: %s", entry.Content)
				}
			}
			if !logged {
				t.Fatal("request was not logged")
			}
		})
	}
}
