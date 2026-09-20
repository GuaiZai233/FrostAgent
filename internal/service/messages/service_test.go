package messages_test

import (
	"FrostAgent/internal/core"
	"FrostAgent/internal/service/messages"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type mockAdapter struct {
	id       string
	mu       sync.Mutex
	messages []core.OutgoingMessage
	err      error
}

func (m *mockAdapter) ID() string {
	return m.id
}

func (m *mockAdapter) Send(ctx context.Context, msg core.OutgoingMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, msg)
	return nil
}

const defaultTestToken = "secret_test_token_abc"

func authedGetenv(key string) string {
	if key == "FROSTAGENT_ACTIONSCAT_TOKEN" || key == "FROSTAGENT_API_KEY" {
		return defaultTestToken
	}
	return ""
}

func newAuthedRequest(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+defaultTestToken)
	return req
}

func TestService_Auth(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)

	// 1. FAIL-CLOSED INVARIANT: When no server token is configured, requests MUST return HTTP 503
	t.Run("server unconfigured fail-closed 503", func(t *testing.T) {
		unconfiguredSvc := messages.New(dispatcher, "inst_mock_1", func(s string) string { return "" })
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(`{"platform":"mock_platform","target_id":"t1","content":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer any_token")
		w := httptest.NewRecorder()
		unconfiguredSvc.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 Service Unavailable when server has no token configured, got %d: %s", w.Code, w.Body.String())
		}
	})

	validToken := "secret_test_token_abc"
	getenv := func(key string) string {
		if key == "FROSTAGENT_ACTIONSCAT_TOKEN" {
			return validToken
		}
		return ""
	}

	svc := messages.New(dispatcher, "inst_mock_1", getenv)

	makeReq := func(authHeader, frostKey, authToken string) *http.Response {
		body := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if frostKey != "" {
			req.Header.Set("X-FrostAgent-Key", frostKey)
		}
		if authToken != "" {
			req.Header.Set("X-Auth-Token", authToken)
		}
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		return w.Result()
	}

	// 2. Missing auth -> 401
	resp := makeReq("", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", resp.StatusCode)
	}

	// 3. Invalid Bearer token -> 401
	resp = makeReq("Bearer invalid_token", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid Bearer token, got %d", resp.StatusCode)
	}

	// 4. Valid Bearer token -> 200
	resp = makeReq("Bearer "+validToken, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid Bearer token, got %d", resp.StatusCode)
	}

	// 5. Valid X-FrostAgent-Key -> 200
	resp = makeReq("", validToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid X-FrostAgent-Key, got %d", resp.StatusCode)
	}

	// 6. Valid X-Auth-Token -> 200
	resp = makeReq("", "", validToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid X-Auth-Token, got %d", resp.StatusCode)
	}
}

func TestService_InstanceBindingDefense(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)

	svc := messages.New(dispatcher, "bound_instance_id_123", authedGetenv)

	// Mismatched instance ID should be rejected with 400 Bad Request
	bodyMismatched := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello","instance_id":"attacker_instance_999"}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyMismatched)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for instance tampering, got %d: %s", w.Code, w.Body.String())
	}

	// Matching instance ID should succeed
	bodyMatching := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello","instance_id":"bound_instance_id_123"}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyMatching)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for matching instance_id, got %d: %s", w.Code, w.Body.String())
	}

	// Empty instance ID should succeed
	bodyEmpty := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello"}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyEmpty)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty instance_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestService_MessageNormalizationAndDispatch(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)

	svc := messages.New(dispatcher, "", authedGetenv)

	t.Run("direct content and target", func(t *testing.T) {
		body := `{"platform":"mock_platform","target_id":"target_test_2","content":"normalized text"}`
		req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		if len(adapter.messages) == 0 {
			t.Fatal("expected message to be dispatched to adapter")
		}
		last := adapter.messages[len(adapter.messages)-1]
		if last.Content != "normalized text" || last.TargetID != "target_test_2" || last.Platform != "mock_platform" {
			t.Fatalf("unexpected message fields: %+v", last)
		}
	})

	t.Run("session string parsing fallback", func(t *testing.T) {
		body := `{"session":"mock_platform:private:user_test_3","content":"session parsed text"}`
		req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		last := adapter.messages[len(adapter.messages)-1]
		if last.Content != "session parsed text" || last.TargetID != "user_test_3" || last.MessageType != "private" {
			t.Fatalf("unexpected parsed fields: %+v", last)
		}
	})

	t.Run("segmented messages array", func(t *testing.T) {
		body := `{
			"platform":"mock_platform",
			"target_id":"target_test_4",
			"messages":[
				{"content":"first segment"},
				{"content":"second segment","attachments":[{"type":"image","url":"http://example.com/test.png"}]}
			]
		}`
		req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp messages.SendMessageResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Count != 2 {
			t.Fatalf("expected count 2, got %d", resp.Count)
		}
	})

	t.Run("actionscat sdk format interoperability", func(t *testing.T) {
		// ActionsCat actionscat.Reply sends:
		// {"session": "mock_platform:group:grp_888", "messages": [{"type": "plain", "text": "hello from actions"}]}
		body := `{
			"session": "mock_platform:group:grp_888",
			"messages": [
				{"type": "plain", "text": "hello from actions"}
			]
		}`
		req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
		w := httptest.NewRecorder()
		svc.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for ActionsCat SDK payload, got %d: %s", w.Code, w.Body.String())
		}

		adapter.mu.Lock()
		defer adapter.mu.Unlock()
		last := adapter.messages[len(adapter.messages)-1]
		if last.Platform != "mock_platform" {
			t.Fatalf("expected Platform 'mock_platform', got %q", last.Platform)
		}
		if last.TargetID != "grp_888" {
			t.Fatalf("expected TargetID 'grp_888', got %q", last.TargetID)
		}
		if last.MessageType != "group" {
			t.Fatalf("expected MessageType 'group', got %q", last.MessageType)
		}
		if last.Content != "hello from actions" {
			t.Fatalf("expected Content 'hello from actions', got %q", last.Content)
		}
	})
}

func TestService_PlatformFallback(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	onebotAdapter := &mockAdapter{id: "onebot"}
	dispatcher.RegisterAdapter(onebotAdapter)

	svc := messages.New(dispatcher, "", authedGetenv)

	// Dispatch to "qq" should fall back to "onebot" adapter
	body := `{"platform":"qq","target_id":"group_test_qq","content":"qq to onebot"}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	onebotAdapter.mu.Lock()
	defer onebotAdapter.mu.Unlock()
	if len(onebotAdapter.messages) == 0 {
		t.Fatal("expected message to reach onebot adapter via qq fallback")
	}
}

func TestService_DispatchFailure(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	failingAdapter := &mockAdapter{id: "mock_failing", err: errors.New("network failure")}
	dispatcher.RegisterAdapter(failingAdapter)

	svc := messages.New(dispatcher, "", authedGetenv)

	body := `{"platform":"mock_failing","target_id":"target_test_fail","content":"fail text"}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", body)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway, got %d: %s", w.Code, w.Body.String())
	}
}

func TestService_MessageTypeValidation(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// 1. Invalid message_type at top level must return 400 Bad Request
	bodyInvalidTop := `{"platform":"mock_platform","message_type":"groupp","target_id":"123","content":"hi"}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyInvalidTop)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for misspelled message_type, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Invalid message_type in messages array must return 400 Bad Request
	bodyInvalidArr := `{"platform":"mock_platform","target_id":"123","messages":[{"message_type":"user","content":"hi"}]}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyInvalidArr)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid message_type in messages array, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Invalid message_type in session string must return 400 Bad Request
	bodyInvalidSess := `{"session":"mock_platform:other:123","content":"hi"}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyInvalidSess)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid message_type in session string, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Valid private message_type must succeed
	bodyPrivate := `{"platform":"mock_platform","message_type":"private","target_id":"user_456","content":"secret"}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyPrivate)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid private message_type, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last := adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if last.MessageType != "private" {
		t.Fatalf("expected MessageType 'private', got %q", last.MessageType)
	}

	// 5. Valid group message_type must succeed
	bodyGroup := `{"platform":"mock_platform","message_type":"group","target_id":"grp_789","content":"hello group"}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyGroup)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid group message_type, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last = adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if last.MessageType != "group" {
		t.Fatalf("expected MessageType 'group', got %q", last.MessageType)
	}
}

func TestService_ImageUrlCompatibility(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// 1. AstrBot image_url segment inside messages array
	bodyArr := `{
		"session": "mock_platform:group:grp_888",
		"messages": [
			{"type": "image_url", "url": "https://example.com/cat.png"}
		]
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyArr)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for image_url payload, got %d: %s", w.Code, w.Body.String())
	}

	adapter.mu.Lock()
	last := adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if len(last.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(last.Attachments))
	}
	if last.Attachments[0].Type != core.AttachmentTypeImage {
		t.Fatalf("expected AttachmentTypeImage, got %q", last.Attachments[0].Type)
	}
	if last.Attachments[0].URL != "https://example.com/cat.png" {
		t.Fatalf("expected URL 'https://example.com/cat.png', got %q", last.Attachments[0].URL)
	}

	// 2. Top-level image_url payload
	bodyTop := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"type": "image_url",
		"url": "https://example.com/dog.png"
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyTop)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for top-level image_url payload, got %d: %s", w.Code, w.Body.String())
	}

	adapter.mu.Lock()
	last = adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if len(last.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(last.Attachments))
	}
	if last.Attachments[0].Type != core.AttachmentTypeImage {
		t.Fatalf("expected AttachmentTypeImage, got %q", last.Attachments[0].Type)
	}
	if last.Attachments[0].URL != "https://example.com/dog.png" {
		t.Fatalf("expected URL 'https://example.com/dog.png', got %q", last.Attachments[0].URL)
	}
}

func TestService_RecordAndStickerCompatibility(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// 1. "record" segment inside messages array -> maps to core.AttachmentTypeAudio
	bodyRecordArr := `{
		"session": "mock_platform:group:grp_888",
		"messages": [
			{"type": "record", "url": "https://example.com/voice.silk"}
		]
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyRecordArr)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for record payload, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last := adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if len(last.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(last.Attachments))
	}
	if last.Attachments[0].Type != core.AttachmentTypeAudio {
		t.Fatalf("expected AttachmentTypeAudio for 'record', got %q", last.Attachments[0].Type)
	}

	// 2. Top-level "record" payload
	bodyRecordTop := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"type": "record",
		"url": "https://example.com/top_voice.silk"
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyRecordTop)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for top-level record payload, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last = adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if len(last.Attachments) != 1 || last.Attachments[0].Type != core.AttachmentTypeAudio {
		t.Fatalf("expected 1 audio attachment, got %+v", last.Attachments)
	}

	// 3. Sticker segment with is_sticker: true -> SubType=1
	bodySticker := `{
		"session": "mock_platform:group:grp_888",
		"messages": [
			{"type": "image", "url": "https://example.com/fox_sticker.png", "is_sticker": true}
		]
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodySticker)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for sticker payload, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last = adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()
	if len(last.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(last.Attachments))
	}
	if last.Attachments[0].Type != core.AttachmentTypeImage {
		t.Fatalf("expected AttachmentTypeImage, got %q", last.Attachments[0].Type)
	}
	if last.Attachments[0].SubType != 1 {
		t.Fatalf("expected SubType=1 for sticker, got %d", last.Attachments[0].SubType)
	}
}

func TestService_UnsupportedSegmentTypes(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// 1. mention_user in messages array
	bodyMention := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"messages": [{"type": "mention_user", "mention_user_id": "123456"}]
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyMention)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mention_user, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mention_user and quote are currently unsupported") {
		t.Fatalf("expected explicit unsupported error in body, got: %s", w.Body.String())
	}

	// 2. quote in messages array
	bodyQuote := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"messages": [{"type": "quote", "message_id": "msg_999"}]
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyQuote)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for quote, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mention_user and quote are currently unsupported") {
		t.Fatalf("expected explicit unsupported error in body, got: %s", w.Body.String())
	}

	// 3. Unknown type
	bodyUnknown := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"messages": [{"type": "unknown_xyz", "text": "foo"}]
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyUnknown)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown type, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown message type") {
		t.Fatalf("expected unknown message type error, got: %s", w.Body.String())
	}
}

func TestService_ActionsCatWirePayloadIdempotentNormalization(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// ActionsCat PR #3 pre-normalizes: sends BOTH compatibility fields (type, url)
	// AND canonical attachments array with the same image.
	// FrostAgent normalization must be idempotent and NOT duplicate the attachment!
	wirePayload := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"messages": [
			{
				"type": "image",
				"url": "https://example.com/fox.png",
				"attachments": [
					{"type": "image", "url": "https://example.com/fox.png"}
				]
			}
		]
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", wirePayload)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last := adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()

	if len(last.Attachments) != 1 {
		t.Fatalf("expected exactly 1 attachment (no duplication), got %d: %+v", len(last.Attachments), last.Attachments)
	}
	if last.Attachments[0].URL != "https://example.com/fox.png" {
		t.Fatalf("unexpected attachment URL: %s", last.Attachments[0].URL)
	}
}

func TestService_ActionsCatWirePayloadStickerIdempotent(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	wirePayload := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"messages": [
			{
				"type": "image",
				"url": "https://example.com/fox_sticker.png",
				"is_sticker": true,
				"attachments": [
					{"type": "image", "url": "https://example.com/fox_sticker.png"}
				]
			}
		]
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", wirePayload)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	adapter.mu.Lock()
	last := adapter.messages[len(adapter.messages)-1]
	adapter.mu.Unlock()

	if len(last.Attachments) != 1 {
		t.Fatalf("expected exactly 1 attachment, got %d", len(last.Attachments))
	}
	if last.Attachments[0].SubType != 1 {
		t.Fatalf("expected SubType=1 for sticker, got %d", last.Attachments[0].SubType)
	}
}

func TestService_RejectPathOnlyAndFile(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)
	svc := messages.New(dispatcher, "", authedGetenv)

	// 1. File message type rejected with 400
	bodyFile := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"type": "file",
		"url": "https://example.com/report.pdf"
	}`
	req := newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyFile)
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for file type, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "file attachments are currently unsupported") {
		t.Fatalf("expected file unsupported error, got: %s", w.Body.String())
	}

	// 2. Path-only (no URL) rejected with 400
	bodyPathOnly := `{
		"platform": "mock_platform",
		"target_id": "grp_888",
		"type": "image",
		"path": "/sandbox/local/image.png"
	}`
	req = newAuthedRequest(http.MethodPost, "/api/v1/messages/send", bodyPathOnly)
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path-only media, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "requires a valid 'url'") {
		t.Fatalf("expected requires valid url error, got: %s", w.Body.String())
	}
}
