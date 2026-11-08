package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func fixedFrostInfoSource() FrostAgentInfoSource {
	started := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return FrostAgentInfoSource{
		BotName:   func() string { return "霜降" },
		Aliases:   func() []string { return []string{" fx ", "", "小狐狸"} },
		Version:   func() string { return "1.2.3" },
		StartedAt: func() time.Time { return started },
		Now:       func() time.Time { return started.Add(2*24*time.Hour + 3*time.Hour + 4*time.Minute + 30*time.Second) },
		Platforms: func() []string { return []string{"onebot"} },
		DialogueModel: func(context.Context) (string, error) {
			return "upstream-chat-model", nil
		},
		Features: func() map[string]bool {
			return map[string]bool{FeatureMemory: true, FeatureMCP: true}
		},
	}
}

func TestFrostAgentInfoSchemaIsStrictAndReadOnly(t *testing.T) {
	tool := FrostAgentInfoTool(fixedFrostInfoSource())
	if tool.Name() != FrostAgentInfoToolName || !tool.IsEnabled() {
		t.Fatalf("tool = %q enabled=%v", tool.Name(), tool.IsEnabled())
	}
	if tool.IsProactiveAllowed() {
		t.Fatal("read-only status tool must not be allowed during proactive turns")
	}
	schema, err := json.Marshal(tool.Parameters())
	if err != nil {
		t.Fatal(err)
	}
	text := string(schema)
	for _, want := range []string{`"required":["section"]`, `"additionalProperties":false`, `"enum":["overview","model","features"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("schema missing %s: %s", want, text)
		}
	}
}

func TestFrostAgentInfoRejectsInvalidArguments(t *testing.T) {
	tool := FrostAgentInfoTool(fixedFrostInfoSource())
	for _, args := range []string{
		``,
		`null`,
		`[]`,
		`{}`,
		`{"section":"other"}`,
		`{"section":""}`,
		`{"section":"model","extra":true}`,
		`{"section":"model"} {"section":"features"}`,
		`{"section":123}`,
		`invalid`,
	} {
		if _, err := tool.Execute(args); err == nil {
			t.Fatalf("Execute(%q) accepted invalid arguments", args)
		}
	}
}

func TestFrostAgentInfoOverviewReportsRuntimeFacts(t *testing.T) {
	tool := FrostAgentInfoTool(fixedFrostInfoSource())
	raw, err := tool.Execute(`{"section":"overview"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		BotName       string   `json:"bot_name"`
		Aliases       []string `json:"aliases"`
		Version       string   `json:"version"`
		Uptime        string   `json:"uptime"`
		UptimeSeconds int64    `json:"uptime_seconds"`
		Platforms     []string `json:"platforms"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	if got.BotName != "霜降" || got.Version != "1.2.3" {
		t.Fatalf("identity = %+v", got)
	}
	if len(got.Aliases) != 2 || got.Aliases[0] != "fx" || got.Aliases[1] != "小狐狸" {
		t.Fatalf("aliases not trimmed and filtered: %v", got.Aliases)
	}
	if got.Uptime != "2d 3h 4m" || got.UptimeSeconds != 2*24*3600+3*3600+4*60+30 {
		t.Fatalf("uptime = %q / %d", got.Uptime, got.UptimeSeconds)
	}
	if len(got.Platforms) != 1 || got.Platforms[0] != "onebot" {
		t.Fatalf("platforms = %v", got.Platforms)
	}
}

func TestFrostAgentInfoOverviewDefaultsWithoutSources(t *testing.T) {
	tool := FrostAgentInfoTool(FrostAgentInfoSource{})
	raw, err := tool.Execute(`{"section":"overview"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"bot_name":"助手"`, `"aliases":[]`, `"version":"unknown"`, `"uptime":"未知"`, `"platforms":[]`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("defaults missing %s in %s", want, raw)
		}
	}
}

func TestFrostAgentInfoModelReturnsOnlyUpstreamName(t *testing.T) {
	tool := FrostAgentInfoTool(fixedFrostInfoSource())
	raw, err := tool.Execute(`{"section":"model"}`)
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"available":true,"upstream_model":"upstream-chat-model"}` {
		t.Fatalf("model = %s", raw)
	}
}

func TestFrostAgentInfoModelDoesNotEchoResolverErrors(t *testing.T) {
	secret := "https://internal.example/v1?key=synthetic-secret"
	source := FrostAgentInfoSource{DialogueModel: func(context.Context) (string, error) {
		return "", errors.New(secret)
	}}
	raw, err := FrostAgentInfoTool(source).Execute(`{"section":"model"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "synthetic-secret") || strings.Contains(raw, "internal.example") {
		t.Fatalf("resolver error leaked into tool output: %s", raw)
	}
	if !strings.Contains(raw, `"available":false`) {
		t.Fatalf("model should be reported unavailable: %s", raw)
	}
}

func TestFrostAgentInfoFeaturesUseFixedOrderAndDefaultFalse(t *testing.T) {
	tool := FrostAgentInfoTool(fixedFrostInfoSource())
	raw, err := tool.Execute(`{"section":"features"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Features []struct {
			Name      string `json:"name"`
			Available bool   `json:"available"`
		} `json:"features"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	want := []string{FeatureMemory, FeatureVectorRetrieval, FeatureSandbox, FeatureMCP, FeatureActionsCat, FeatureSticker}
	if len(got.Features) != len(want) {
		t.Fatalf("features = %+v", got.Features)
	}
	for i, name := range want {
		if got.Features[i].Name != name {
			t.Fatalf("feature %d = %q, want %q", i, got.Features[i].Name, name)
		}
	}
	if !got.Features[0].Available || got.Features[1].Available || !got.Features[3].Available {
		t.Fatalf("availability not mapped from source: %+v", got.Features)
	}
}

func TestFrostAgentInfoReadsLiveValuesOnEachCall(t *testing.T) {
	name := "first"
	source := FrostAgentInfoSource{BotName: func() string { return name }}
	tool := FrostAgentInfoTool(source)
	first, _ := tool.Execute(`{"section":"overview"}`)
	name = "second"
	second, _ := tool.Execute(`{"section":"overview"}`)
	if !strings.Contains(first, `"first"`) || !strings.Contains(second, `"second"`) {
		t.Fatalf("values were cached: %s / %s", first, second)
	}
}
