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

func TestSplitFencedCodeBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, code string }{
		{"backticks in code line", "```go\nfmt.Println(\"```\")\n\n" + SegmentMark + "\n```"},
		{"longer closing fence", "```text\na\n\n" + SegmentMark + "\n`````"},
		{"shorter inner fence", "````text\n```\n\n" + SegmentMark + "\n````"},
		{"closing fence with text", "```text\n``` not a close\n\n" + SegmentMark + "\n```"},
		{"indented closing fence", "```text\na\n\n" + SegmentMark + "\n   ```` \t"},
		{"four-space inner delimiter", "```text\n    ```\n\n" + SegmentMark + "\n```"},
		{"crlf fence", "```text\r\na\r\n\r\n" + SegmentMark + "\r\n````"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := tc.code + "\n\nafter" + SegmentMark + "tail"
			if got := Split(input, true); !reflect.DeepEqual(got, []string{strings.TrimSpace(tc.code), "after", "tail"}) {
				t.Fatalf("split code fence: %q", got)
			}
			if got := Split(input, false); !reflect.DeepEqual(got, []string{tc.code + "\n\naftertail"}) {
				t.Fatalf("filtered protected fence content: %q", got)
			}
		})
	}
	// Inline spans cannot consume a fence delimiter, and a fence extends to EOF
	// when the model omits its closing line.
	input := "before ` unmatched\n\n```text\na\n\n" + SegmentMark
	if got := Split(input, true); !reflect.DeepEqual(got, []string{"before ` unmatched", "```text\na\n\n" + SegmentMark}) {
		t.Fatalf("unclosed fence lost: %q", got)
	}
	if got := Split("```"+SegmentMark+"```", false); !reflect.DeepEqual(got, []string{"```" + SegmentMark + "```"}) {
		t.Fatalf("inline triple backticks misread as a fence: %q", got)
	}
	if got := Split("```text\r\na\r\n```\r\n\r\nafter", true); !reflect.DeepEqual(got, []string{"```text\r\na\r\n```", "after"}) {
		t.Fatalf("CRLF separator after fence lost: %q", got)
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
