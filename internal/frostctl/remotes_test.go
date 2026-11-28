package frostctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteOriginsAndTokenInvalidation(t *testing.T) {
	for _, value := range []string{"http://example.test", "https://user:secret@example.test", "https://example.test?token=x", "https://example.test/#fragment"} {
		if _, _, err := canonicalURL(value); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	_, a, _ := canonicalURL("https://EXAMPLE.test/a")
	_, b, _ := canonicalURL("https://example.test:443/b")
	if a != b {
		t.Fatal("default HTTPS ports changed origin")
	}
	dir := t.TempDir()
	t.Setenv("FROSTCTL_CONFIG_DIR", dir)
	invoke := func(args ...string) any {
		t.Helper()
		inv, err := Parse(args)
		if err != nil {
			t.Fatal(err)
		}
		data, err := remoteCommand(context.Background(), inv, strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	invoke("remote", "add", "lab", "--url", "https://example.test/a")
	_, err := withRemotes(context.Background(), func(s *remoteStore) (any, error) {
		r := s.Entries["lab"]
		r.Credential = "synthetic"
		r.Store = "file"
		r.TokenConfigured = true
		s.Entries["lab"] = r
		bytes, _ := json.Marshal(credential{Origin: a, Token: "synthetic-secret"})
		if err := privateWrite(filepath.Join(dir, "synthetic.secret"), bytes); err != nil {
			return nil, err
		}
		return nil, s.save()
	})
	if err != nil {
		t.Fatal(err)
	}
	visible := invoke("remote", "show", "lab")
	if visible.(Remote).Store != "file" {
		t.Fatal("credential storage method is hidden")
	}
	bytes, _ := json.Marshal(visible)
	if strings.Contains(string(bytes), "synthetic-secret") || strings.Contains(string(bytes), "credential") {
		t.Fatal("credential leaked")
	}
	invoke("remote", "update", "lab", "--url", "https://example.test/b")
	_, err = withRemotes(context.Background(), func(s *remoteStore) (any, error) {
		token, err := s.token(context.Background(), s.Entries["lab"])
		if token != "synthetic-secret" {
			t.Fatal("same origin lost token")
		}
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	invoke("remote", "update", "lab", "--url", "https://different.test")
	if _, err := os.Stat(filepath.Join(dir, "synthetic.secret")); !os.IsNotExist(err) {
		t.Fatal("old origin credential retained")
	}
	if invoke("remote", "show", "lab").(Remote).TokenConfigured {
		t.Fatal("token remained bound after origin change")
	}
}

func TestCredentialFallbackIsVisibleAndCancellationDoesNotWrite(t *testing.T) {
	store := &remoteStore{dir: t.TempDir()}
	data := []byte(`{"origin":"https://example.test:443","token":"synthetic-secret"}`)
	systemWrite := func(ctx context.Context, id string, got []byte) error {
		t.Helper()
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Fatal("system store has no bounded deadline")
		}
		return errors.New("synthetic unavailable")
	}
	kind, err := store.saveCredential(context.Background(), "synthetic", data, systemWrite)
	if err != nil || kind != "file" {
		t.Fatalf("fallback: %s %v", kind, err)
	}
	saved, err := os.ReadFile(filepath.Join(store.dir, "synthetic.secret"))
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatal("fallback credential was not saved")
	}
	visible := visibleRemote(Remote{Name: "lab", URL: "https://example.test", TokenConfigured: true, Credential: "synthetic", Store: kind})
	for _, format := range []string{"json", "table"} {
		var out, diagnostics bytes.Buffer
		if err := render(&out, &diagnostics, format, Result{Status: "success", Data: visible}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "store") || !strings.Contains(out.String(), "file") || strings.Contains(out.String(), "synthetic") {
			t.Fatalf("unsafe or incomplete output: %s", out.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.saveCredential(ctx, "cancelled", data, systemWrite); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.dir, "cancelled.secret")); !os.IsNotExist(err) {
		t.Fatal("cancelled operation wrote a fallback secret")
	}
}
