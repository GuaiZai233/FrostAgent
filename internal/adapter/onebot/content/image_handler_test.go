package content

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/core"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMarketFaceSegmentSource(t *testing.T) {
	tests := []struct {
		name   string
		seg    MessageSegment
		source string
	}{
		{
			name: "NapCat inbound image",
			seg: MessageSegment{Type: "image", Data: map[string]any{
				"emoji_id": "abcdef",
				"url":      "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
			}},
			source: "https://gxh.vip.qq.com/club/item/parcel/item/ab/abcdef/raw300.gif",
		},
		{
			name: "native mface",
			seg: MessageSegment{Type: "mface", Data: map[string]any{
				"emoji_id": "123456",
			}},
			source: "https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif",
		},
		{
			name: "native mface base64",
			seg: MessageSegment{Type: "mface", Data: map[string]any{
				"emoji_id": "123456",
				"file":     "base64://bWZhY2U=",
			}},
			source: "base64://bWZhY2U=",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !IsMarketFaceSegment(test.seg) {
				t.Fatal("expected marketplace sticker segment")
			}
			if source := SegmentImageSource(test.seg); source != test.source {
				t.Fatalf("source = %q, want %q", source, test.source)
			}
		})
	}
}

func TestIsContainImageRecognizesMFace(t *testing.T) {
	if !IsContainImage([]MessageSegment{{Type: "mface", Data: map[string]any{"emoji_id": "123456"}}}) {
		t.Fatal("expected mface to be treated as image content")
	}
}

func TestTrustedQQImageURL(t *testing.T) {
	tests := []struct {
		url     string
		allowed bool
	}{
		{url: "https://gchat.qpic.cn/download?file=test", allowed: true},
		{url: "https://multimedia.nt.qq.com.cn/download?file=test", allowed: true},
		{url: "https://gxh.vip.qq.com/club/item/parcel/item/12/123456/raw300.gif", allowed: true},
		{url: "http://gchat.qpic.cn/download", allowed: false},
		{url: "https://gchat.qpic.cn.evil.example/download", allowed: false},
		{url: "https://gchat.qpic.cn@evil.example/download", allowed: false},
		{url: "https://gchat.qpic.cn:8443/download", allowed: false},
		{url: "http://127.0.0.1/private", allowed: false},
	}

	for _, test := range tests {
		if got := trustedQQImageURL.MatchString(test.url); got != test.allowed {
			t.Errorf("trustedQQImageURL.MatchString(%q) = %v, want %v", test.url, got, test.allowed)
		}
	}
}

func TestImageSourceToBase64RejectsUntrustedURL(t *testing.T) {
	if _, err := imageSourceToBase64("http://127.0.0.1/private"); err == nil {
		t.Fatal("expected untrusted image URL to be rejected")
	}
}

type imageRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f imageRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type imageFallbackProvider struct {
	requests []core.ChatRequest
}

func (p *imageFallbackProvider) Chat(_ context.Context, request core.ChatRequest) (*core.ChatResponse, error) {
	p.requests = append(p.requests, request)
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "引用图片描述"}}, nil
}

func TestProcessImageWithFallbackHandlesRemoteFailureAndDeduplicates(t *testing.T) {
	originalClient := imageHTTPClient
	t.Cleanup(func() { imageHTTPClient = originalClient })
	forwardedBytes := []byte("forwarded-image")
	for _, tc := range []struct {
		name        string
		status      int
		remoteBytes []byte
		wantImages  int
	}{
		{"expired_remote_403", http.StatusForbidden, nil, 1},
		{"same_remote_image", http.StatusOK, forwardedBytes, 1},
		{"lookup_only_image", http.StatusOK, []byte("another-image"), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			downloads := 0
			imageHTTPClient = &http.Client{Transport: imageRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				downloads++
				return &http.Response{
					StatusCode: tc.status, Header: make(http.Header),
					Body:    io.NopCloser(strings.NewReader(string(tc.remoteBytes))),
					Request: request,
				}, nil
			})}
			provider := &imageFallbackProvider{}
			lookedUp := []MessageSegment{
				{Type: "text", Data: map[string]any{"text": "引用消息原文"}},
				{Type: "image", Data: map[string]any{"url": "https://gchat.qpic.cn/image"}},
			}
			forwarded := []MessageSegment{{Type: "image", Data: map[string]any{"base64": base64.StdEncoding.EncodeToString(forwardedBytes)}}}
			if description := ProcessImageWithFallback(context.Background(), lookedUp, forwarded, provider, core.RouteContext{}); description != "引用图片描述" {
				t.Fatalf("description = %q", description)
			}
			if downloads != 1 || len(provider.requests) != 1 {
				t.Fatalf("downloads=%d vision calls=%d", downloads, len(provider.requests))
			}
			encoded, err := json.Marshal(provider.requests[0].Messages[1].Content)
			if err != nil {
				t.Fatal(err)
			}
			var blocks []ContentBlock
			if err := json.Unmarshal(encoded, &blocks); err != nil {
				t.Fatal(err)
			}
			images := 0
			texts := 0
			for _, block := range blocks {
				if block.Type == "image_url" {
					images++
				}
				if block.Type == "text" {
					texts++
					if block.Text != "引用消息原文" {
						t.Fatalf("quote text duplicated: %q", block.Text)
					}
				}
			}
			if images != tc.wantImages || texts != 1 {
				t.Fatalf("vision images=%d texts=%d, want images=%d texts=1", images, texts, tc.wantImages)
			}
		})
	}
}
