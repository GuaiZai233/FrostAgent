package wsclient

import (
	"strings"
	"testing"
)

func TestLoadConnectionConfigDefaultsAndIsolation(t *testing.T) {
	values := map[string]string{
		"ONEBOT_WS_MODE": "forward", "ONEBOT_WS_URL": "wss://bot.example/ws?route=synthetic",
		"ONEBOT_WS_TOKEN": "synthetic-client-token",
	}
	getenv := func(key string) string { return values[key] }
	onebot, err := Load(getenv, "ONEBOT")
	if err != nil || onebot.Mode != Forward || onebot.URL != values["ONEBOT_WS_URL"] || onebot.Token != values["ONEBOT_WS_TOKEN"] {
		t.Fatalf("forward config did not round trip: %v", err)
	}
	astrbot, err := Load(getenv, "ASTRBOT")
	if err != nil || astrbot.Mode != Reverse || astrbot.URL != "" || astrbot.Token != "" {
		t.Fatalf("adapter config leaked: %#v %v", astrbot, err)
	}
	defaults, err := Load(nil, "ONEBOT")
	if err != nil || defaults.Mode != Reverse {
		t.Fatalf("nil getter defaults: %#v %v", defaults, err)
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
