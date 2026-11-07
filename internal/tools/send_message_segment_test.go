package tools

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"testing"
)

func TestSendMessageFiltersDisabledSegmentMarks(t *testing.T) {
	t.Setenv(replyformat.EnvKey, "false")
	input := "a" + replyformat.SegmentMark + "b\n\n`" + replyformat.SegmentMark + "`"
	args, _ := json.Marshal(map[string]any{"messages": []Msg{{Type: "plain", Text: input}}})
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
	if payload.Messages[0].Text != "ab\n\n`"+replyformat.SegmentMark+"`" {
		t.Fatal(payload.Messages)
	}
	empty, _ := json.Marshal(map[string]any{"messages": []Msg{{Type: "plain", Text: replyformat.SegmentMark}}})
	if _, err := SendMsgTool().Execute(string(empty)); err == nil {
		t.Fatal("marker-only tool text must not send an empty message")
	}
}
