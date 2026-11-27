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
