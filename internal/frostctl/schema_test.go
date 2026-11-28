package frostctl

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScopeAndFlagValidationBeforeConnection(t *testing.T) {
	for _, args := range [][]string{
		{"status"}, {"status", "--scope", "global"}, {"remote", "list", "--remote", "example"},
		{"config", "list", "--instance", "synthetic", "--scope", "global"},
		{"config", "set", "BOT_NAME", "--instance", "synthetic"},
		{"doctor", "--check", "unexpected"}, {"doctor", "--check", "onebot"},
		{"logs", "clear", "--scope", "control-plane"},
		{"version", "--remote", "example"},
		{"instance", "list", "--timeout", "0s"}, {"instance", "list", "--output", "jsonl"},
		{"config", "secret", "set", "ONEBOT_WS_TOKEN", "--instance", "synthetic"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
	}
	for _, args := range [][]string{
		{"doctor", "--check", "management", "--check", "storage"},
		{"instance", "list", "--remote", "example"}, {"instance", "list"},
		{"config", "secret", "set", "ONEBOT_WS_TOKEN", "--instance", "synthetic", "--stdin"},
		{"logs", "tail", "--scope", "control-plane", "--output", "jsonl"},
	} {
		if _, err := Parse(args); err != nil {
			t.Errorf("rejected %v: %v", args, err)
		}
	}
}
func TestMachineOutputAndDiscovery(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"schema", "config", "--output", "json"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d %s", code, stderr.String())
	}
	var result struct {
		Status string
		Data   []Command
	}
	if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Status != "success" || len(result.Data) == 0 {
		t.Fatal(stdout.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"status", "--output", "json"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("parameter exit=%d", code)
	}
	if !json.Valid(stdout.Bytes()) || stderr.Len() != 0 {
		t.Fatal("machine output was mixed with diagnostics")
	}
}

func TestRunRejectsInvalidTimeoutBeforeSideEffects(t *testing.T) {
	for _, value := range []string{"invalid", "0", "0s", "-1s", "999999999999999999999h", ""} {
		var out, diagnostics bytes.Buffer
		code := Run(context.Background(), []string{"remote", "add", "synthetic", "--url", "https://example.test", "--timeout=" + value, "--output=json"}, strings.NewReader(""), &out, &diagnostics)
		var result Result
		if json.Unmarshal(out.Bytes(), &result) != nil || code != 2 || result.Error == nil || result.Error.Code != "invalid_argument" || !strings.Contains(result.Error.Message, "--timeout") {
			t.Fatalf("timeout %q: code=%d result=%s", value, code, out.String())
		}
	}
	for _, limit := range []string{"0", "1001", "invalid"} {
		if _, err := Parse([]string{"security", "audit", "list", "--instance", "synthetic", "--limit", limit}); err == nil {
			t.Fatalf("audit limit %q accepted", limit)
		}
	}
}
