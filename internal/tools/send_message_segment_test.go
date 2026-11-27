package tools

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"testing"
)

func TestSendMessageFiltersSegmentMarks(t *testing.T) {
	for _, tc := range []struct{ name, enabled, limit string }{
		{"switch on", "true", "0"},
		{"switch off", "false", "0"},
		{"single segment limit", "true", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(replyformat.EnvKey, tc.enabled)
			t.Setenv(replyformat.LimitEnvKey, tc.limit)
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
			if payload.Messages[0].Text != "ab\n\n``" {
				t.Fatal(payload.Messages)
			}
			empty, _ := json.Marshal(map[string]any{"messages": []Msg{{Type: "plain", Text: replyformat.SegmentMark}}})
			if _, err := SendMsgTool().Execute(string(empty)); err == nil {
				t.Fatal("marker-only tool text must not send an empty message")
			}
		})
	}
}

func TestSendMessageForcefullyAvoidSeparation(t *testing.T) {
	input := "a" + replyformat.SegmentMark + "b"
	for _, tc := range []struct {
		name  string
		flag  any
		want  string
		keeps bool
	}{
		{"omitted", nil, "ab", false},
		{"false", false, "ab", false},
		{"true", true, input, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"messages": []Msg{{Type: "plain", Text: input}}}
			if tc.flag != nil {
				args["forcefully_avoid_separation"] = tc.flag
			}
			raw, _ := json.Marshal(args)
			result, err := SendMsgTool().Execute(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Messages []Msg `json:"messages"`
			}
			if err := json.Unmarshal([]byte(result), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Messages[0].Text != tc.want {
				t.Fatalf("got %q; want %q", payload.Messages[0].Text, tc.want)
			}
		})
	}
	marker, _ := json.Marshal(map[string]any{"forcefully_avoid_separation": true, "messages": []Msg{{Type: "plain", Text: replyformat.SegmentMark}}})
	if _, err := SendMsgTool().Execute(string(marker)); err != nil {
		t.Fatalf("a literal marker is ordinary text when separation is avoided: %v", err)
	}
}
