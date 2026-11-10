package balancequery

import (
	"encoding/json"
	"testing"
)

func TestProviderKeyIsNeverSentToAChangedOrigin(t *testing.T) {
	raw, _ := json.Marshal(Config{Curl: "curl https://old.example/v1/user/balance -H 'Authorization: Bearer {{API_KEY}}'", ProviderID: "provider-a"})
	get := func(string) string { return string(raw) }
	// The provider's published base URL moved to another origin after the reference was saved.
	moved := func(string) (ProviderCredential, error) {
		return ProviderCredential{APIKey: "synthetic-provider-key", BaseURL: "https://new.example/v1"}, nil
	}
	if _, err := LoadResolved(get, moved); err == nil {
		t.Fatal("provider key must not be used against a different origin")
	}
	sameOrigin := func(string) (ProviderCredential, error) {
		return ProviderCredential{APIKey: "synthetic-provider-key", BaseURL: "https://old.example/anything"}, nil
	}
	cfg, err := LoadResolved(get, sameOrigin)
	if err != nil || cfg.APIKey != "synthetic-provider-key" {
		t.Fatalf("same origin must resolve the provider key, got %v", err)
	}
}
