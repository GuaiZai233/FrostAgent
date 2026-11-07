package onebot

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
)

func TestFilterStructuredSegmentMarks(t *testing.T) {
	protected := "`" + replyformat.SegmentMark + "`"
	input := []tools.Msg{
		{Type: "quote", MessageID: "synthetic-message"},
		{Type: "mention_user", MentionUserID: "synthetic-user"},
		{Type: "plain", Text: "first" + replyformat.SegmentMark + "second\n\n" + protected},
		{Type: "plain", Text: replyformat.SegmentMark},
		{Type: "image", Path: "synthetic/" + replyformat.SegmentMark + ".png", IsSticker: true},
	}
	marshal := func(messages []tools.Msg) string {
		t.Helper()
		data, err := json.Marshal(map[string]any{"messages": messages})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	got, err := parseFinalMessages(filterStructuredSegmentMarks(marshal(input)))
	if err != nil {
		t.Fatal(err)
	}
	want := []tools.Msg{input[0], input[1], {Type: "plain", Text: "firstsecond\n\n" + protected}, input[4]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
	if got := filterStructuredSegmentMarks(marshal([]tools.Msg{{Type: "plain", Text: replyformat.SegmentMark}})); got != "" {
		t.Fatalf("marker-only final must become empty: %q", got)
	}
	for _, input := range []string{"ordinary " + replyformat.SegmentMark, "{invalid", marshal([]tools.Msg{{Type: "plain", Text: protected}})} {
		if got := filterStructuredSegmentMarks(input); got != input {
			t.Fatalf("unexpected rewrite: %q -> %q", input, got)
		}
	}
}
