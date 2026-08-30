package tools

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
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
	segments, err := BuildOneBotMessage([]Msg{{Type: "file", URL: "https://example.com/a.zip"}})
	if err != nil {
		t.Fatalf("BuildOneBotMessage returned error: %v", err)
	}
	if len(segments) != 1 || segments[0].Type != "file" || segments[0].Data["file"] == "" {
		t.Fatalf("unexpected segments: %+v", segments)
	}
}

func TestBuildOneBotMessageEncodesStickerPathAsBase64(t *testing.T) {
	content := []byte("sticker image")
	path := filepath.Join(t.TempDir(), "sticker.png")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write sticker fixture: %v", err)
	}

	segments, err := BuildOneBotMessage([]Msg{{
		Type:      "image",
		Path:      path,
		URL:       "/api/sticker/example/image",
		IsSticker: true,
	}})
	if err != nil {
		t.Fatalf("BuildOneBotMessage returned error: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("unexpected segments: %+v", segments)
	}
	wantFile := "base64://" + base64.StdEncoding.EncodeToString(content)
	if got := segments[0].Data["file"]; got != wantFile {
		t.Fatalf("sticker file = %q, want %q", got, wantFile)
	}
	if got := segments[0].Data["sub_type"]; got != 1 {
		t.Fatalf("sticker sub_type = %v, want 1", got)
	}
	if got := segments[0].Data["subType"]; got != 1 {
		t.Fatalf("sticker subType = %v, want 1", got)
	}
}

func TestBuildOneBotMessageReturnsStickerReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.png")
	segments, err := BuildOneBotMessage([]Msg{{Type: "image", Path: path, IsSticker: true}})
	if err == nil {
		t.Fatalf("BuildOneBotMessage returned no error for missing sticker: %+v", segments)
	}
	if len(segments) != 0 {
		t.Fatalf("BuildOneBotMessage returned partial segments: %+v", segments)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q does not identify missing sticker path %q", err, path)
	}
}

func TestBuildOneBotMessageKeepsRegularImagePath(t *testing.T) {
	path := filepath.Join("data", "images", "regular.png")
	segments, err := BuildOneBotMessage([]Msg{{Type: "image", Path: path}})
	if err != nil {
		t.Fatalf("BuildOneBotMessage returned error: %v", err)
	}
	if len(segments) != 1 || segments[0].Data["file"] != "file://"+path {
		t.Fatalf("unexpected segments: %+v", segments)
	}
	if _, ok := segments[0].Data["sub_type"]; ok {
		t.Fatalf("regular image unexpectedly has sticker subtype: %+v", segments[0])
	}
	if _, ok := segments[0].Data["subType"]; ok {
		t.Fatalf("regular image unexpectedly has LLBot sticker subtype: %+v", segments[0])
	}
}
