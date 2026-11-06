package balancequery

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCurlRequestSubset(t *testing.T) {
	for _, raw := range []string{DefaultCurl,
		`curl --url=http://127.0.0.1/balance -XPOST -H 'X-API-Key: {{API_KEY}}' --data-raw '{"currency":"CNY"}'`,
		`curl 'http://localhost/balance?key={{API_KEY}}' -G -d 'currency=CNY' -s -S`,
		"curl http://localhost/balance \\\r\n -H \"Authorization: Bearer {{API_KEY}}\"",
	} {
		if _, err := parseCurl(raw); err != nil {
			t.Errorf("valid request rejected: %v", err)
		}
	}
	for _, raw := range []string{
		`curl file:///etc/passwd -H 'X-Key: {{API_KEY}}'`,
		`curl https://user:pass@example.invalid -H 'X-Key: {{API_KEY}}'`,
		`curl http://localhost/{{API_KEY}}`,
		`curl http://localhost -H 'Authorization: Bearer literal-key'`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' -L`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' -k`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' --proxy http://proxy.invalid`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' -d @file`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' -H 'Host: other.invalid'`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' -X DELETE`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}' https://other.invalid`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}'; whoami`,
		`curl $(whoami) -H 'X-Key: {{API_KEY}}'`,
		`curl http://localhost -H 'X-Key: {{API_KEY}}`,
		"curl http://localhost -H 'X-Key: {{API_KEY}}\r\nInjected: yes'",
	} {
		if _, err := parseCurl(raw); err == nil {
			t.Errorf("unsafe/unsupported request accepted: %s", raw)
		}
	}
}

func TestQueryReturnsRawBodyAndInjectsKeyOnlyOnServer(t *testing.T) {
	key := "synthetic+balance/key&value=fixture"
	raw := " {\n  \"balance\": \"12.50\"\n}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Query().Get("key") != key || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("server request did not match administrator configuration")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"key":"`+key+`","currency":"CNY"}` {
			t.Errorf("unexpected request body: %s", body)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, raw)
	}))
	defer server.Close()
	cfg := Config{APIKey: key, Curl: fmt.Sprintf(`curl '%s/balance?key={{API_KEY}}' -H 'Authorization: Bearer {{API_KEY}}' -H 'Content-Type: application/json' --data-raw '{"key":"{{API_KEY}}","currency":"CNY"}'`, server.URL)}
	got, err := Execute(context.Background(), cfg)
	if err != nil || got != raw {
		t.Fatalf("raw result changed: %q, %v", got, err)
	}
}

func TestQueryBlocksRedirectsAndSensitiveOrOversizedResponses(t *testing.T) {
	key := "synthetic-balance-secret"
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	var escaped strings.Builder
	for _, ch := range key {
		fmt.Fprintf(&escaped, `\u%04x`, ch)
	}
	for _, test := range []struct {
		name, body string
		redirect   bool
	}{
		{"literal credential", key, false},
		{"base64 credential", base64.StdEncoding.EncodeToString([]byte(key)), false},
		{"escaped credential", `{"echo":"` + escaped.String() + `"}`, false},
		{"large response", strings.Repeat("x", MaxResponseBytes+1), false},
		{"binary response", string([]byte{0xff, 0xfe}), false},
		{"redirect", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.redirect {
					http.Redirect(w, r, target.URL, http.StatusFound)
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			result, err := Execute(context.Background(), Config{APIKey: key, Curl: fmt.Sprintf(`curl %s -H 'Authorization: Bearer {{API_KEY}}'`, server.URL)})
			if err == nil || result != "" || strings.Contains(err.Error(), key) {
				t.Fatalf("response boundary failed: result bytes=%d, error=%v", len(result), err)
			}
		})
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestQueryCancellationAndTransportErrorsDoNotLeakRequest(t *testing.T) {
	key := "synthetic-timeout-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	cfg := Config{APIKey: key, Curl: fmt.Sprintf(`curl '%s/balance?key={{API_KEY}}'`, server.URL)}
	result, err := Execute(ctx, cfg)
	if err == nil || result != "" || strings.Contains(err.Error(), key) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("unsafe error: %v", err)
	}
}
