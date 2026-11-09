package balancequery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKeylessTemplateIsUsableWithoutCredential(t *testing.T) {
	var sawAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"balance":"12.5"}`))
	}))
	defer server.Close()
	curl := "curl " + server.URL + "/balance -H 'Authorization: Bearer {{API_KEY}}'"

	keyless := Config{Curl: curl, Keyless: true}
	if err := keyless.Validate(); err != nil {
		t.Fatalf("keyless template must validate: %v", err)
	}
	body, err := Execute(context.Background(), keyless)
	if err != nil || body != `{"balance":"12.5"}` {
		t.Fatalf("keyless query = %q, %v", body, err)
	}
	if sawAuth {
		t.Fatal("keyless query must not send a credential header")
	}
	if _, err := Execute(context.Background(), Config{Curl: curl}); err == nil {
		t.Fatal("a template without a key and without keyless must still be refused")
	}
}

func TestConfiguredRequiresKeyOrKeyless(t *testing.T) {
	const curl = "curl https://example.invalid/balance -H 'Authorization: Bearer {{API_KEY}}'"
	stored := func(cfg Config) func(string) string {
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return func(key string) string {
			if key == ConfigKey {
				return string(raw)
			}
			return ""
		}
	}
	if Configured(func(string) string { return "" }) {
		t.Fatal("nothing saved must not be configured")
	}
	if !Configured(stored(Config{Curl: curl, Keyless: true})) {
		t.Fatal("saved keyless template must be configured")
	}
	if Configured(stored(Config{Curl: curl})) {
		t.Fatal("saved template without key or keyless flag must not be configured")
	}
}
