package replyformat

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		enabled     bool
		want        []string
	}{
		{"unchanged disabled", "  a\n\nb\n ", false, []string{"  a\n\nb\n "}},
		{"disabled marker", "a" + SegmentMark + "b", false, []string{"ab"}},
		{"disabled inline", "a`" + SegmentMark + "`b" + SegmentMark, false, []string{"a`" + SegmentMark + "`b"}},
		{"enabled marker", "一" + SegmentMark + "二", true, []string{"一", "二"}},
		{"blank lines", "a\n\nb\r\n\r\nc\nd", true, []string{"a", "b", "c\nd"}},
		{"mixed", "a\n\nb" + SegmentMark + "c", true, []string{"a", "b", "c"}},
		{"single newline", "  a\nb  ", true, []string{"  a\nb  "}},
		{"empty segments", SegmentMark + "a" + SegmentMark + SegmentMark + "\n\n", true, []string{"a"}},
		{"only markers on", SegmentMark + SegmentMark, true, nil},
		{"only markers off", SegmentMark + SegmentMark, false, nil},
		{"inline", "a`" + SegmentMark + "`" + SegmentMark + "b", true, []string{"a`" + SegmentMark + "`", "b"}},
		{"double ticks", "``" + SegmentMark + "``\n\nb", true, []string{"``" + SegmentMark + "``", "b"}},
		{"fenced", "```text\na\n\n" + SegmentMark + "\n```\n\nb", true, []string{"```text\na\n\n" + SegmentMark + "\n```", "b"}},
		{"unmatched tick", "a`" + SegmentMark + "b", false, []string{"a`b"}},
		{"literal backslash n", "a\\n\\nb", true, []string{"a\\n\\nb"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Split(tc.input, tc.enabled); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}
func TestReceiptIsNotSegmented(t *testing.T) {
	parts := Split("a"+SegmentMark+"b", true)
	got := WithReceipt(parts, "receipt\n\nmore")
	if !reflect.DeepEqual(got, []string{"a", "b\n\nreceipt\n\nmore"}) || parts[1] != "b" {
		t.Fatalf("receipt mutated/split: %q / %q", got, parts)
	}
	if got := WithReceipt(nil, "receipt"); !reflect.DeepEqual(got, []string{"receipt"}) {
		t.Fatal(got)
	}
}
func FuzzSplit(f *testing.F) {
	for _, value := range []string{"", SegmentMark, "`" + SegmentMark + "`", "a\n\nb"} {
		f.Add(value, true)
		f.Add(value, false)
	}
	f.Fuzz(func(t *testing.T, text string, enabled bool) {
		parts := Split(text, enabled)
		for _, part := range parts {
			if strings.TrimSpace(part) == "" {
				t.Fatal("empty output")
			}
		}
		if !enabled && len(parts) > 1 {
			t.Fatal("split disabled output")
		}
	})
}
