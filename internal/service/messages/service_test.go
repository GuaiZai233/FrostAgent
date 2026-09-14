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

func TestService_Auth(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)

	validToken := "secret_test_token_abc"
	getenv := func(key string) string {
		if key == "FROSTAGENT_API_KEY" {
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

	// 1. Missing auth
	resp := makeReq("", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing auth, got %d", resp.StatusCode)
	}

	// 2. Invalid Bearer token
	resp = makeReq("Bearer invalid_token", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid Bearer token, got %d", resp.StatusCode)
	}

	// 3. Valid Bearer token
	resp = makeReq("Bearer "+validToken, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid Bearer token, got %d", resp.StatusCode)
	}

	// 4. Valid X-FrostAgent-Key
	resp = makeReq("", validToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid X-FrostAgent-Key, got %d", resp.StatusCode)
	}

	// 5. Valid X-Auth-Token
	resp = makeReq("", "", validToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid X-Auth-Token, got %d", resp.StatusCode)
	}
}

func TestService_InstanceBindingDefense(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	adapter := &mockAdapter{id: "mock_platform"}
	dispatcher.RegisterAdapter(adapter)

	svc := messages.New(dispatcher, "bound_instance_id_123", func(s string) string { return "" })

	// Mismatched instance ID should be rejected with 400 Bad Request
	bodyMismatched := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello","instance_id":"attacker_instance_999"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(bodyMismatched))
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for instance tampering, got %d: %s", w.Code, w.Body.String())
	}

	// Matching instance ID should succeed
	bodyMatching := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello","instance_id":"bound_instance_id_123"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(bodyMatching))
	w = httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for matching instance_id, got %d: %s", w.Code, w.Body.String())
	}

	// Empty instance ID should succeed
	bodyEmpty := `{"platform":"mock_platform","target_id":"target_test_1","content":"hello"}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(bodyEmpty))
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

	svc := messages.New(dispatcher, "", func(s string) string { return "" })

	t.Run("direct content and target", func(t *testing.T) {
		body := `{"platform":"mock_platform","target_id":"target_test_2","content":"normalized text"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
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
		req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
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
}

func TestService_PlatformFallback(t *testing.T) {
	dispatcher := core.NewDefaultDispatcher()
	onebotAdapter := &mockAdapter{id: "onebot"}
	dispatcher.RegisterAdapter(onebotAdapter)

	svc := messages.New(dispatcher, "", func(s string) string { return "" })

	// Dispatch to "qq" should fall back to "onebot" adapter
	body := `{"platform":"qq","target_id":"group_test_qq","content":"qq to onebot"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
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

	svc := messages.New(dispatcher, "", func(s string) string { return "" })

	body := `{"platform":"mock_failing","target_id":"target_test_fail","content":"fail text"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/send", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	svc.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway, got %d: %s", w.Code, w.Body.String())
	}
}
