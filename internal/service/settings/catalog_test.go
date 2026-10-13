package settings

import (
	v1 "FrostAgent/gen/proto/frostagent/v1"
	"FrostAgent/internal/instanceconfig"
	"FrostAgent/internal/storage"
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestSettingsCatalogExcludesRetiredEntries(t *testing.T) {
	for _, key := range []string{
		"CODER_API_KEY", "UPSTREAM_API_KEY", "MAX_CONTEXT_CHARS", "ALLOW_REMOTE_MCP_MANAGEMENT",
		"ENABLE_ONEBOT_ADAPTER", "ENABLE_ASTRBOT_ADAPTER", "BRAIN_PATH", "DIALOGUE_PATH", "ONEBOT_WS_PATH", "ASTRBOT_WS_PATH",
	} {
		if _, exists := knownEnvVars[key]; exists {
			t.Fatalf("retired field is still exposed: %s", key)
		}
		if err := validateUpdate(key, "synthetic"); err == nil {
			t.Fatalf("retired field remains writable: %s", key)
		}
	}
}

func TestBillingInputValidationMatchesRuntime(t *testing.T) {
	for _, key := range []string{"BILLING_PROMPT_PRICE_PER_MILLION", "BILLING_COMPLETION_PRICE_PER_MILLION"} {
		for _, value := range []string{"1.5", "-1", "NaN", "9223372036854775808"} {
			if err := validateUpdate(key, value); err == nil {
				t.Fatalf("accepted ignored price %s=%s", key, value)
			}
		}
		for _, value := range []string{"", "0", "2000"} {
			if err := validateUpdate(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, value := range []string{"0", "-1", "NaN", "Inf", "bad"} {
		if err := validateUpdate("BILLING_SAFETY_MULTIPLIER", value); err == nil {
			t.Fatalf("accepted ignored multiplier %s", value)
		}
	}
	for _, value := range []string{"", "0.001", "1.2"} {
		if err := validateUpdate("BILLING_SAFETY_MULTIPLIER", value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSettingsResetHintsExplainCodeFallbackAndInheritance(t *testing.T) {
	db, err := storage.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	global, err := instanceconfig.OpenDatabase(db, "", true)
	if err != nil {
		t.Fatal(err)
	}
	config, err := instanceconfig.OpenDatabase(db, "a1b2c3d4", false)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewScoped(config, global, nil)
	if err := global.Update("SECURITY_GATEWAY_TIMEOUT", "50s", false); err != nil {
		t.Fatal(err)
	}
	if err := config.Update("SECURITY_GATEWAY_TIMEOUT", "35s", false); err != nil {
		t.Fatal(err)
	}
	if err := config.Update("GROUP_COMPACT_BUFFER_SIZE", "30", false); err != nil {
		t.Fatal(err)
	}
	response, err := svc.ListEnvVars(context.Background(), connect.NewRequest(&v1.ListEnvVarsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	hints := make(map[string]string)
	for _, value := range response.Msg.EnvVars {
		hints[value.Key] = value.ResetHint
	}
	if !strings.Contains(hints["MAX_CONTEXT_MESSAGES"], "50 条") ||
		!strings.Contains(hints["SECURITY_GATEWAY_TIMEOUT"], "继承全局配置") || !strings.Contains(hints["SECURITY_GATEWAY_TIMEOUT"], "50s") ||
		!strings.Contains(hints["SECURITY_CLASSIFIER_TIMEOUT"], "当前实例") || !strings.Contains(hints["SECURITY_CLASSIFIER_TIMEOUT"], "35s") ||
		!strings.Contains(hints["GROUP_COMPACT_MAX_BUFFER_SIZE"], "300 条") {
		t.Fatalf("reset hints do not match actual fallbacks: %#v", hints)
	}
}
