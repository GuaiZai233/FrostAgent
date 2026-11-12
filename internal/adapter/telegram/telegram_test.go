package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/GuaiZai233/FrostAgent/internal/adapter/chatflow"
	"github.com/GuaiZai233/FrostAgent/internal/core"
)

// All numeric IDs and tokens in this file are synthetic protocol fixtures.
const fixtureToken = "900000:synthetic-test-token"
const fixtureUserID int64 = 41
const fixtureBotID int64 = 42
const fixtureChatID int64 = -43

func testClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := newClient(Config{Token: fixtureToken})
	c.baseURL = server.URL
	// Use the test server's transport, bypassing machine-wide proxy settings.
	c.http.Transport = server.Client().Transport
	t.Cleanup(c.http.CloseIdleConnections)
	return c
}

func replyJSON(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}
func fixtureMessage() *message {
	return &message{ID: 1, From: &user{ID: fixtureUserID, FirstName: "Fixture"}, Chat: chat{ID: fixtureChatID, Type: "supergroup"}, Text: "hello", Date: 1}
}

func TestConfigDefaultsValidationAndSecretErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		valid  bool
	}{
		{"default", map[string]string{}, true},
		{"enabled-missing-token", map[string]string{"TELEGRAM_ENABLED": "true"}, false},
		{"invalid-boolean", map[string]string{"TELEGRAM_ENABLED": "bad-secret"}, false},
		{"valid", map[string]string{"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": fixtureToken}, true},
		{"invalid-token", map[string]string{"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": "secret/value"}, false},
		{"proxy", map[string]string{"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": fixtureToken, "TELEGRAM_PROXY_URL": "socks5://fixture:secret@127.0.0.1:1080"}, true},
		{"bad-proxy", map[string]string{"TELEGRAM_ENABLED": "true", "TELEGRAM_BOT_TOKEN": fixtureToken, "TELEGRAM_PROXY_URL": "file:///secret"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(func(k string) string { return tc.values[k] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), fixtureToken) || strings.Contains(err.Error(), "secret/value")) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestClientDoesNotLeakTokenOrBlindlyRetrySend(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
		fmt.Fprintf(w, `{"ok":false,"error_code":500,"description":%q}`, fixtureToken)
	})
	err := c.call(context.Background(), "sendMessage", map[string]any{"text": "test"}, nil, "", nil)
	if err == nil || strings.Contains(err.Error(), fixtureToken) || calls.Load() != 1 {
		t.Fatalf("unsafe send error/retry: %v calls=%d", err, calls.Load())
	}
	c.http.Transport = failingTransport{}
	err = c.call(context.Background(), "sendMessage", nil, nil, "", nil)
	if err == nil || strings.Contains(err.Error(), fixtureToken) {
		t.Fatalf("transport leaked: %v", err)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("request URL: " + r.URL.String())
}

func TestClientRetriesExplicitRateLimitAndCancelsWait(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":1}}`)
			return
		}
		replyJSON(w, true)
	})
	if err := c.call(context.Background(), "sendMessage", nil, nil, "", nil); err != nil || calls.Load() != 2 {
		t.Fatalf("retry: %v calls=%d", err, calls.Load())
	}
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":60}}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.call(ctx, "sendMessage", nil, nil, "", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait cancellation: %v", err)
	}
}

func TestNormalizeUTF16MentionsRepliesAndScope(t *testing.T) {
	a := &Adapter{}
	bot := user{ID: fixtureBotID, Username: "fixture_bot"}
	m := fixtureMessage()
	m.Text = "😀 @fixture_bot hello"
	m.Entities = []entity{{Type: "mention", Offset: 3, Length: 12}}
	input, accepted, err := a.normalize(context.Background(), m, bot)
	if err != nil || !accepted || !input.IsAt || input.Message.Content != "😀  hello" {
		t.Fatalf("mention normalization: %+v accepted=%v err=%v", input, accepted, err)
	}
	m = fixtureMessage()
	m.Reply = &message{ID: 2, Chat: m.Chat, From: &user{ID: fixtureBotID}, Text: "earlier reply"}
	input, _, err = a.normalize(context.Background(), m, bot)
	if err != nil || !input.IsAt || input.Quote == nil || input.Quote.ID != messageID(fixtureChatID, 2) {
		t.Fatalf("quote: %+v %v", input, err)
	}
	m.Reply.Chat.ID--
	input, _, _ = a.normalize(context.Background(), m, bot)
	if input.Quote != nil || input.IsAt {
		t.Fatal("cross-chat quote accepted")
	}
	m.ThreadID = 1
	if _, accepted, _ = a.normalize(context.Background(), m, bot); accepted {
		t.Fatal("unsupported topic mixed into group session")
	}
}

func TestNormalizeCommandsOnlyWakeTheirBot(t *testing.T) {
	a := &Adapter{}
	bot := user{ID: fixtureBotID, Username: "fixture_bot"}
	for _, tc := range []struct {
		text string
		wake bool
	}{{"/start", true}, {"/start@fixture_bot", true}, {"/start@other_bot", false}} {
		m := fixtureMessage()
		m.Text = tc.text
		m.Entities = []entity{{Type: "bot_command", Offset: 0, Length: len(tc.text)}}
		input, _, err := a.normalize(context.Background(), m, bot)
		if err != nil || input.IsWake != tc.wake {
			t.Fatalf("command %s: wake=%v err=%v", tc.text, input.IsWake, err)
		}
	}
}

func TestPhotoIsDownloadedWithoutCredentialURLInInput(t *testing.T) {
	image := []byte("GIF89a fixture image")
	a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getFile") {
			replyJSON(w, map[string]any{"file_path": "photos/fixture.gif"})
			return
		}
		_, _ = w.Write(image)
	})}
	m := fixtureMessage()
	m.Photos = []photo{{FileID: "large", Size: maxMediaBytes + 1, Width: 999, Height: 999}, {FileID: "small", Size: len64(image), Width: 1, Height: 1}}
	input, ok, err := a.normalize(context.Background(), m, user{})
	if err != nil || !ok || len(input.Message.Attachments) != 1 {
		t.Fatalf("photo: %+v %v", input, err)
	}
	att := input.Message.Attachments[0]
	if att.URL != "" || string(att.Content) != string(image) {
		t.Fatalf("photo must carry bytes, got %+v", att)
	}
}
func len64(b []byte) int64 { return int64(len(b)) }

func TestDeliverySplitsUnicodeAndKeepsMentionEntities(t *testing.T) {
	var requests []map[string]any
	a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		requests = append(requests, p)
		replyJSON(w, map[string]any{"message_id": 1})
	})}
	text := strings.Repeat("😀", 2047) + "a"
	err := a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "-43"}, Parts: []chatflow.Part{{Type: "quote", MessageID: messageID(fixtureChatID, 2)}, {Type: "plain", Text: text}, {Type: "mention_user", MentionUserID: "41"}, {Type: "plain", Text: " done"}}})
	if err != nil || len(requests) != 2 {
		t.Fatalf("chunks=%d err=%v", len(requests), err)
	}
	joined := ""
	for _, r := range requests {
		part := r["text"].(string)
		if len(utf16.Encode([]rune(part))) > 4096 {
			t.Fatal("oversized chunk")
		}
		joined += part
	}
	if joined != text+"@41 done" || requests[0]["reply_parameters"] == nil || requests[1]["reply_parameters"] != nil || requests[1]["entities"] == nil {
		t.Fatalf("lost text, quote or mention: %+v", requests)
	}
	if err = a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "-43"}, Parts: []chatflow.Part{{Type: "quote", MessageID: "-999/2"}, {Type: "plain", Text: "bad"}}}); err == nil {
		t.Fatal("cross-chat outgoing quote accepted")
	}
}

func TestDeliveryRejectsLocalPathsAndKeepsAttachmentOrder(t *testing.T) {
	var methods []string
	a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		replyJSON(w, true)
	})}
	err := a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "41", Content: "before photo", Attachments: []core.Attachment{{Type: core.AttachmentTypeImage, URL: "https://example.invalid/image.png"}}}})
	if err != nil || strings.Join(methods, ",") != "sendMessage,sendPhoto" {
		t.Fatalf("order=%v err=%v", methods, err)
	}
	for _, source := range []string{`C:\secret.png`, "file:///etc/secret", "/tmp/secret"} {
		err = a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "41"}, Parts: []chatflow.Part{{Type: "image", Path: source}}})
		if err == nil {
			t.Fatal("host path accepted")
		}
	}
}

type fakeProcessor struct {
	mu          sync.Mutex
	inputs      []chatflow.Input
	completions []chan struct{}
	submitted   chan struct{}
}

func (p *fakeProcessor) Submit(in chatflow.Input) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inputs = append(p.inputs, in)
	done := make(chan struct{})
	p.completions = append(p.completions, done)
	p.submitted <- struct{}{}
	return done
}
func (p *fakeProcessor) Close() {}

func TestPollingReceivesWhilePendingAndConfirmsOnlyContinuousPrefix(t *testing.T) {
	p := &fakeProcessor{submitted: make(chan struct{}, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polled := make(chan int64, 10)
	var polls atomic.Int32
	a := NewAdapter(Config{Enabled: true, Token: fixtureToken}, func(chatflow.Sender) chatflow.Processor { return p }, nil)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "getMe") {
			replyJSON(w, user{ID: fixtureBotID, Username: "fixture_bot"})
			return
		}
		var params struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&params)
		polled <- params.Offset
		if params.Offset >= 12 {
			cancel()
			replyJSON(w, []update{})
			return
		}
		m := fixtureMessage()
		updates := []update{{ID: 10, Message: m}}
		if polls.Add(1) > 1 {
			second := *m
			second.ID = 2
			second.Text = "correction while the first turn is running"
			updates = append(updates, update{ID: 11, Message: &second})
		}
		replyJSON(w, updates)
	})
	finished := make(chan struct{})
	go func() { a.Run(ctx); close(finished) }()
	t.Cleanup(func() { a.Close(); <-finished })
	for range 2 {
		select {
		case <-p.submitted:
		case <-time.After(3 * time.Second):
			t.Fatal("pending turn blocked subsequent intake")
		}
	}
	p.mu.Lock()
	if len(p.inputs) != 2 {
		t.Fatalf("duplicate submitted: %d", len(p.inputs))
	}
	close(p.completions[1])
	p.mu.Unlock()
	// A later completion cannot acknowledge the still-running earlier update.
	for range 3 {
		select {
		case offset := <-polled:
			if offset != 0 {
				t.Fatalf("premature offset %d", offset)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("polling stopped while pending")
		}
	}
	p.mu.Lock()
	if len(p.inputs) != 2 {
		t.Fatalf("repeated updates submitted: %d", len(p.inputs))
	}
	close(p.completions[0])
	p.mu.Unlock()
	select {
	case offset := <-polled:
		if offset != 12 {
			t.Fatalf("completed prefix offset=%d", offset)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("completed prefix not acknowledged")
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("poller did not cancel")
	}
	a.Close()
	if a.Ready() {
		t.Fatal("closed adapter is ready")
	}
}

func TestConstructionDisabledAndConflictDoNotStartConsumers(t *testing.T) {
	var calls atomic.Int32
	p := &fakeProcessor{submitted: make(chan struct{}, 1)}
	a := NewAdapter(Config{}, func(chatflow.Sender) chatflow.Processor { t.Fatal("disabled processor created"); return p }, nil)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); replyJSON(w, true) })
	a.Run(context.Background())
	a.Close()
	if calls.Load() != 0 {
		t.Fatal("disabled adapter dialed")
	}
	a = NewAdapter(Config{Enabled: true, Token: fixtureToken}, func(chatflow.Sender) chatflow.Processor { return p }, nil)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if strings.HasSuffix(r.URL.Path, "getMe") {
			replyJSON(w, user{ID: fixtureBotID})
			return
		}
		w.WriteHeader(409)
		fmt.Fprint(w, `{"ok":false,"error_code":409}`)
	})
	if calls.Load() != 0 {
		t.Fatal("constructor dialed")
	}
	a.Run(context.Background())
	a.Close()
	if calls.Load() != 2 || a.Ready() {
		t.Fatalf("conflict retried or left ready: calls=%d", calls.Load())
	}
}

func TestDeliveryReportsOnlyConfirmedParts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []chatflow.Part
		text  string
	}{
		{"long-text", []chatflow.Part{{Type: "plain", Text: strings.Repeat("a", 4096) + "unsent"}}, strings.Repeat("a", 4096)},
		{"text-then-media", []chatflow.Part{{Type: "plain", Text: "confirmed"}, {Type: "image", URL: "https://example.invalid/image.png"}}, "confirmed"},
		{"media-then-text", []chatflow.Part{{Type: "image", URL: "https://example.invalid/image.png"}, {Type: "plain", Text: "unsent"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					replyJSON(w, true)
					return
				}
				w.WriteHeader(500)
				fmt.Fprint(w, `{"ok":false,"error_code":500}`)
			})}
			err := a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "41"}, Parts: tc.parts})
			var partial *chatflow.DeliveryError
			if !errors.As(err, &partial) || !partial.Confirmed || partial.DeliveredText != tc.text || calls.Load() != 2 {
				t.Fatalf("partial=%+v err=%v calls=%d", partial, err, calls.Load())
			}
		})
	}
}

func TestDeliveryMediaReplySurvivesURLAndMultipart(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(fmt.Sprint(upload), func(t *testing.T) {
			var received map[string]any
			a := &Adapter{client: testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if upload {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
					}
					defer r.MultipartForm.RemoveAll()
					var reply map[string]any
					if err := json.Unmarshal([]byte(r.FormValue("reply_parameters")), &reply); err != nil {
						t.Error(err)
					}
					received = map[string]any{"reply_parameters": reply}
					file, _, err := r.FormFile("photo")
					if err != nil {
						t.Error(err)
					} else {
						file.Close()
					}
				} else {
					_ = json.NewDecoder(r.Body).Decode(&received)
				}
				replyJSON(w, true)
			})}
			if upload {
				// Byte attachments exercise the same multipart reply path as trusted stickers.
				if err := a.sendAttachment(context.Background(), fixtureChatID, core.Attachment{Type: core.AttachmentTypeImage, Content: []byte("GIF89a fixture")}, 2); err != nil {
					t.Fatal(err)
				}
			} else {
				err := a.deliver(context.Background(), chatflow.Delivery{Action: "send_message", Message: core.OutgoingMessage{TargetID: "-43"}, Parts: []chatflow.Part{{Type: "quote", MessageID: "-43/2"}, {Type: "image", URL: "https://example.invalid/image.png"}}})
				if err != nil {
					t.Fatal(err)
				}
			}
			reply, ok := received["reply_parameters"].(map[string]any)
			if !ok || reply["message_id"] != float64(2) {
				t.Fatalf("lost media reply: %+v", received)
			}
		})
	}
}

func TestPollingRetriesTruncatedPhotoWithoutAcknowledging(t *testing.T) {
	p := &fakeProcessor{submitted: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var downloads atomic.Int32
	var polls atomic.Int32
	a := NewAdapter(Config{Enabled: true, Token: fixtureToken}, func(chatflow.Sender) chatflow.Processor { return p }, nil)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "getMe"):
			replyJSON(w, user{ID: fixtureBotID})
		case strings.HasSuffix(r.URL.Path, "getUpdates"):
			var params struct {
				Offset int64 `json:"offset"`
			}
			_ = json.NewDecoder(r.Body).Decode(&params)
			if polls.Add(1) <= 2 && params.Offset != 0 {
				t.Errorf("photo prematurely confirmed: %d", params.Offset)
			}
			m := fixtureMessage()
			m.Photos = []photo{{FileID: "fixture-photo", Width: 1, Height: 1}}
			replyJSON(w, []update{{ID: 10, Message: m}})
		case strings.HasSuffix(r.URL.Path, "getFile"):
			replyJSON(w, map[string]any{"file_path": "photos/fixture.gif"})
		default:
			if downloads.Add(1) == 1 {
				w.Header().Set("Content-Length", "100")
			}
			_, _ = w.Write([]byte("GIF89a fixture"))
		}
	})
	finished := make(chan struct{})
	go func() { a.Run(ctx); close(finished) }()
	t.Cleanup(func() { a.Close(); <-finished })
	select {
	case <-p.submitted:
	case <-time.After(4 * time.Second):
		t.Fatal("truncated image was skipped instead of retried")
	}
	if downloads.Load() != 2 || polls.Load() != 2 {
		t.Fatalf("downloads=%d polls=%d", downloads.Load(), polls.Load())
	}
	a.Close()
}
