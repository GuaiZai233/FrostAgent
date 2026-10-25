package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"FrostAgent/internal/core"
	"FrostAgent/internal/logs"
)

func TestSensitiveChatNeverLogsContent(t *testing.T) {
	const secret = "synthetic-sensitive-code-and-output"
	for _, fixture := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"success", 200, `{"choices":[{"message":{"role":"assistant","content":"` + secret + `"}}]}`, false},
		{"http_error", 503, secret, true},
		{"provider_error", 200, `{"error":{"message":"` + secret + `"}}`, true},
		{"malformed", 200, secret, true},
		{"empty_choices", 200, `{"choices":[]}`, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if _, exists := body["Sensitive"]; exists {
					t.Error("local Sensitive flag leaked to wire request")
				}
				if _, exists := body["sensitive"]; exists {
					t.Error("local sensitive flag leaked to wire request")
				}
				w.WriteHeader(fixture.status)
				_, _ = w.Write([]byte(fixture.body))
			}))
			defer server.Close()
			client := NewClient(server.URL, "synthetic-key")
			client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
			_, err := client.Chat(context.Background(), core.ChatRequest{
				Sensitive: true, Model: "synthetic-model", TraceID: "synthetic-trace",
				Messages: []core.ChatMessage{{Role: core.RoleUser, Content: secret}},
			})
			if (err != nil) != fixture.wantError {
				t.Fatalf("unexpected response error: %v", err)
			}
			if got := client.Logger.Snapshot(); len(got) != 0 {
				t.Fatalf("sensitive request wrote ordinary logs: %+v", got)
			}
		})
	}
}

type sensitiveRoundTripper func(*http.Request) (*http.Response, error)

func (f sensitiveRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type sensitiveErrorReader struct{}

func (sensitiveErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic-sensitive-read-error")
}
func (sensitiveErrorReader) Close() error { return nil }

func TestSensitiveChatDoesNotLogTransportOrReadErrors(t *testing.T) {
	for _, readError := range []bool{false, true} {
		client := NewClient("https://synthetic.invalid/v1", "")
		client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
		client.HTTPClient.Transport = sensitiveRoundTripper(func(*http.Request) (*http.Response, error) {
			if readError {
				return &http.Response{StatusCode: 200, Body: sensitiveErrorReader{}}, nil
			}
			return nil, errors.New("synthetic-sensitive-transport-error")
		})
		_, err := client.Chat(context.Background(), core.ChatRequest{Sensitive: true})
		if err == nil || len(client.Logger.Snapshot()) != 0 {
			t.Fatalf("sensitive failure was logged: %v %+v", err, client.Logger.Snapshot())
		}
	}
}

func TestOrdinaryChatKeepsContentLogging(t *testing.T) {
	const source = "synthetic-normal-request"
	const response = "synthetic-normal-response"
	for _, status := range []int{200, 503} {
		client := NewClient("https://synthetic.invalid/v1", "")
		client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
		client.HTTPClient.Transport = sensitiveRoundTripper(func(*http.Request) (*http.Response, error) {
			body := `{"choices":[{"message":{"role":"assistant","content":"` + response + `"}}]}`
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		_, _ = client.Chat(context.Background(), core.ChatRequest{
			Messages: []core.ChatMessage{{Role: core.RoleUser, Content: source}},
		})
		raw, _ := json.Marshal(client.Logger.Snapshot())
		if !strings.Contains(string(raw), source) || !strings.Contains(string(raw), response) {
			t.Fatalf("ordinary logging changed: %s", raw)
		}
	}
}

type trackedSensitiveBody struct {
	io.Reader
	readBytes int
	closed    bool
}

func (b *trackedSensitiveBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}

func (b *trackedSensitiveBody) Close() error {
	b.closed = true
	return nil
}

func TestSensitiveChatBoundsSuccessAndHTTPErrorResponses(t *testing.T) {
	const secret = "synthetic-sensitive-response-content"
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			body := &trackedSensitiveBody{Reader: strings.NewReader(secret + strings.Repeat("x", maxSensitiveResponseBytes*2))}
			client := NewClient("https://synthetic.invalid/v1", "")
			client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
			client.HTTPClient.Transport = sensitiveRoundTripper(func(*http.Request) (*http.Response, error) {
				// The streaming limit must not trust an upstream Content-Length.
				return &http.Response{StatusCode: status, ContentLength: 1, Body: body}, nil
			})
			response, err := client.Chat(context.Background(), core.ChatRequest{Sensitive: true})
			if response != nil || !errors.Is(err, errSensitiveResponseTooLarge) {
				t.Fatalf("oversized response not rejected: response=%+v error=%v", response, err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("oversized response content leaked through the error")
			}
			if body.readBytes != maxSensitiveResponseBytes+1 || !body.closed {
				t.Fatalf("response wasn't bounded and closed: read=%d closed=%v", body.readBytes, body.closed)
			}
			if entries := client.Logger.Snapshot(); len(entries) != 0 {
				t.Fatalf("oversized sensitive response was logged: %+v", entries)
			}
		})
	}
}

func TestSensitiveChatAcceptsResponseExactlyAtLimit(t *testing.T) {
	const payload = `{"choices":[{"message":{"role":"assistant","content":"synthetic-result"}}]}`
	body := &trackedSensitiveBody{Reader: strings.NewReader(payload + strings.Repeat(" ", maxSensitiveResponseBytes-len(payload)))}
	client := NewClient("https://synthetic.invalid/v1", "")
	client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
	client.HTTPClient.Transport = sensitiveRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	response, err := client.Chat(context.Background(), core.ChatRequest{Sensitive: true})
	if err != nil || response == nil || response.Message.Content != "synthetic-result" {
		t.Fatalf("response at limit failed: %+v %v", response, err)
	}
	if body.readBytes != maxSensitiveResponseBytes || !body.closed || len(client.Logger.Snapshot()) != 0 {
		t.Fatal("response at limit did not preserve body lifecycle/privacy")
	}
}

func TestOrdinaryChatResponseRemainsUnboundedBySensitiveLimit(t *testing.T) {
	const payload = `{"choices":[{"message":{"role":"assistant","content":"synthetic-result"}}]}`
	body := &trackedSensitiveBody{Reader: strings.NewReader(payload + strings.Repeat(" ", maxSensitiveResponseBytes+1))}
	client := NewClient("https://synthetic.invalid/v1", "")
	client.Logger = logs.New("synthetic-instance", "Synthetic", 100)
	client.HTTPClient.Transport = sensitiveRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})
	response, err := client.Chat(context.Background(), core.ChatRequest{})
	if err != nil || response == nil || response.Message.Content != "synthetic-result" {
		t.Fatalf("ordinary large response behavior changed: %+v %v", response, err)
	}
	if body.readBytes <= maxSensitiveResponseBytes || !body.closed || len(client.Logger.Snapshot()) == 0 {
		t.Fatal("ordinary response lifecycle or logging changed")
	}
}
