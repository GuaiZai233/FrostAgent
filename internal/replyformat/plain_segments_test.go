package replyformat

import (
	"reflect"
	"testing"
)

type testComponent struct{ Type, Text string }

func kindOf(c testComponent) string { return c.Type }

func TestGroupPlainSegmentsSplitsEachPlain(t *testing.T) {
	items := []testComponent{{"plain", "a"}, {"plain", "b"}, {"plain", "c"}}
	got := GroupPlainSegments(items, kindOf)
	want := [][]testComponent{{{"plain", "a"}}, {{"plain", "b"}}, {{"plain", "c"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGroupPlainSegmentsKeepsQuoteWithFirstSegment(t *testing.T) {
	items := []testComponent{{"mention_specific", "42"}, {"plain", "first"}, {"plain", "second"}}
	got := GroupPlainSegments(items, kindOf)
	want := [][]testComponent{{{"mention_specific", "42"}, {"plain", "first"}}, {{"plain", "second"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestGroupPlainSegmentsSingleSegmentUnchanged(t *testing.T) {
	items := []testComponent{{"mention_user", "1"}, {"plain", "hi"}}
	if got := GroupPlainSegments(items, kindOf); len(got) != 1 {
		t.Fatalf("expected one segment, got %v", got)
	}
}

func TestCheckPlainLimit(t *testing.T) {
	if err := CheckPlainLimit(3, 0); err != nil {
		t.Fatalf("zero limit must mean no cap: %v", err)
	}
	if err := CheckPlainLimit(2, 2); err != nil {
		t.Fatalf("at the cap must pass: %v", err)
	}
	if err := CheckPlainLimit(3, 2); err == nil {
		t.Fatal("over the cap must be rejected")
	}
}
