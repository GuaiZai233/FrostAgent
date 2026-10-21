package actionscat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"FrostAgent/internal/core"
)

func TestClientDisabledBlocksRequestsAndResumes(t *testing.T) {
	var disabled atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client := New(func(key string) string {
		switch key {
		case "ACTIONSCAT_ENDPOINT":
			return server.URL
		case "ACTIONSCAT_ENABLED":
			if disabled.Load() {
				return "false"
			}
		}
		return ""
	})
	ctx := context.Background()
	disabled.Store(true)
	for _, call := range []struct {
		name string
		fn   func() error
	}{
		{"health", func() error { _, err := client.Health(ctx); return err }},
		{"list", func() error { _, err := client.ListActions(ctx); return err }},
		{"run", func() error { _, err := client.TriggerRun(ctx, "test-action", ManualRunReq{}); return err }},
		{"build", func() error { _, err := client.BuildVersion(ctx, "test-action", "test-version"); return err }},
		{"dispatch", func() error { return client.Dispatch(ctx, map[string]string{"text": "test"}) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			err := call.fn()
			if !errors.Is(err, ErrDisabled) || errors.Is(err, ErrBuildUnknownResult) {
				t.Fatalf("expected definite disabled error, got %v", err)
			}
		})
	}
	status := client.Status(ctx)
	if !status.Disabled || !status.Configured || status.Healthy || status.Authenticated || status.Error != ErrDisabled.Error() {
		t.Fatalf("disabled status = %+v", status)
	}
	if err := client.DispatchIncoming(ctx, core.IncomingMessage{MessageType: "private", Content: "test"}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("disabled client contacted backend")
	}
	disabled.Store(false)
	if _, err := client.ListActions(ctx); err != nil || requests.Load() != 1 {
		t.Fatalf("re-enable failed: requests=%d, error=%v", requests.Load(), err)
	}
}

func TestClientEnableSwitchValues(t *testing.T) {
	for _, value := range []string{"false", " FALSE ", "0", "no", "off"} {
		if New(func(string) string { return value }).IsEnabled() {
			t.Fatalf("%q should disable ActionsCat", value)
		}
	}
	for _, value := range []string{"", "true", "TRUE", "1", "yes", "on"} {
		if !New(func(string) string { return value }).IsEnabled() {
			t.Fatalf("%q should enable ActionsCat", value)
		}
	}
}
