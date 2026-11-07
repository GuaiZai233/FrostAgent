package llm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
)

type switchedTool struct{ calls int }

func (*switchedTool) Name() string                     { return "switch_fixture" }
func (*switchedTool) Description() string              { return "Synthetic test tool" }
func (*switchedTool) Parameters() map[string]any       { return map[string]any{"type": "object"} }
func (t *switchedTool) Execute(string) (string, error) { t.calls++; return "executed", nil }

type staleToolProvider struct {
	t      *testing.T
	tool   *switchedTool
	rounds int
}

type switchableStickerTool struct {
	staticTool
	available bool
}

func (t *switchableStickerTool) IsEnabled() bool { return t.available }

func newStickerSwitchEngine(t *testing.T) *Engine {
	t.Helper()
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(sticker.ActivityEnvKey, "10", false); err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	t.Cleanup(scope.Cancel)
	e := &Engine{Scope: scope, MaxIterations: 2, ToolRegistry: map[string]ToolExecutor{
		"send_sticker": &switchableStickerTool{staticTool: staticTool{name: "send_sticker"}, available: true},
	}}
	for range 4 {
		e.RecordBotTurn()
	}
	return e
}

func assertNoStickerConstraint(t *testing.T, req core.ChatRequest) {
	t.Helper()
	for _, tool := range req.Tools {
		if tool.Name == "send_sticker" {
			t.Error("unavailable sticker tool advertised")
		}
	}
	for _, msg := range req.Messages {
		var texts []string
		switch content := msg.Content.(type) {
		case string:
			texts = append(texts, content)
		case []core.ContentPart:
			for _, part := range content {
				if part.Type == string(core.ContentPartTypeText) {
					texts = append(texts, part.Text)
				}
			}
		}
		for _, text := range texts {
			if strings.Contains(text, "FrostAgent 强制") || text == stickerEnforcementPrompt || text == ignoreEnforceStickerReply {
				t.Error("unavailable sticker tool still constrained the model")
			}
		}
	}
}

func TestUnavailableStickerToolDoesNotEnforceDueQuota(t *testing.T) {
	for _, unavailable := range []string{"disabled", "not registered", "prerequisite unavailable"} {
		for _, toolReply := range []bool{false, true} {
			t.Run(unavailable+fmt.Sprint("/tool reply=", toolReply), func(t *testing.T) {
				e := newStickerSwitchEngine(t)
				switch unavailable {
				case "disabled":
					if err := e.Config.Update(DisabledToolsKey, `["send_sticker"]`, false); err != nil {
						t.Fatal(err)
					}
				case "not registered":
					delete(e.ToolRegistry, "send_sticker")
				case "prerequisite unavailable":
					e.ToolRegistry["send_sticker"].(*switchableStickerTool).available = false
				}
				e.ToolRegistry["send_message"] = &staticTool{name: "send_message", result: `{"messages":[{"type":"plain","text":"ordinary tool reply"}]}`}
				calls, deliveries := 0, 0
				e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
					calls++
					assertNoStickerConstraint(t, req)
					msg := core.ChatMessage{Role: core.RoleAssistant, Content: "ordinary final"}
					if toolReply && calls == 1 {
						msg.ToolCalls = []core.ToolCall{{ID: "fixture-reply", Type: "function", Function: core.ToolCallFunction{Name: "send_message", Arguments: `{}`}}}
					}
					return &core.ChatResponse{Message: msg}, nil
				})
				result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: "hello"}}, RunContext{SendHook: func(string) error { deliveries++; return nil }})
				wantCalls, wantDeliveries := 1, 0
				if toolReply {
					wantCalls, wantDeliveries = 2, 1
				}
				if result.Content != "ordinary final" || calls != wantCalls || deliveries != wantDeliveries {
					t.Fatalf("content=%q calls=%d deliveries=%d", result.Content, calls, deliveries)
				}
				if _, remaining, grace := e.stickerActivitySnapshot(); remaining != 0 || grace != 0 {
					t.Fatalf("unavailable tool changed quota: remaining=%d grace=%d", remaining, grace)
				}
			})
		}
	}
}

func TestStickerEnforcementStopsWhenToolBecomesUnavailable(t *testing.T) {
	image := core.ContentPart{Type: "image_url", ImageURL: &core.ImageURL{URL: "data:image/gif;base64,R0lGODlh"}}
	for _, content := range []struct {
		name  string
		value any
	}{
		{"text", "hello"},
		{"image with text", []core.ContentPart{image, {Type: "text", Text: "hello"}}},
		{"image only", []core.ContentPart{image}},
	} {
		for _, disableDuring := range []string{"model response", "tool execution"} {
			for _, prerequisite := range []bool{false, true} {
				t.Run(content.name+"/"+disableDuring+fmt.Sprint("/prerequisite=", prerequisite), func(t *testing.T) {
					e := newStickerSwitchEngine(t)
					disable := func() {
						if prerequisite {
							e.ToolRegistry["send_sticker"].(*switchableStickerTool).available = false
						} else if err := e.Config.Update(DisabledToolsKey, `["send_sticker"]`, false); err != nil {
							t.Fatal(err)
						}
					}
					e.ToolRegistry["fixture-disable"] = &injectionTool{name: "fixture-disable", execute: func() (string, error) { disable(); return "disabled", nil }}
					calls := 0
					e.Provider = injectionProvider(func(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
						calls++
						msg := core.ChatMessage{Role: core.RoleAssistant, Content: "ordinary final"}
						if calls == 1 {
							sawEnforcement := false
							for _, message := range req.Messages {
								if message.Content == stickerEnforcementPrompt {
									sawEnforcement = true
								}
							}
							if !sawEnforcement {
								t.Error("enabled tool did not enforce due quota")
							}
							if disableDuring == "model response" {
								disable()
							} else {
								msg.ToolCalls = []core.ToolCall{{ID: "fixture-disable", Type: "function", Function: core.ToolCallFunction{Name: "fixture-disable", Arguments: `{}`}}}
							}
						} else {
							assertNoStickerConstraint(t, req)
						}
						return &core.ChatResponse{Message: msg}, nil
					})
					result := e.RunMessagesWithContext([]ChatMessage{{Role: "user", Content: content.value}}, RunContext{})
					wantCalls := 1
					if disableDuring == "tool execution" {
						wantCalls = 2
					}
					if result.Content != "ordinary final" || calls != wantCalls {
						t.Fatalf("content=%q calls=%d", result.Content, calls)
					}
					if _, _, grace := e.stickerActivitySnapshot(); grace != 0 {
						t.Fatal("disabling tool granted automatic forgiveness")
					}
				})
			}
		}
	}
}

func (p *staleToolProvider) Chat(_ context.Context, req core.ChatRequest) (*core.ChatResponse, error) {
	p.rounds++
	if len(req.Tools) != 0 {
		p.t.Fatal("disabled tool advertised to model")
	}
	if p.rounds == 1 {
		return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
			ID: "synthetic-call", Type: "function", Function: core.ToolCallFunction{Name: p.tool.Name(), Arguments: "{}"},
		}}}}, nil
	}
	if p.tool.calls != 0 {
		p.t.Fatal("stale model call executed disabled tool")
	}
	return &core.ChatResponse{Message: core.ChatMessage{Role: core.RoleAssistant, Content: "blocked"}}, nil
}

func TestToolSwitchRejectsStaleCallsAndIsInstanceLocal(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	tool := &switchedTool{}
	provider := &staleToolProvider{t: t, tool: tool}
	e := &Engine{Scope: scope, MaxIterations: 2, Provider: provider, ToolRegistry: map[string]ToolExecutor{tool.Name(): tool}}
	other := &Engine{ToolRegistry: e.ToolRegistry}
	if len(e.ModelTools()) != 1 {
		t.Fatal("missing configuration must preserve enabled default")
	}
	if err := config.Update(DisabledToolsKey, `["switch_fixture"]`, false); err != nil {
		t.Fatal(err)
	}
	if got := e.Run("synthetic prompt"); got != "blocked" {
		t.Fatalf("result = %q", got)
	}
	if len(other.ModelTools()) != 1 {
		t.Fatal("switch leaked to another instance")
	}
	if err := config.Update(DisabledToolsKey, "[]", false); err != nil {
		t.Fatal(err)
	}
	if len(e.ModelTools()) != 1 {
		t.Fatal("re-enabling required runtime restart")
	}
	if err := config.Update(DisabledToolsKey, "malformed", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.findToolExecutor(tool.Name()); ok {
		t.Fatal("malformed switch configuration must fail closed")
	}
}
