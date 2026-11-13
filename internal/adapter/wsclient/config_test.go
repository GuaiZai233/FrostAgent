package wsclient

import (
	"strings"
	"testing"
)

func TestLoadConnectionConfigDefaultsAndIsolation(t *testing.T) {
	values := map[string]string{
		"ONEBOT_WS_MODE": "forward", "ONEBOT_WS_URL": "wss://bot.example/ws?route=synthetic",
		"ONEBOT_WS_TOKEN":         "synthetic-client-token",
		"ONEBOT_WS_INBOUND_TOKEN": "synthetic-inbound-token",
	}
	getenv := func(key string) string { return values[key] }
	onebot, err := Load(getenv, "ONEBOT")
	if err != nil || onebot.Disabled || onebot.Mode != Forward || onebot.URL != values["ONEBOT_WS_URL"] || onebot.Token != values["ONEBOT_WS_TOKEN"] || onebot.InboundToken != values["ONEBOT_WS_INBOUND_TOKEN"] {
		t.Fatalf("forward config did not round trip: %v", err)
	}
	if _, err := Load(func(string) string { t.Fatal("retired adapter settings were read"); return "" }, "ASTRBOT"); err == nil {
		t.Fatal("retired adapter was accepted")
	}
	defaults, err := Load(nil, "ONEBOT")
	if err != nil || defaults.Disabled || defaults.Mode != Reverse {
		t.Fatalf("nil getter defaults: %#v %v", defaults, err)
	}
}

func TestInboundTokenValidation(t *testing.T) {
	for _, adapter := range []string{"ONEBOT"} {
		for _, invalid := range []string{"synthetic-secret\n", "synthetic-secret\t", "synthetic-secret ", "\x00synthetic-secret", "synthetic-secret\u00a0"} {
			_, err := Load(func(key string) string {
				if key == adapter+"_WS_INBOUND_TOKEN" {
					return invalid
				}
				return ""
			}, adapter)
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("invalid inbound credential accepted or disclosed")
			}
		}
	}
}

func TestLoadConnectionEnabledFlag(t *testing.T) {
	for _, adapter := range []string{"ONEBOT"} {
		values := map[string]string{adapter + "_WS_ENABLED": "false", adapter + "_WS_MODE": Forward}
		get := func(key string) string { return values[key] }
		config, err := Load(get, adapter)
		if err != nil || !config.Disabled || config.Mode != Forward || config.URL != "" {
			t.Fatalf("disabled forward configuration should not require a URL: %#v %v", config, err)
		}
		values[adapter+"_WS_ENABLED"] = "true"
		if _, err := Load(get, adapter); err == nil {
			t.Fatal("enabling forward without a destination was accepted")
		}
		values[adapter+"_WS_URL"] = "ws://synthetic.invalid/"
		config, err = Load(get, adapter)
		if err != nil || config.Disabled {
			t.Fatalf("explicitly enabled configuration rejected: %v", err)
		}
		for _, invalid := range []string{"synthetic-secret", "false\n", "0", "TRUE"} {
			values[adapter+"_WS_ENABLED"] = invalid
			if _, err := Load(get, adapter); err == nil || strings.Contains(err.Error(), invalid) {
				t.Fatalf("invalid flag accepted or echoed: %v", err)
			}
		}
	}
}

func TestLoadConnectionConfigValidationAndSecretRedaction(t *testing.T) {
	for _, tc := range []struct{ name, mode, url, token string }{
		{"mode", "synthetic-secret", "", ""},
		{"empty destination", Forward, "", ""},
		{"http", Forward, "https://bot.example/synthetic-secret", ""},
		{"relative", Forward, "/synthetic-secret", ""},
		{"userinfo", Forward, "ws://synthetic-secret:password@bot.example/", ""},
		{"fragment", Forward, "ws://bot.example/#synthetic-secret", ""},
		{"empty fragment", Forward, "ws://bot.example/#", ""},
		{"missing host", Forward, "ws:///synthetic-secret", ""},
		{"invalid port", Forward, "ws://bot.example:70000/synthetic-secret", ""},
		{"zero port", Forward, "ws://bot.example:0/", ""},
		{"empty port", Forward, "ws://bot.example:/", ""},
		{"space", Forward, "ws://bot.example/synthetic secret", ""},
		{"control before trim", Forward, "\nws://bot.example/synthetic-secret\r", ""},
		{"mode control", "reverse\n", "", ""},
		{"token control", Forward, "ws://bot.example/", "synthetic-secret\n"},
		{"reverse stale url", Reverse, "http://bot.example/synthetic-secret", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{"ONEBOT_WS_MODE": tc.mode, "ONEBOT_WS_URL": tc.url, "ONEBOT_WS_TOKEN": tc.token}
			_, err := Load(func(key string) string { return values[key] }, "ONEBOT")
			if err == nil {
				t.Fatal("invalid connection settings were accepted")
			}
			if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "password") {
				t.Fatalf("validation disclosed a secret: %v", err)
			}
		})
	}
	for _, endpoint := range []string{"ws://127.0.0.1:8081/", "wss://bot.example/", "ws://[::1]:8081/", "ws://bot.example/?route=test"} {
		if err := (Config{Mode: Forward, URL: endpoint}).Validate(); err != nil {
			t.Fatalf("valid URL rejected: %v", err)
		}
	}
	if _, err := Load(nil, "unknown"); err == nil {
		t.Fatal("unregistered adapter accepted")
	}
}
