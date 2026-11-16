package qqextend

import (
	"reflect"
	"testing"
)

func TestTargetGroupsFailClosed(t *testing.T) {
	groups, err := ParseTargetGroups("42, 43;42\n44")
	if err != nil || !reflect.DeepEqual(groups, []string{"42", "43", "44"}) {
		t.Fatalf("groups=%v err=%v", groups, err)
	}
	for _, raw := range []string{"", "42x", "42,bad", "042", "0", "-1", "+42", "9223372036854775808", "group:42"} {
		if TargetAllowed(raw, "42") {
			t.Fatalf("invalid or empty configuration allowed a target: %q", raw)
		}
	}
	if !TargetAllowed("42,43", "42") || TargetAllowed("42,43", "4") {
		t.Fatal("target matching is not exact")
	}
}
