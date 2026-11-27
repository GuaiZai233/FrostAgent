package frostctl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
