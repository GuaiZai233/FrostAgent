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
	"github.com/joho/godotenv"

	v1 "FrostAgent/gen/proto/frostagent/v1"
)

func mustNew(t *testing.T, envPath string) *Service {
	t.Helper()
	svc, err := New(envPath)
	if err != nil {
		t.Fatalf("New(%q) failed: %v", envPath, err)
	}
	return svc
}

func TestSettingsListEnvVarsReturnsUnmaskedValues(t *testing.T) {
	t.Setenv("UPSTREAM_API_KEY", "sk-secret-key-123456789")
	t.Setenv("BOT_NAME", "FrostFox")

	svc := mustNew(t, ".env.nonexistent")
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
	svc := mustNew(t, envPath)

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

	svc := mustNew(t, envPath)

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
	svc := mustNew(t, envPath)

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
	svc := mustNew(t, envPath)

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
	svc := mustNew(t, envPath)

	keys := []string{
		"BOT_NAME",
		"SYSTEM_PROMPT",
		"MAX_CONTEXT_MESSAGES",
		"MAX_CONTEXT_CHARS",
		"GROUP_COMPACT_BUFFER_SIZE",
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(keys)*10)

	for i := range 10 {
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

func TestSettingsUpdateEnvVarNewlineInjectionRejection(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := mustNew(t, envPath)

	// 1. Single-line variable (BOT_NAME) must reject values with newlines
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "ok\nUNREGISTERED_KEY=pwned",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected BOT_NAME update with newline to be rejected")
	}
	if !strings.Contains(resp.Msg.GetError(), "does not allow multiline values") {
		t.Fatalf("unexpected error message: %s", resp.Msg.GetError())
	}

	// 2. Multiline variable (SYSTEM_PROMPT) accepts multiline text,
	// but escapes newlines into a single line in .env, preventing key injection
	multilinePrompt := "Line 1: Hello\nLine 2: UNREGISTERED_KEY=pwned\nLine 3: Goodbye"
	resp, err = svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SYSTEM_PROMPT",
		Value: multilinePrompt,
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("expected SYSTEM_PROMPT multiline update to succeed, got error: %s", resp.Msg.GetError())
	}

	// 3. Verify in-process env is the exact multiline value
	if os.Getenv("SYSTEM_PROMPT") != multilinePrompt {
		t.Fatalf("expected os.Getenv(SYSTEM_PROMPT) to be %q, got %q", multilinePrompt, os.Getenv("SYSTEM_PROMPT"))
	}

	// 4. Verify with godotenv parser that NO extra keys were injected
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	parsed, err := godotenv.Unmarshal(string(content))
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed on generated file: %v\ncontent:\n%s", err, string(content))
	}

	if val, ok := parsed["SYSTEM_PROMPT"]; !ok || val != multilinePrompt {
		t.Fatalf("expected parsed SYSTEM_PROMPT to be %q, got %q", multilinePrompt, val)
	}

	if _, injected := parsed["UNREGISTERED_KEY"]; injected {
		t.Fatalf("FATAL: UNREGISTERED_KEY was injected as an independent key into .env! Content:\n%s", string(content))
	}
	if _, injected := parsed["Line 2"]; injected {
		t.Fatalf("FATAL: 'Line 2' was parsed as an independent key! Content:\n%s", string(content))
	}
}

func TestSettingsConcurrentInterleavedUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := mustNew(t, envPath)

	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	// Interleave structured UpdateEnvVar and raw UpdateRawEnvFile
	for i := range 15 {
		// Goroutine 1: structured update on BOT_NAME
		wg.Add(1)
		go func(round int) {
			defer wg.Done()
			val := fmt.Sprintf("InterleavedBot_%d", round)
			resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
				Key:   "BOT_NAME",
				Value: val,
			}))
			if err != nil {
				errCh <- fmt.Errorf("UpdateEnvVar BOT_NAME error: %w", err)
				return
			}
			if !resp.Msg.GetSuccess() {
				errCh <- fmt.Errorf("UpdateEnvVar BOT_NAME failed: %s", resp.Msg.GetError())
				return
			}
		}(i)

		// Goroutine 2: structured update on SYSTEM_PROMPT
		wg.Add(1)
		go func(round int) {
			defer wg.Done()
			val := fmt.Sprintf("Prompt line 1\nPrompt line 2: %d", round)
			resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
				Key:   "SYSTEM_PROMPT",
				Value: val,
			}))
			if err != nil {
				errCh <- fmt.Errorf("UpdateEnvVar SYSTEM_PROMPT error: %w", err)
				return
			}
			if !resp.Msg.GetSuccess() {
				errCh <- fmt.Errorf("UpdateEnvVar SYSTEM_PROMPT failed: %s", resp.Msg.GetError())
				return
			}
		}(i)

		// Goroutine 3: raw file update
		wg.Add(1)
		go func(round int) {
			defer wg.Done()
			rawContent := fmt.Sprintf("BOT_NAME=RawBot_%d\nLISTEN_ADDR=127.0.0.1:8080\n", round)
			resp, err := svc.UpdateRawEnvFile(context.Background(), connect.NewRequest(&v1.UpdateRawEnvFileRequest{
				Content: rawContent,
			}))
			if err != nil {
				errCh <- fmt.Errorf("UpdateRawEnvFile error: %w", err)
				return
			}
			if !resp.Msg.GetSuccess() {
				errCh <- fmt.Errorf("UpdateRawEnvFile failed: %s", resp.Msg.GetError())
				return
			}
		}(i)

		// Goroutine 4: raw file read
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.GetRawEnvFile(context.Background(), connect.NewRequest(&v1.GetRawEnvFileRequest{}))
			if err != nil {
				errCh <- fmt.Errorf("GetRawEnvFile error: %w", err)
				return
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	// Verify file is readable and valid dotenv
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read final .env file: %v", err)
	}
	if _, err := godotenv.Unmarshal(string(content)); err != nil {
		t.Fatalf("final .env file is corrupted: %v\ncontent:\n%s", err, string(content))
	}
}

func TestSettingsDotenvRoundTripMatrix(t *testing.T) {
	// Round-trip test matrix covering:
	// - Trailing backslashes
	// - Backslash + quotes
	// - Ordinary Windows paths
	// - Dollar signs ($) and variable expansions
	// - Backticks (`)
	// - Exclamation marks (!)
	// - Empty values
	// - Leading/trailing and internal whitespace padding
	// - Multiline strings
	// - Quotes and JSON-like structures
	validMatrix := []struct {
		name  string
		value string
	}{
		// Trailing backslashes
		{"trailing backslash - windows dir", `C:\temp\`},
		{"trailing backslash - single slash", `\`},
		{"trailing backslash - word slash", `foo\`},
		{"trailing backslash - space dir", `C:\Program Files\App\`},

		// Backslash + quote
		{"backslash quote - word middle", `foo\"bar`},
		{"backslash quote - single escaped quote", `\"`},
		{"backslash quote - double escaped quote", `\"\"`},
		{"backslash quote - word suffix", `foo\"`},
		{"backslash quote - word prefix", `\"bar`},

		// Ordinary Windows paths
		{"windows path - program files", `C:\Program Files\App`},
		{"windows path - file with extension", `C:\Users\foo\bar.txt`},
		{"windows path - drive D yml", `D:\test\app\config.yml`},

		// Dollar signs ($)
		{"dollar - single", `$`},
		{"dollar - double", `$$`},
		{"dollar - path variable", `$PATH`},
		{"dollar - embedded in name", `user$name`},
		{"dollar - braced variable", `${VAR}`},
		{"dollar - escaped dollar", `\$`},
		{"dollar - escaped dollar with word", `\$PATH`},

		// Backticks (`)
		{"backtick - enclosed word", "`hello`"},
		{"backtick - single", "`"},
		{"backtick - multiple words", "`foo` `bar`"},

		// Exclamation marks (!)
		{"exclamation - suffix", `hello!`},
		{"exclamation - prefix number", `!123`},
		{"exclamation - wrapped word", `!foo!bar!`},

		// Empty value
		{"empty value", ""},

		// Whitespace padding
		{"whitespace - leading spaces", `  hello`},
		{"whitespace - trailing spaces", `hello  `},
		{"whitespace - both sides", `  hello  `},
		{"whitespace - single space", ` `},
		{"whitespace - tab", "\t"},
		{"whitespace - padded phrase", `  foo bar  `},

		// Multiline strings
		{"multiline - standard lf", "line1\nline2"},
		{"multiline - crlf", "line1\r\nline2"},
		{"multiline - single lf", "\n"},
		{"multiline - single crlf", "\r\n"},
		{"multiline - three lines", "a\nb\nc"},
		{"multiline - trailing lf", "line1\nline2\n"},

		// Quotes & structures
		{"quotes - double quoted word", `"quoted"`},
		{"quotes - single quoted word", `'single-quoted'`},
		{"structure - json map", `{"key": "value"}`},
		{"complex - quote and dollar", `hello "world" $FOO`},
	}

	for _, tc := range validMatrix {
		t.Run(tc.name, func(t *testing.T) {
			formatted, err := formatEnvEntry("TEST_KEY", tc.value)
			if err != nil {
				t.Fatalf("formatEnvEntry failed for %q: %v", tc.value, err)
			}

			parsed, err := godotenv.Unmarshal(formatted)
			if err != nil {
				t.Fatalf("godotenv.Unmarshal failed on %q (formatted: %s): %v", tc.value, formatted, err)
			}

			if got, ok := parsed["TEST_KEY"]; !ok || got != tc.value {
				t.Fatalf("round-trip mismatch for %q: got %q (formatted: %s)", tc.value, got, formatted)
			}

			if len(parsed) != 1 {
				t.Fatalf("unexpected keys parsed from formatted line %q: %v", formatted, parsed)
			}
		})
	}

	// Unrepresentable values that godotenv v1.5.1 cannot safely parse back
	// must be explicitly rejected before write.
	unrepresentableMatrix := []struct {
		name  string
		value string
	}{
		{"multiline with trailing backslash", "line1\nline2\\"},
		{"dollar with trailing backslash", "$PATH\\"},
	}

	for _, tc := range unrepresentableMatrix {
		t.Run("reject: "+tc.name, func(t *testing.T) {
			formatted, err := formatEnvEntry("TEST_KEY", tc.value)
			if err == nil {
				t.Fatalf("expected formatEnvEntry to fail for unrepresentable value %q, got: %s", tc.value, formatted)
			}
		})
	}
}

func TestSettingsUpdateEnvVarEndToEndMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	svc := mustNew(t, envPath)

	testCases := []struct {
		key   string
		value string
	}{
		{"BOT_NAME", `C:\Program Files\App\`},
		{"BOT_NAME", `foo\"bar`},
		{"BOT_NAME", `$PATH`},
		{"BOT_NAME", "`hello`"},
		{"BOT_NAME", `hello!`},
		{"BOT_NAME", `  padded bot  `},
		{"SYSTEM_PROMPT", "line1\nline2\nline3"},
		{"SYSTEM_PROMPT", `hello "world" $PROMPT`},
	}

	for _, tc := range testCases {
		resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
			Key:   tc.key,
			Value: tc.value,
		}))
		if err != nil {
			t.Fatalf("UpdateEnvVar RPC failed for key %s: %v", tc.key, err)
		}
		if !resp.Msg.GetSuccess() {
			t.Fatalf("UpdateEnvVar failed for key %s with value %q: %s", tc.key, tc.value, resp.Msg.GetError())
		}

		// 1. In-process env is immediately updated
		if got := os.Getenv(tc.key); got != tc.value {
			t.Fatalf("os.Getenv(%q) = %q, want %q", tc.key, got, tc.value)
		}

		// 2. .env file on disk parses with godotenv to the exact value
		content, err := os.ReadFile(envPath)
		if err != nil {
			t.Fatalf("failed to read .env: %v", err)
		}
		parsed, err := godotenv.Unmarshal(string(content))
		if err != nil {
			t.Fatalf("godotenv.Unmarshal failed on disk .env content:\n%s\nerr: %v", string(content), err)
		}
		if got, ok := parsed[tc.key]; !ok || got != tc.value {
			t.Fatalf("parsed[%q] = %q, want %q (file content: %s)", tc.key, got, tc.value, string(content))
		}
	}

	// 3. Unrepresentable value via UpdateEnvVar is rejected and does not corrupt .env
	badValue := "prompt line 1\nprompt line 2\\"
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SYSTEM_PROMPT",
		Value: badValue,
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar RPC failed: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected UpdateEnvVar to fail for unrepresentable value %q", badValue)
	}
	if !strings.Contains(resp.Msg.GetError(), "cannot be safely represented") &&
		!strings.Contains(resp.Msg.GetError(), "cannot be safely round-tripped") {
		t.Fatalf("unexpected error message: %s", resp.Msg.GetError())
	}
}

func TestSettingsHardenPermissionsFailure(t *testing.T) {
	tmpDir := t.TempDir()
	// Directory path instead of file path: os.Stat is Dir, should fail hardenPermissions
	_, err := New(tmpDir)
	if err == nil {
		t.Fatalf("expected New() on a directory path to fail, got nil error")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected error mentioning directory, got: %v", err)
	}
}
