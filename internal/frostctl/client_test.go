package frostctl

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnknownWriteAndRejectedWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reject" {
			http.Error(w, "rejected", 400)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	for _, tc := range []struct {
		path   string
		write  bool
		status string
	}{{"/write", true, "unknown"}, {"/read", false, "failed"}, {"/reject", true, "failed"}} {
		_, err := c.call(context.Background(), "POST", tc.path, map[string]string{"synthetic": "value"}, tc.write)
		var failure *commandError
		if !errors.As(err, &failure) || failure.Status != tc.status {
			t.Fatalf("%s: %v", tc.path, err)
		}
	}
}
func TestCredentialNeverSentToDifferentOrigin(t *testing.T) {
	c := &client{base: "https://different.test", origin: "https://expected.test:443", token: "synthetic-secret", http: http.DefaultClient}
	if _, err := c.request(context.Background(), "GET", "/api/version", nil, false); err == nil {
		t.Fatal("origin check failed")
	}
}
