package instanceconfig

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoresNeverMutateProcessEnvironment(t *testing.T) {
	t.Setenv("BOT_NAME", "process-name")
	dir := t.TempDir()
	a, _ := Open(filepath.Join(dir, "a.env"), false)
	b, _ := Open(filepath.Join(dir, "b.env"), false)
	if err := a.Update("BOT_NAME", "a", false); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("BOT_NAME") != "process-name" || b.Get("BOT_NAME") != "" {
		t.Fatal("environment leaked")
	}
	if err := a.Replace("# preserve comment\nBOT_NAME=b\nCUSTOM_VALUE=test\n"); err != nil {
		t.Fatal(err)
	}
	raw, _ := a.Raw()
	if raw != "# preserve comment\nBOT_NAME=b\nCUSTOM_VALUE=test\n" {
		t.Fatal(raw)
	}
	for _, key := range []string{"LISTEN_ADDR", "BRAIN_PATH", "DIALOGUE_PATH"} {
		if err := a.Update(key, "bad", false); err == nil {
			t.Fatal("foreign scope allowed", key)
		}
	}
	if err := a.Update("SYSTEM_PROMPT", "valid prompt\nmultiline", false); err != nil {
		t.Fatalf("SYSTEM_PROMPT must be allowed on instance store: %v", err)
	}
	if a.Get("SYSTEM_PROMPT") != "valid prompt\nmultiline" {
		t.Fatalf("SYSTEM_PROMPT value mismatch: %q", a.Get("SYSTEM_PROMPT"))
	}
	if err := a.Replace("BOT_NAME=valid\nLISTEN_ADDR=bad"); err == nil {
		t.Fatal("raw foreign key accepted")
	}
	if a.Get("BOT_NAME") != "b" {
		t.Fatal("partial raw update")
	}
}

func TestGlobalStorePreservesProcessEnvironmentPrecedence(t *testing.T) {
	t.Setenv("SANDBOX_ENABLED", "true")
	t.Setenv("SANDBOX_AUTH_TOKEN", "external-synthetic-token")
	t.Setenv("BOT_NAME", "external-instance-name")
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.env")
	if err := os.WriteFile(globalPath, []byte("SANDBOX_ENABLED=false\nSANDBOX_AUTH_TOKEN=file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	global, err := Open(globalPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if global.Get("SANDBOX_ENABLED") != "true" || global.Get("SANDBOX_AUTH_TOKEN") != "external-synthetic-token" {
		t.Fatal("global Store did not preserve process environment precedence")
	}
	if err = global.Update("SANDBOX_ENABLED", "false", false); err != nil {
		t.Fatal(err)
	}
	if global.Get("SANDBOX_ENABLED") != "true" {
		t.Fatal("file edit bypassed the process environment override")
	}

	instancePath := filepath.Join(dir, "instance.env")
	if err = os.WriteFile(instancePath, []byte("BOT_NAME=file-instance-name\n"), 0600); err != nil {
		t.Fatal(err)
	}
	instance, err := Open(instancePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Get("BOT_NAME") != "file-instance-name" {
		t.Fatal("process environment leaked into instance-owned configuration")
	}
}

func TestConcurrentUpdatesAreAtomic(t *testing.T) {
	c, _ := Open(filepath.Join(t.TempDir(), "instance.env"), false)
	var wg sync.WaitGroup
	for _, key := range []string{"TEST_A", "TEST_B", "TEST_C", "TEST_D"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for range 10 {
				if err := c.Update(key, "value", false); err != nil {
					t.Error(err)
				}
			}
		}(key)
	}
	wg.Wait()
	loaded, err := Open(c.path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Snapshot()) != 4 {
		t.Fatal("lost updates")
	}
}

func TestAppendToRawEnvWithoutFinalNewline(t *testing.T) {
	for _, raw := range []string{"BOT_NAME=old", "BOT_NAME=\"old\"", "# keep-comment"} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			c, err := Open(path, false)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Replace(raw); err != nil {
				t.Fatal(err)
			}
			if err = c.Update("ADMIN_QQ_IDS", "synthetic", false); err != nil {
				t.Fatal(err)
			}
			if c.Get("ADMIN_QQ_IDS") != "synthetic" {
				t.Fatal("appended field missing")
			}
			if raw != "# keep-comment" && c.Get("BOT_NAME") != "old" {
				t.Fatal("sibling field changed")
			}
			reloaded, err := Open(path, false)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Get("ADMIN_QQ_IDS") != "synthetic" || reloaded.Get("BOT_NAME") != c.Get("BOT_NAME") {
				t.Fatal("persisted values differ")
			}
		})
	}
}

func TestApplyScopeMetadataIsDisjoint(t *testing.T) {
	for key := range InstanceRestartKeys {
		if GlobalKeys[key] || ControlPlaneRestartKeys[key] {
			t.Fatalf("instance restart key %s has conflicting scope", key)
		}
	}
	for _, key := range []string{
		"LISTEN_ADDR", "WS_LISTEN_ADDR", "HTTP_ALLOWED_ORIGINS",
		"ALCYONE_BASE_URL", "ALCYONE_SERVICE_TOKEN", "ALCYONE_TIMEOUT",
		"SANDBOX_BASE_URL", "SANDBOX_AUTH_TOKEN", "SANDBOX_SESSION_NAMESPACE",
		"MCP_CONTROL_TOKEN", "ADMIN_TOKEN", "ALLOW_REMOTE_MCP_MANAGEMENT", "MCP_ENFORCE_LOCAL_TOKEN",
	} {
		if !GlobalKeys[key] || !ControlPlaneRestartKeys[key] {
			t.Fatalf("%s must require a Control Plane restart", key)
		}
	}
	for _, key := range []string{"WS_ALLOWED_ORIGINS", "SANDBOX_ENABLED"} {
		if !GlobalKeys[key] || ControlPlaneRestartKeys[key] {
			t.Fatalf("%s must remain global and hot", key)
		}
	}
	if GlobalKeys["SYSTEM_PROMPT"] {
		t.Fatal("SYSTEM_PROMPT must not be in GlobalKeys")
	}
}

func TestWriteAtomicDurableReportsPostCommitSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := syncDirectory
	syncDirectory = func(string) error { return errors.New("synthetic directory sync failure") }
	t.Cleanup(func() { syncDirectory = original })

	committed, err := WriteAtomicDurable(path, []byte("BOT_NAME=committed\n"), 0600)
	if !committed || err == nil {
		t.Fatalf("committed=%v err=%v, want committed post-rename error", committed, err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "BOT_NAME=committed\n" {
		t.Fatalf("rename did not commit visible data: %q", data)
	}
}
