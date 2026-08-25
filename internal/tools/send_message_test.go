package tools

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestSendMsgToolAdvertisesNativeMentionContract(t *testing.T) {
	tool := SendMsgTool()
	for _, keyword := range []string{
		"mention_user",
		"plain",
		"user ID",
	} {
		if !strings.Contains(tool.Description(), keyword) {
			t.Fatalf("send_message description is missing mention guidance keyword %q: %s", keyword, tool.Description())
		}
	}

	properties := tool.Parameters()["properties"].(map[string]any)
	messages := properties["messages"].(map[string]any)
	items := messages["items"].(map[string]any)
	messageProperties := items["properties"].(map[string]any)
	typeSchema := messageProperties["type"].(map[string]any)
	allowedTypes := typeSchema["enum"].([]string)
	if !slices.Contains(allowedTypes, "mention_user") {
		t.Fatalf("send_message type enum does not include mention_user: %v", allowedTypes)
	}
	mentionUserIDSchema := messageProperties["mention_user_id"].(map[string]any)
	if mentionUserIDSchema["type"] != "string" {
		t.Fatalf("mention_user_id must be declared as a string: %v", mentionUserIDSchema)
	}
}

func TestSendMsgToolExecuteNormalizesValidPayload(t *testing.T) {
	tool := SendMsgTool()
	got, err := tool.Execute(`{"messages":[{"type":"plain","text":"hello"},{"type":"image","url":"https://example.com/a.png"}]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	var payload struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("Execute returned invalid JSON: %v", err)
	}
	if len(payload.Messages) != 2 || payload.Messages[0].Text != "hello" || payload.Messages[1].URL == "" {
		t.Fatalf("unexpected normalized payload: %+v", payload)
	}
}

func TestSendMsgToolExecuteRejectsInvalidPayload(t *testing.T) {
	tool := SendMsgTool()
	cases := []string{
		`not-json`,
		`{"messages":[]}`,
		`{"messages":[{"type":"plain"}]}`,
		`{"messages":[{"type":"image"}]}`,
		`{"messages":[{"type":"unknown"}]}`,
	}
	for _, tc := range cases {
		if got, err := tool.Execute(tc); err == nil || strings.TrimSpace(got) != "" {
			t.Fatalf("Execute(%s) = (%q, %v), want error and empty result", tc, got, err)
		}
	}
}

func TestBuildOneBotMessageSupportsFile(t *testing.T) {
	segments := BuildOneBotMessage([]Msg{{Type: "file", URL: "https://example.com/a.zip"}})
	if len(segments) != 1 || segments[0].Type != "file" || segments[0].Data["file"] == "" {
		t.Fatalf("unexpected segments: %+v", segments)
	}
}
