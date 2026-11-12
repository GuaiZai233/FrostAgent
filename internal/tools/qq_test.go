package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/core"
	"github.com/GuaiZai233/FrostAgent/internal/instanceconfig"
	"github.com/GuaiZai233/FrostAgent/internal/llm"
	"github.com/GuaiZai233/FrostAgent/internal/qqextend"
	"github.com/GuaiZai233/FrostAgent/internal/runtimescope"
	"path/filepath"
)

func qqTestContext() context.Context {
	return llm.WithRunContext(context.Background(), llm.RunContext{SessionID: "fixture-session", PlatformActionHook: func(context.Context, string) (core.PlatformActionResult, error) {
		panic("tool preparation must not send")
	}})
}

func TestQQDiscoveryAndPreparation(t *testing.T) {
	tool := QQTool()
	if tool.Name() != qqextend.ToolName {
		t.Fatal(tool.Name())
	}
	definition, _ := json.Marshal(tool.Parameters())
	if len(definition) > 1500 {
		t.Fatalf("QQ schema grew to %d bytes", len(definition))
	}
	for _, capability := range []string{"", "send", "forward_bundle", "forward_message", "poke", "reaction", "recall"} {
		args, _ := json.Marshal(map[string]any{"action": "discover", "capability": capability})
		result, err := tool.ExecuteResult(context.Background(), string(args))
		if err != nil || result.PlatformActionPayload != "" || !json.Valid([]byte(result.Content)) {
			t.Fatalf("discovery %q: %+v %v", capability, result, err)
		}
	}
	for _, raw := range []string{
		`{"action":"send","arguments":{"messages":[{"type":"face","data":{"id":"14"}},{"type":"dice"},{"type":"rps"},{"type":"mention_all"}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"contact","data":{"type":"qq","id":"10001"}},{"type":"music","data":{"type":"163","id":"fixture-song"}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"music","data":{"type":"custom","url":"https://example.invalid/song","audio":"https://example.invalid/audio","title":"sample"}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"location","data":{"lat":0,"lon":0}},{"type":"share","data":{"url":"https://example.invalid","title":"sample"}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"image","url":"https://example.invalid/image","summary":"fixture","name":"a.png"},{"type":"video","url":"https://example.invalid/video","thumb":"https://example.invalid/thumb"},{"type":"file","url":"https://example.invalid/file","folder_id":"fixture-folder","name":"a.txt"}]}}`,
		`{"action":"forward_bundle","arguments":{"nodes":[{"message_id":"101"},{"messages":[{"type":"plain","text":"sample"}]}],"source":"sample","news":["preview"]}}`,
		`{"action":"forward_message","arguments":{"message_id":"101"}}`,
		`{"action":"poke","arguments":{}}`,
		`{"action":"reaction","arguments":{"message_id":"101","emoji_id":"14","set":false}}`,
		`{"action":"recall","arguments":{"message_id":"101"}}`,
	} {
		result, err := tool.ExecuteResult(qqTestContext(), raw)
		if err != nil || result.PlatformActionPayload == "" || result.MessagePayload != "" {
			t.Fatalf("prepare %s: %+v %v", raw, result, err)
		}
	}
}

func TestQQRejectsInvalidRequestsAndRawActions(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`{"action":"send_packet","arguments":{}}`,
		`{"action":"send_voice_text","arguments":{}}`,
		`{"action":"send_group_ai_voice","arguments":{}}`,
		`{"action":"send_profile_like","arguments":{}}`,
		`{"action":"send","arguments":{"messages":[]}}`,
		`{"action":"send","arguments":{"session":"elsewhere","messages":[{"type":"dice"}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"dice","data":{"result":6}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"contact","data":{"type":"other","id":"10001"}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"share","data":{"url":"file:///secret","title":"sample"}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"location","data":{"lat":91,"lon":0}}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"image","path":"a","url":"https://example.invalid"}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"file","url":"https://example.invalid","name":"../secret"}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"plain","text":"ok","folder_id":"folder"}]}}`,
		`{"action":"send","arguments":{"messages":[{"type":"mention_specific","message_id":"101"},{"type":"plain","text":"sample"}]}}`,
		`{"action":"forward_bundle","arguments":{"nodes":[{"message_id":"101","messages":[{"type":"dice"}]}]}}`,
		`{"action":"forward_bundle","arguments":{"nodes":[{"messages":[{"type":"file","url":"https://example.invalid"}]}]}}`,
		`{"action":"recall","arguments":{"message_id":"101","user_id":"10001"}}`,
		`{"action":"send","arguments":{"target_group_id":"group:42","messages":[{"type":"dice"}]}}`,
		`{"action":"poke","arguments":{"target_group_id":"42"}}`,
		`{"action":"reaction","arguments":{"target_group_id":"42","message_id":"101","emoji_id":"14"}}`,
		`{"action":"recall","arguments":{"target_group_id":"42","message_id":"101"}}`,
	} {
		result, err := QQTool().ExecuteResult(qqTestContext(), raw)
		if err == nil || result.PlatformActionPayload != "" {
			t.Errorf("accepted invalid request %s: %+v %v", raw, result, err)
		}
	}
}

func TestQQDiscoveryShowsOnlyConfiguredTargets(t *testing.T) {
	config, err := instanceconfig.Open(filepath.Join(t.TempDir(), "instance.env"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Update(qqextend.TargetGroupsKey, "42,43", false); err != nil {
		t.Fatal(err)
	}
	scope := runtimescope.New(config, nil, nil)
	defer scope.Cancel()
	ctx := runtimescope.WithContext(qqTestContext(), scope)
	result, err := QQTool().ExecuteResult(ctx, `{"action":"discover","capability":"send"}`)
	if err != nil || !strings.Contains(result.Content, `"target_groups":["42","43"]`) || !strings.Contains(result.Content, "target_group_id") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, raw := range []string{
		`{"action":"send","arguments":{"target_group_id":"42","messages":[{"type":"plain","text":"fixture"}]}}`,
		`{"action":"forward_message","arguments":{"target_group_id":"42","message_id":"101"}}`,
		`{"action":"forward_bundle","arguments":{"target_group_id":"42","nodes":[{"message_id":"101"}]}}`,
	} {
		result, err := QQTool().ExecuteResult(ctx, raw)
		if err != nil || !strings.Contains(result.PlatformActionPayload, `"target_group_id":"42"`) {
			t.Fatalf("%+v %v", result, err)
		}
	}
}

func TestQQRejectsMissingAdapterProactiveAndMock(t *testing.T) {
	for _, run := range []llm.RunContext{{}, {Proactive: true}, {Mock: true}} {
		if run.Proactive || run.Mock {
			run.PlatformActionHook = func(context.Context, string) (core.PlatformActionResult, error) { panic("unexpected send") }
		}
		for _, tool := range []Tool{QQTool(), SendMsgTool()} {
			args := `{"messages":[{"type":"dice"}]}`
			if tool.Name() == "qq_extend_messages" {
				args = `{"action":"send","arguments":` + args + `}`
			}
			if result, err := tool.ExecuteResult(llm.WithRunContext(context.Background(), run), args); err == nil || result.PlatformActionPayload != "" {
				t.Errorf("gate failed: %s %+v %v", tool.Name(), result, err)
			}
		}
	}
}

func TestQQSendMessageUsesDeferredNativeDelivery(t *testing.T) {
	result, err := SendMsgTool().ExecuteResult(qqTestContext(), `{"messages":[{"type":"plain","text":"hello"},{"type":"face","data":{"id":"14"}}]}`)
	if err != nil || result.PlatformActionPayload == "" || result.MessagePayload != "" {
		t.Fatalf("native preparation: %+v %v", result, err)
	}
	if !strings.Contains(result.PlatformActionPayload, `"action":"send"`) {
		t.Fatal(result.PlatformActionPayload)
	}
}

func TestQQBuildNativeComponents(t *testing.T) {
	lat, lon := 12.0, 34.0
	msgs := []Msg{
		{Type: "face", Data: &QQMessageData{ID: "14"}}, {Type: "dice"}, {Type: "rps"}, {Type: "mention_all"},
		{Type: "contact", Data: &QQMessageData{Type: "group", ID: "30001"}},
		{Type: "music", Data: &QQMessageData{Type: "qq", ID: "fixture-song"}},
		{Type: "location", Data: &QQMessageData{Lat: &lat, Lon: &lon}},
		{Type: "mface", Data: &QQMessageData{EmojiID: "abcdef012345", EmojiPackageID: "42", Key: "fixture-key"}},
		{Type: "image", URL: "https://example.invalid/image", Name: "photo.png", Summary: "sample"},
		{Type: "video", URL: "https://example.invalid/video", Thumb: "https://example.invalid/thumb"},
	}
	segments, err := BuildOneBotMessage(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != len(msgs) || segments[3].Type != "at" || segments[3].Data["qq"] != "all" || segments[8].Data["summary"] != "sample" || segments[9].Data["thumb"] != "https://example.invalid/thumb" {
		t.Fatalf("segments: %+v", segments)
	}
}
