package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/sticker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSendMsgToolRejectsStickerFlagWhenDisabled(t *testing.T) {
	t.Setenv(sticker.ActivityEnvKey, "0")
	result, err := SendMsgTool().Execute(`{"messages":[{"type":"image","url":"https://example.com/sticker.png","is_sticker":true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":"表情包发送功能已被禁用，请管理员在 FrostAgent 后端启用此功能。"}`
	if result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
}

func TestSendMsgToolAdvertisesNativeMentionContract(t *testing.T) {
	tool := SendMsgTool()
	for _, keyword := range []string{
		"mention_user",
		"plain",
		"user ID",
		"[@ID]",
		"[CQ:at,qq=ID]",
		nativeMentionExample,
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

func TestSendMsgToolKeepsBasicSchemaAndCurrentSession(t *testing.T) {
	tool := SendMsgTool()
	properties := tool.Parameters()["properties"].(map[string]any)
	if _, exists := properties["session"]; exists {
		t.Fatal("basic schema must not advertise a session override")
	}
	items := properties["messages"].(map[string]any)["items"].(map[string]any)
	messageProperties := items["properties"].(map[string]any)
	wantTypes := []string{"plain", "image", "record", "video", "file", "mention_user", "quote", "mention_specific"}
	if got := messageProperties["type"].(map[string]any)["enum"].([]string); !slices.Equal(got, wantTypes) {
		t.Fatalf("basic types = %v", got)
	}
	for _, field := range []string{"name", "folder_id", "thumb", "summary", "transfer", "data", "sticker_index", "target_group_id"} {
		if _, exists := messageProperties[field]; exists {
			t.Fatalf("QQ field %q leaked into the basic schema", field)
		}
	}
	for _, args := range []string{
		`{"session":"group:42","messages":[{"type":"plain","text":"fixture"}]}`,
		`{"target_group_id":"42","messages":[{"type":"plain","text":"fixture"}]}`,
	} {
		result, err := tool.ExecuteResult(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "qq_extend_messages") || result.MessagePayload != "" || result.PlatformActionPayload != "" {
			t.Fatalf("target override accepted: %+v %v", result, err)
		}
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
		`{"messages":[{"type":"mention_user"}]}`,
		`{"messages":[{"type":"mention_user","mention_user_id":"  "}]}`,
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
	path := filepath.Join(t.TempDir(), "regular.png")
	if err := os.WriteFile(path, []byte("regular image payload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
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

func TestBuildOneBotMessage_PreflightFailFastForAllMediaTypes(t *testing.T) {
	mediaTypes := []string{"image", "record", "video", "file"}

	// 1. Missing file fails fast for all media types
	for _, mt := range mediaTypes {
		missingPath := filepath.Join(t.TempDir(), "nonexistent_"+mt+".dat")
		segments, err := BuildOneBotMessage([]Msg{{Type: mt, Path: missingPath}})
		if err == nil {
			t.Fatalf("expected preflight error for missing %s, got segments: %+v", mt, segments)
		}
		if !strings.Contains(err.Error(), missingPath) {
			t.Fatalf("error %q should mention missing path %q", err, missingPath)
		}
	}

	// 2. Empty file fails fast for all media types
	for _, mt := range mediaTypes {
		emptyPath := filepath.Join(t.TempDir(), "empty_"+mt+".dat")
		if err := os.WriteFile(emptyPath, []byte{}, 0o600); err != nil {
			t.Fatalf("write empty fixture: %v", err)
		}
		segments, err := BuildOneBotMessage([]Msg{{Type: mt, Path: emptyPath}})
		if err == nil {
			t.Fatalf("expected preflight error for empty %s, got segments: %+v", mt, segments)
		}
		if !strings.Contains(err.Error(), "为空") {
			t.Fatalf("error %q should mention '为空' for empty path %q", err, emptyPath)
		}
	}

	// 3. Valid local file succeeds for all media types
	for _, mt := range mediaTypes {
		validPath := filepath.Join(t.TempDir(), "valid_"+mt+".dat")
		if err := os.WriteFile(validPath, []byte("valid content"), 0o600); err != nil {
			t.Fatalf("write valid fixture: %v", err)
		}
		segments, err := BuildOneBotMessage([]Msg{{Type: mt, Path: validPath}})
		if err != nil {
			t.Fatalf("expected success for valid %s, got err: %v", mt, err)
		}
		if len(segments) != 1 || segments[0].Type != mt {
			t.Fatalf("unexpected segments for %s: %+v", mt, segments)
		}
	}
}

func TestBuildOneBotMessage_SparseLargeFileDoesNotExhaustMemory(t *testing.T) {
	// Create a 1GB sparse file to verify preflight validation uses O(1) memory and does not read full file
	path := filepath.Join(t.TempDir(), "sparse_video.mp4")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sparse file: %v", err)
	}
	const oneGB = int64(1024 * 1024 * 1024)
	if err := f.Truncate(oneGB); err != nil {
		f.Close()
		t.Fatalf("truncate sparse file to 1GB: %v", err)
	}
	if _, err := f.WriteAt([]byte{0x01}, 0); err != nil {
		f.Close()
		t.Fatalf("write probe byte: %v", err)
	}
	f.Close()

	segments, err := BuildOneBotMessage([]Msg{{Type: "video", Path: path}})
	if err != nil {
		t.Fatalf("expected success for 1GB sparse file, got err: %v", err)
	}
	if len(segments) != 1 || segments[0].Data["file"] != "file://"+path {
		t.Fatalf("unexpected segments for 1GB sparse file: %+v", segments)
	}
}

func TestSendMsgToolMentionExampleReachesNativeOneBotChain(t *testing.T) {
	result, err := SendMsgTool().ExecuteResult(context.Background(), nativeMentionExample)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(result.MessagePayload), &payload); err != nil {
		t.Fatalf("missing delivery payload: %v", err)
	}
	chain, err := BuildOneBotMessage(payload.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 || chain[0].Type != "at" || chain[0].Data["qq"] != "10001" ||
		chain[1].Type != "text" || chain[1].Data["text"] != " 你好" {
		t.Fatalf("documented example must yield a native mention followed by text: %+v", chain)
	}
}

func TestSendMsgToolKeepsMentionSyntaxLiteral(t *testing.T) {
	const text = "语法示例：[@10001] @Example [CQ:at,qq=10001]"
	args, err := json.Marshal(map[string]any{"messages": []Msg{{Type: "plain", Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := SendMsgTool().Execute(string(args))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Messages []Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatal(err)
	}
	chain, err := BuildOneBotMessage(payload.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0].Type != "text" || chain[0].Data["text"] != text {
		t.Fatalf("literal syntax must stay text: %+v", chain)
	}
}
