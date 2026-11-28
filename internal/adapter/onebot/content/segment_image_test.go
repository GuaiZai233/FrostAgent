package content

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/logs"
)

type countingBody struct {
	remaining int
	read      int
}

func (b *countingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *countingBody) Close() error { return nil }

func stubImageClient(t *testing.T, response func(*http.Request) *http.Response) {
	t.Helper()
	original := imageHTTPClient
	t.Cleanup(func() { imageHTTPClient = original })
	imageHTTPClient = &http.Client{Transport: imageRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		resp := response(request)
		resp.Request = request
		return resp, nil
	})}
}

func previewSegment(url string) MessageSegment {
	return MessageSegment{Type: "image", Data: map[string]any{"url": url}}
}

func TestFetchSegmentImageStopsReadingAtSizeLimitWithoutContentLength(t *testing.T) {
	body := &countingBody{remaining: 9 << 20}
	stubImageClient(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: -1, Body: body}
	})
	data, err := FetchSegmentImage(context.Background(), previewSegment("https://gchat.qpic.cn/large"))
	if !errors.Is(err, errImageTooLarge) || data != nil {
		t.Fatalf("got %d bytes, err %v; want errImageTooLarge", len(data), err)
	}
	if body.read > logs.MaxSessionImageBytes+64<<10 {
		t.Fatalf("read %d bytes of a %d-byte limit before stopping", body.read, logs.MaxSessionImageBytes)
	}
}

func TestFetchSegmentImageRejectsDeclaredOversizeBeforeReading(t *testing.T) {
	body := &countingBody{remaining: 9 << 20}
	stubImageClient(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: 9 << 20, Body: body}
	})
	if _, err := FetchSegmentImage(context.Background(), previewSegment("https://gchat.qpic.cn/declared")); !errors.Is(err, errImageTooLarge) {
		t.Fatalf("err = %v, want errImageTooLarge", err)
	}
	if body.read != 0 {
		t.Fatalf("read %d bytes despite a declared oversize", body.read)
	}
}

func TestFetchSegmentImageReturnsImagesWithinLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1024)
	stubImageClient(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(bytes.NewReader(payload))}
	})
	data, err := FetchSegmentImage(context.Background(), previewSegment("https://gchat.qpic.cn/small"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("got %d bytes, err %v", len(data), err)
	}
}

func TestFetchSegmentImageChecksInlineSizeBeforeDecoding(t *testing.T) {
	oversized := base64.StdEncoding.EncodeToString(make([]byte, logs.MaxSessionImageBytes+1))
	for name, data := range map[string]map[string]any{
		"base64_field": {"base64": oversized},
		"data_uri":     {"url": "data:image/png;base64," + oversized},
	} {
		if _, err := FetchSegmentImage(context.Background(), MessageSegment{Type: "image", Data: data}); !errors.Is(err, errImageTooLarge) {
			t.Fatalf("%s: err = %v, want errImageTooLarge", name, err)
		}
	}
	small := base64.StdEncoding.EncodeToString([]byte("synthetic"))
	if data, err := FetchSegmentImage(context.Background(), MessageSegment{Type: "image", Data: map[string]any{"base64": small}}); err != nil || string(data) != "synthetic" {
		t.Fatalf("small inline image: %q, %v", data, err)
	}
}

func TestFetchSegmentImageRejectsUntrustedSources(t *testing.T) {
	if _, err := FetchSegmentImage(context.Background(), previewSegment("https://example.invalid/a.png")); err == nil {
		t.Fatal("untrusted host must not be fetched")
	}
}
