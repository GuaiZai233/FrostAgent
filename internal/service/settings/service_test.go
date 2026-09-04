package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "FrostAgent/gen/proto/frostagent/v1"
)

func TestSettingsListEnvVarsReturnsUnmaskedValues(t *testing.T) {
	t.Setenv("UPSTREAM_API_KEY", "sk-secret-key-123456789")
	t.Setenv("BOT_NAME", "FrostFox")

	svc := New(".env.nonexistent")
	resp, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil {
		t.Fatalf("ListEnvVars failed: %v", err)
	}

	var foundKey, foundName bool
	for _, envVar := range resp.Msg.GetEnvVars() {
		if envVar.GetKey() == "UPSTREAM_API_KEY" {
			foundKey = true
			if envVar.GetValue() != "sk-secret-key-123456789" {
				t.Fatalf("expected unmasked secret value for single-admin console, got %q", envVar.GetValue())
			}
			if !envVar.GetIsSecret() {
				t.Fatalf("expected UPSTREAM_API_KEY to have isSecret=true")
			}
		}
		if envVar.GetKey() == "BOT_NAME" {
			foundName = true
			if envVar.GetValue() != "FrostFox" {
				t.Fatalf("expected BOT_NAME %q, got %q", "FrostFox", envVar.GetValue())
			}
		}
	}

	if !foundKey || !foundName {
		t.Fatalf("expected to find UPSTREAM_API_KEY and BOT_NAME in response")
	}
}

func TestSettingsUpdateEnvVarAllowlist(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := New(envPath)

	// 1. Updating an allowed key should succeed
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "TestBot",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("expected UpdateEnvVar to succeed, got error: %s", resp.Msg.GetError())
	}
	if os.Getenv("BOT_NAME") != "TestBot" {
		t.Fatalf("expected in-process env BOT_NAME to be TestBot, got %s", os.Getenv("BOT_NAME"))
	}

	// 2. Updating an arbitrary non-allowed key should be rejected
	resp, err = svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "ARBITRARY_UNSAFE_CMD",
		Value: "malicious",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected non-allowlisted key to be rejected")
	}
	if resp.Msg.GetError() == "" {
		t.Fatalf("expected error message for non-allowlisted key")
	}

	// 3. Empty key should be rejected
	resp, err = svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "",
		Value: "val",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected empty key to be rejected")
	}
}

func TestSettingsDeleteEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	os.WriteFile(envPath, []byte("BOT_NAME=DeleteMe\n"), 0600)
	os.Setenv("BOT_NAME", "DeleteMe")

	svc := New(envPath)

	// 1. Deleting non-allowlisted key should fail
	resp, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "SOME_RANDOM_KEY",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected non-allowlisted key delete to be rejected")
	}

	// 2. Deleting allowlisted key should succeed
	resp, err = svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "BOT_NAME",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("expected DeleteEnvVar to succeed, got error: %s", resp.Msg.GetError())
	}
	if os.Getenv("BOT_NAME") != "" {
		t.Fatalf("expected BOT_NAME to be unset")
	}
}

func TestSettingsRawEnvFileReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := New(envPath)

	content := "BOT_NAME=RawBot\nLISTEN_ADDR=127.0.0.1:8080\n"
	updateResp, err := svc.UpdateRawEnvFile(context.Background(), connect.NewRequest(&v1.UpdateRawEnvFileRequest{
		Content: content,
	}))
	if err != nil {
		t.Fatalf("UpdateRawEnvFile error: %v", err)
	}
	if !updateResp.Msg.GetSuccess() {
		t.Fatalf("expected UpdateRawEnvFile to succeed, got: %s", updateResp.Msg.GetError())
	}

	getResp, err := svc.GetRawEnvFile(context.Background(), connect.NewRequest(&v1.GetRawEnvFileRequest{}))
	if err != nil {
		t.Fatalf("GetRawEnvFile error: %v", err)
	}
	if getResp.Msg.GetContent() != content {
		t.Fatalf("expected content %q, got %q", content, getResp.Msg.GetContent())
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(envPath)
		if err != nil {
			t.Fatalf("stat failed: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected 0600 permissions, got %o", fi.Mode().Perm())
		}
	}
}

func TestSettingsTightensExisting0644File(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	// Pre-create an insecure 0644 file
	if err := os.WriteFile(envPath, []byte("BOT_NAME=PreExisting\n"), 0644); err != nil {
		t.Fatalf("write pre-existing file: %v", err)
	}

	// Service init should tighten permissions
	svc := New(envPath)

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(envPath)
		if err != nil {
			t.Fatalf("stat failed: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected New() to tighten 0644 file to 0600, got %o", fi.Mode().Perm())
		}
	}

	// Updating a value should keep 0600
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "TightenedBot",
	}))
	if err != nil || !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %v, resp=%v", err, resp)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(envPath)
		if err != nil {
			t.Fatalf("stat failed: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected mode 0600 after update, got %o", fi.Mode().Perm())
		}
	}
}

func TestSettingsConcurrentUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := New(envPath)

	keys := []string{
		"BOT_NAME",
		"SYSTEM_PROMPT",
		"MAX_CONTEXT_MESSAGES",
		"MAX_CONTEXT_CHARS",
		"GROUP_COMPACT_BUFFER_SIZE",
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(keys)*10)

	for i := 0; i < 10; i++ {
		for _, key := range keys {
			wg.Add(1)
			go func(k string, round int) {
				defer wg.Done()
				val := fmt.Sprintf("val_%s_%d", k, round)
				resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
					Key:   k,
					Value: val,
				}))
				if err != nil {
					errCh <- fmt.Errorf("concurrent update error on %s: %w", k, err)
					return
				}
				if !resp.Msg.GetSuccess() {
					errCh <- fmt.Errorf("concurrent update failed on %s: %s", k, resp.Msg.GetError())
					return
				}
			}(key, i)
		}
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Ensure all 5 keys exist in the final file
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read final env file: %v", err)
	}
	text := string(content)
	for _, key := range keys {
		if !strings.Contains(text, key+"=") {
			t.Fatalf("expected final env file to contain key %q, file content:\n%s", key, text)
		}
	}
}
