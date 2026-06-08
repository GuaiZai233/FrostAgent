package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

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
