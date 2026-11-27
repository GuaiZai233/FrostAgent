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
		{"disabled backticks", "a`" + SegmentMark + "`b" + SegmentMark, false, []string{"a``b"}},
		{"enabled marker", "一" + SegmentMark + "二", true, []string{"一", "二"}},
		{"blank lines", "a\n\nb\r\n\r\nc\nd", true, []string{"a", "b", "c\nd"}},
		{"mixed", "a\n\nb" + SegmentMark + "c", true, []string{"a", "b", "c"}},
		{"single newline", "  a\nb  ", true, []string{"  a\nb  "}},
		{"empty segments", SegmentMark + "a" + SegmentMark + SegmentMark + "\n\n", true, []string{"a"}},
		{"only markers on", SegmentMark + SegmentMark, true, nil},
		{"only markers off", SegmentMark + SegmentMark, false, nil},
		{"backticks do not protect markers", "a`" + SegmentMark + "`b", true, []string{"a`", "`b"}},
		{"fence does not protect markers or blank lines", "```text\na\n\n" + SegmentMark + "\nb\n```", true, []string{"```text\na", "b\n```"}},
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
func TestParseLimit(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{{"", 0}, {"0", 0}, {"1", 1}, {"-1", 0}, {"abc", 0}, {"2", 2}, {" 3 ", 3}, {"21", 0}} {
		if got := ParseLimit(tc.value); got != tc.want {
			t.Fatalf("ParseLimit(%q)=%d want %d", tc.value, got, tc.want)
		}
	}
}

func TestEnabled(t *testing.T) {
	for _, tc := range []struct {
		enabled, limit string
		want           bool
	}{
		{"true", "", true}, {"true", "0", true}, {"true", "1", false},
		{"true", "2", true}, {"true", "20", true}, {"true", "invalid", true},
		{"false", "0", false}, {"false", "3", false}, {"", "3", false},
	} {
		values := map[string]string{EnvKey: tc.enabled, LimitEnvKey: tc.limit}
		if got := Enabled(func(key string) string { return values[key] }); got != tc.want {
			t.Fatalf("enabled=%q limit=%q: got %v want %v", tc.enabled, tc.limit, got, tc.want)
		}
	}
}

func TestSingleSegmentLimitDisablesSplitting(t *testing.T) {
	text := "first" + SegmentMark + "second\n\nthird"
	if got := SplitLimited(text, true, 1); !reflect.DeepEqual(got, []string{"firstsecond\n\nthird"}) {
		t.Fatalf("limit one must preserve all text and paragraphs without splitting: %q", got)
	}
}

func TestSplitLimitedKeepsRandomSubsetInOrder(t *testing.T) {
	text := "a" + SegmentMark + "b" + SegmentMark + "c" + SegmentMark + "d"
	if got := SplitLimited(text, true, 0); len(got) != 4 {
		t.Fatalf("no cap must keep all segments: %q", got)
	}
	if got := SplitLimited(text, true, 9); len(got) != 4 {
		t.Fatalf("cap above count must keep all segments: %q", got)
	}
	if got := SplitLimited(text, false, 2); !reflect.DeepEqual(got, []string{"abcd"}) {
		t.Fatalf("disabled segmentation must ignore the cap: %q", got)
	}
	seen := map[string]bool{}
	for range 200 {
		got := SplitLimited(text, true, 2)
		if len(got) != 2 {
			t.Fatalf("cap 2 kept %q", got)
		}
		// Segments are single letters a<b<c<d, so kept order must be ascending.
		if got[0] >= got[1] {
			t.Fatalf("kept segments out of order: %q", got)
		}
		seen[strings.Join(got, "")] = true
	}
	if len(seen) < 2 {
		t.Fatal("selection never varied across runs")
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
