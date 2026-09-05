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

func TestSettingsStructuredMutationOnRawDotenv(t *testing.T) {
	// Raw .env containing:
	// - Comments and blank lines
	// - 'export KEY=value' syntax
	// - 'KEY: value' colon separator syntax
	// - Quoted multiline values with internal comments
	// - Duplicate keys (BOT_NAME defined twice)
	initialRaw := `# Header comment
export BOT_NAME=FirstBotName
LISTEN_ADDR: 127.0.0.1:8080

# Prompt block with multiline quotes
SYSTEM_PROMPT="line 1 of prompt
line 2 of prompt
line 3 with # inline comment"
BOT_NAME=DuplicateOldBotName

# Trailing footer comment
`
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to write initial .env: %v", err)
	}

	svc := mustNew(t, envPath)

	// 1. Update BOT_NAME:
	// - First occurrence (export BOT_NAME=FirstBotName) must be replaced with canonical BOT_NAME=NewBot
	// - Duplicate occurrence (BOT_NAME=DuplicateOldBotName) must be eliminated
	// - Comments, blank lines, LISTEN_ADDR, and multiline SYSTEM_PROMPT must be preserved intact
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "NewBot",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text := string(content)

	// Verify comments and non-target statements preserved
	if !strings.Contains(text, "# Header comment") {
		t.Errorf("expected # Header comment preserved in .env:\n%s", text)
	}
	if !strings.Contains(text, "# Trailing footer comment") {
		t.Errorf("expected # Trailing footer comment preserved in .env:\n%s", text)
	}
	if !strings.Contains(text, "LISTEN_ADDR: 127.0.0.1:8080") {
		t.Errorf("expected LISTEN_ADDR: 127.0.0.1:8080 preserved in .env:\n%s", text)
	}
	// Verify duplicate was removed and not duplicated
	if strings.Count(text, "BOT_NAME") != 1 {
		t.Errorf("expected exactly 1 BOT_NAME statement, found %d in:\n%s", strings.Count(text, "BOT_NAME"), text)
	}
	if strings.Contains(text, "DuplicateOldBotName") {
		t.Errorf("expected duplicate old bot name removed from .env:\n%s", text)
	}

	// Verify godotenv parse matches expectation
	parsed, err := godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed on modified .env:\n%s\nerr: %v", text, err)
	}
	if parsed["BOT_NAME"] != "NewBot" {
		t.Errorf("parsed BOT_NAME = %q, want %q", parsed["BOT_NAME"], "NewBot")
	}
	expectedPrompt := "line 1 of prompt\nline 2 of prompt\nline 3 with # inline comment"
	if parsed["SYSTEM_PROMPT"] != expectedPrompt {
		t.Errorf("parsed SYSTEM_PROMPT = %q, want %q", parsed["SYSTEM_PROMPT"], expectedPrompt)
	}

	// 2. Update SYSTEM_PROMPT:
	// - Entire multiline statement should be replaced cleanly
	// - No leftover orphan lines ("line 2...", "line 3...")
	resp, err = svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SYSTEM_PROMPT",
		Value: "single line prompt",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	if strings.Contains(text, "line 2 of prompt") || strings.Contains(text, "line 3 with") {
		t.Fatalf("orphan continuation lines from multiline statement were left behind in .env:\n%s", text)
	}

	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed: %v\ncontent:\n%s", err, text)
	}
	if parsed["SYSTEM_PROMPT"] != "single line prompt" {
		t.Errorf("parsed SYSTEM_PROMPT = %q, want %q", parsed["SYSTEM_PROMPT"], "single line prompt")
	}

	// 3. Update colon-separated LISTEN_ADDR:
	// - LISTEN_ADDR: 127.0.0.1:8080 should be recognized and replaced
	resp, err = svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "LISTEN_ADDR",
		Value: "127.0.0.1:9090",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	if strings.Contains(text, "127.0.0.1:8080") {
		t.Fatalf("old colon-separated LISTEN_ADDR value was not replaced in .env:\n%s", text)
	}
	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed: %v\ncontent:\n%s", err, text)
	}
	if parsed["LISTEN_ADDR"] != "127.0.0.1:9090" {
		t.Errorf("parsed LISTEN_ADDR = %q, want %q", parsed["LISTEN_ADDR"], "127.0.0.1:9090")
	}

	// 4. Delete BOT_NAME:
	// - Statement should be removed cleanly
	delResp, err := svc.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "BOT_NAME",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteEnvVar failed: %s", delResp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	if strings.Contains(text, "BOT_NAME") {
		t.Fatalf("BOT_NAME still exists in .env after DeleteEnvVar:\n%s", text)
	}
	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed: %v\ncontent:\n%s", err, text)
	}
	if _, ok := parsed["BOT_NAME"]; ok {
		t.Fatalf("BOT_NAME still parsed after DeleteEnvVar")
	}
}

func TestSettingsUpdateEnvVarNullByteRejection(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	initialContent := "BOT_NAME=GoodBot\n"
	if err := os.WriteFile(envPath, []byte(initialContent), 0600); err != nil {
		t.Fatalf("failed to write initial .env: %v", err)
	}

	svc := mustNew(t, envPath)

	// Attempt to set a value containing a NUL byte (\x00)
	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "bad\x00value",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if resp.Msg.GetSuccess() {
		t.Fatalf("expected UpdateEnvVar with NUL byte to be rejected")
	}
	if !strings.Contains(resp.Msg.GetError(), "null byte (NUL)") {
		t.Fatalf("expected error message to mention null byte, got: %s", resp.Msg.GetError())
	}

	// Verify .env file on disk was NOT modified
	diskContent, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read .env: %v", err)
	}
	if string(diskContent) != initialContent {
		t.Fatalf("expected .env content unchanged, got:\n%s", string(diskContent))
	}

	// Verify formatEnvEntry itself rejects NUL bytes
	if _, err := formatEnvEntry("BOT_NAME", "null\x00byte"); err == nil {
		t.Fatalf("expected formatEnvEntry to fail for value containing NUL byte")
	}
}

func TestSettingsQuotedMultilineWithDoubleBackslashBeforeQuote(t *testing.T) {
	// Pinned godotenv v1.5.1 semantics test:
	// A quoted multiline value contains an internal quote preceded by two backslashes.
	// In godotenv v1.5.1, `prevChar := src[i-1]; prevChar == '\\'` treats any quote
	// with a preceding backslash as escaped, without checking backslash count parity.
	// Therefore, the statement does NOT terminate at the internal quote; the closing quote
	// is on the subsequent line.
	initialRaw := `SYSTEM_PROMPT="line 1 with \\"
line 2 of prompt"
BOT_NAME=GoodBot
`
	// Verify godotenv v1.5.1 parses the initial file as expected
	parsedInitial, err := godotenv.Unmarshal(initialRaw)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed on initial raw:\n%s\nerr: %v", initialRaw, err)
	}
	expectedInitialPrompt := "line 1 with \\\"\nline 2 of prompt"
	if parsedInitial["SYSTEM_PROMPT"] != expectedInitialPrompt {
		t.Fatalf("expected initial parsed SYSTEM_PROMPT %q, got %q", expectedInitialPrompt, parsedInitial["SYSTEM_PROMPT"])
	}
	if parsedInitial["BOT_NAME"] != "GoodBot" {
		t.Fatalf("expected initial parsed BOT_NAME %q, got %q", "GoodBot", parsedInitial["BOT_NAME"])
	}

	// 1. Update SYSTEM_PROMPT:
	// The entire multiline statement must be replaced without leaving orphan continuation lines.
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to write initial .env: %v", err)
	}
	svc := mustNew(t, envPath)

	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SYSTEM_PROMPT",
		Value: "single line new prompt",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read .env: %v", err)
	}
	text := string(content)
	if strings.Contains(text, "line 2 of prompt") {
		t.Fatalf("orphan continuation line left in .env:\n%s", text)
	}
	parsedAfterPromptUpdate, err := godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after prompt update:\n%s\nerr: %v", text, err)
	}
	if parsedAfterPromptUpdate["SYSTEM_PROMPT"] != "single line new prompt" {
		t.Errorf("parsed SYSTEM_PROMPT = %q, want %q", parsedAfterPromptUpdate["SYSTEM_PROMPT"], "single line new prompt")
	}
	if parsedAfterPromptUpdate["BOT_NAME"] != "GoodBot" {
		t.Errorf("parsed BOT_NAME = %q, want %q", parsedAfterPromptUpdate["BOT_NAME"], "GoodBot")
	}

	// 2. Reset and update BOT_NAME:
	// SYSTEM_PROMPT multiline with \\" must be preserved intact and uncorrupted.
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to reset .env: %v", err)
	}
	svc2 := mustNew(t, envPath)

	resp, err = svc2.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "UpdatedBot",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read .env: %v", err)
	}
	text = string(content)
	parsedAfterBotUpdate, err := godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after bot update:\n%s\nerr: %v", text, err)
	}
	if parsedAfterBotUpdate["SYSTEM_PROMPT"] != expectedInitialPrompt {
		t.Errorf("parsed SYSTEM_PROMPT = %q, want %q", parsedAfterBotUpdate["SYSTEM_PROMPT"], expectedInitialPrompt)
	}
	if parsedAfterBotUpdate["BOT_NAME"] != "UpdatedBot" {
		t.Errorf("parsed BOT_NAME = %q, want %q", parsedAfterBotUpdate["BOT_NAME"], "UpdatedBot")
	}

	// 3. Reset and delete SYSTEM_PROMPT:
	// Must completely remove the multiline statement and leave BOT_NAME intact.
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to reset .env: %v", err)
	}
	svc3 := mustNew(t, envPath)

	delResp, err := svc3.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "SYSTEM_PROMPT",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteEnvVar failed: %s", delResp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read .env: %v", err)
	}
	text = string(content)
	if strings.Contains(text, "SYSTEM_PROMPT") || strings.Contains(text, "line 2 of prompt") {
		t.Fatalf("SYSTEM_PROMPT or orphan continuation line still present:\n%s", text)
	}
	parsedAfterDelete, err := godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after delete:\n%s\nerr: %v", text, err)
	}
	if _, ok := parsedAfterDelete["SYSTEM_PROMPT"]; ok {
		t.Fatalf("SYSTEM_PROMPT still parsed after delete")
	}
	if parsedAfterDelete["BOT_NAME"] != "GoodBot" {
		t.Errorf("parsed BOT_NAME = %q, want %q", parsedAfterDelete["BOT_NAME"], "GoodBot")
	}
}

func TestSettingsMultipleStatementsOnSameLine(t *testing.T) {
	// Raw .env containing multiple variable statements on the same physical line:
	// e.g. KEY1="quoted" KEY2=value
	initialRaw := `SYSTEM_PROMPT="hello" BOT_NAME=InlineBot
`
	// Verify godotenv v1.5.1 parses both variables from the same physical line
	parsedInitial, err := godotenv.Unmarshal(initialRaw)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed on initial raw:\n%s\nerr: %v", initialRaw, err)
	}
	if parsedInitial["SYSTEM_PROMPT"] != "hello" || parsedInitial["BOT_NAME"] != "InlineBot" {
		t.Fatalf("unexpected godotenv parse of same-line statements: %v", parsedInitial)
	}

	// 1. Update SYSTEM_PROMPT:
	// Must update SYSTEM_PROMPT while BOT_NAME is NOT lost!
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to write initial .env: %v", err)
	}
	svc := mustNew(t, envPath)

	resp, err := svc.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "SYSTEM_PROMPT",
		Value: "new hello",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text := string(content)
	parsed, err := godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed on updated .env:\n%s\nerr: %v", text, err)
	}
	if parsed["SYSTEM_PROMPT"] != "new hello" {
		t.Errorf("parsed SYSTEM_PROMPT = %q, want %q", parsed["SYSTEM_PROMPT"], "new hello")
	}
	if parsed["BOT_NAME"] != "InlineBot" {
		t.Errorf("parsed BOT_NAME was lost or corrupted! Got %q, want %q (content:\n%s)", parsed["BOT_NAME"], "InlineBot", text)
	}

	// 2. Delete SYSTEM_PROMPT:
	// Must delete SYSTEM_PROMPT while BOT_NAME is NOT lost!
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to reset .env: %v", err)
	}
	svc2 := mustNew(t, envPath)

	delResp, err := svc2.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "SYSTEM_PROMPT",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteEnvVar failed: %s", delResp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after delete:\n%s\nerr: %v", text, err)
	}
	if _, ok := parsed["SYSTEM_PROMPT"]; ok {
		t.Errorf("SYSTEM_PROMPT still present after delete (content:\n%s)", text)
	}
	if parsed["BOT_NAME"] != "InlineBot" {
		t.Errorf("parsed BOT_NAME was lost when deleting sibling statement on same line! Got %q, want %q (content:\n%s)", parsed["BOT_NAME"], "InlineBot", text)
	}

	// 3. Update BOT_NAME:
	// Must update BOT_NAME while SYSTEM_PROMPT is NOT lost!
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to reset .env: %v", err)
	}
	svc3 := mustNew(t, envPath)

	resp, err = svc3.UpdateEnvVar(context.Background(), connect.NewRequest(&v1.UpdateEnvVarRequest{
		Key:   "BOT_NAME",
		Value: "NewBot",
	}))
	if err != nil {
		t.Fatalf("UpdateEnvVar error: %v", err)
	}
	if !resp.Msg.GetSuccess() {
		t.Fatalf("UpdateEnvVar failed: %s", resp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after bot update:\n%s\nerr: %v", text, err)
	}
	if parsed["SYSTEM_PROMPT"] != "hello" {
		t.Errorf("SYSTEM_PROMPT was lost or corrupted! Got %q, want %q (content:\n%s)", parsed["SYSTEM_PROMPT"], "hello", text)
	}
	if parsed["BOT_NAME"] != "NewBot" {
		t.Errorf("BOT_NAME = %q, want %q", parsed["BOT_NAME"], "NewBot")
	}

	// 4. Delete BOT_NAME:
	// Must delete BOT_NAME while SYSTEM_PROMPT is NOT lost!
	if err := os.WriteFile(envPath, []byte(initialRaw), 0600); err != nil {
		t.Fatalf("failed to reset .env: %v", err)
	}
	svc4 := mustNew(t, envPath)

	delResp, err = svc4.DeleteEnvVar(context.Background(), connect.NewRequest(&v1.DeleteEnvVarRequest{
		Key: "BOT_NAME",
	}))
	if err != nil {
		t.Fatalf("DeleteEnvVar error: %v", err)
	}
	if !delResp.Msg.GetSuccess() {
		t.Fatalf("DeleteEnvVar failed: %s", delResp.Msg.GetError())
	}

	content, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env failed: %v", err)
	}
	text = string(content)
	parsed, err = godotenv.Unmarshal(text)
	if err != nil {
		t.Fatalf("godotenv.Unmarshal failed after bot delete:\n%s\nerr: %v", text, err)
	}
	if parsed["SYSTEM_PROMPT"] != "hello" {
		t.Errorf("SYSTEM_PROMPT was lost when deleting sibling statement on same line! Got %q, want %q (content:\n%s)", parsed["SYSTEM_PROMPT"], "hello", text)
	}
	if _, ok := parsed["BOT_NAME"]; ok {
		t.Errorf("BOT_NAME still present after delete (content:\n%s)", text)
	}
}
