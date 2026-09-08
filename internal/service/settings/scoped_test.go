package settings

import (
	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/sandbox"
	"connectrpc.com/connect"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedSettingsRetainTrustBoundaryWithoutProcessEnv(t *testing.T) {
	t.Setenv("BOT_NAME", "process-sentinel")
	t.Setenv("SYSTEM_PROMPT", "shared-process-sentinel")
	dir := t.TempDir()
	c, err := instanceconfig.Open(filepath.Join(dir, "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := instanceconfig.Open(filepath.Join(dir, "global.env"), true)
	if err != nil {
		t.Fatal(err)
	}
	sandboxManager := sandbox.NewConfigManager(sandbox.Config{})
	svc := NewScoped(c, g, sandboxManager)
	if err = c.Replace("# keep-comment\nexport BOT_NAME = 'old'\nBOT_ALIASES=\"alias\" BOT_NAME=\"duplicate\"\n"); err != nil {
		t.Fatal(err)
	}
	update := func(key, value string) bool {
		t.Helper()
		res, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{Key: key, Value: value}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.Success
	}
	if !update(" BOT_NAME ", "new") {
		t.Fatal("valid update rejected")
	}
	raw, _ := c.Raw()
	if !strings.Contains(raw, "# keep-comment") || c.Get("BOT_ALIASES") != "alias" || strings.Count(raw, "BOT_NAME=") != 1 {
		t.Fatal("structured mutation damaged siblings")
	}
	for _, test := range []struct{ key, value string }{
		{"ARBITRARY_UNSAFE_CMD", "bad"},
		{"BOT_NAME", "bad\nINJECTED=yes"},
		{"BOT_NAME", "bad\x00value"},
		{"BRAIN_PATH", "external.json"},
	} {
		if update(test.key, test.value) {
			t.Fatalf("unsafe update accepted: %s", test.key)
		}
	}
	if !update("SYSTEM_PROMPT", "line1\nline2") || g.Get("SYSTEM_PROMPT") != "shared-process-sentinel" || c.Get("SYSTEM_PROMPT") != "" {
		t.Fatal("shared prompt scope or roundtrip broken")
	}
	globalRaw, err := g.Raw()
	if err != nil || !strings.Contains(globalRaw, "line1\\nline2") {
		t.Fatalf("shared prompt file was not updated behind its process override: raw=%q error=%v", globalRaw, err)
	}
	if !update("SANDBOX_ENABLED", "true") || !sandboxManager.Get().Enabled || g.Get("SANDBOX_ENABLED") != "true" {
		t.Fatal("shared sandbox enable did not apply immediately")
	}
	deleteResponse, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{Key: "SANDBOX_ENABLED"}))
	if err != nil || !deleteResponse.Msg.Success || sandboxManager.Get().Enabled {
		t.Fatalf("shared sandbox disable failed: response=%v error=%v", deleteResponse, err)
	}
	if !update("UPSTREAM_API_KEY", `C:\synthetic\`) || c.Get("UPSTREAM_API_KEY") != `C:\synthetic\` {
		t.Fatal("trailing backslash did not roundtrip")
	}
	if os.Getenv("BOT_NAME") != "process-sentinel" || os.Getenv("SYSTEM_PROMPT") != "shared-process-sentinel" {
		t.Fatal("scoped mutation changed process environment")
	}
}
