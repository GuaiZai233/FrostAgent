package instanceconfig

import (
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
	for _, key := range []string{"LISTEN_ADDR", "BRAIN_PATH", "DIALOGUE_PATH", "SYSTEM_PROMPT"} {
		if err := a.Update(key, "bad", false); err == nil {
			t.Fatal("foreign scope allowed", key)
		}
	}
	if err := a.Replace("BOT_NAME=valid\nSYSTEM_PROMPT=bad"); err == nil {
		t.Fatal("raw foreign key accepted")
	}
	if a.Get("BOT_NAME") != "b" {
		t.Fatal("partial raw update")
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
