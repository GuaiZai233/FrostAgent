package sticker

import (
	"encoding/json"
	"testing"
)

func TestIsImageSubType(t *testing.T) {
	for _, value := range []any{1, int64(1), float64(1), "1", " 1 ", json.Number("1")} {
		if !IsImageSubType(value) {
			t.Errorf("sticker subtype not recognized: %T(%v)", value, value)
		}
	}
	for _, value := range []any{nil, false, true, 0, int64(0), float64(0), 1.5, "0", "01", "image", json.Number("0")} {
		if IsImageSubType(value) {
			t.Errorf("ordinary subtype classified as sticker: %T(%v)", value, value)
		}
	}
}

func TestIsMessage(t *testing.T) {
	for _, tc := range []struct {
		name, msgType string
		flag          bool
		subTypes      []any
		want          bool
	}{
		{name: "mface", msgType: "mface", want: true},
		{name: "sticker", msgType: "sticker", want: true},
		{name: "normalized type", msgType: " STICKER ", want: true},
		{name: "image flag", msgType: "image", flag: true, want: true},
		{name: "non-image flag", msgType: "plain", flag: true, want: true},
		{name: "first subtype", msgType: "image", subTypes: []any{1, 0}, want: true},
		{name: "second subtype", msgType: "image", subTypes: []any{0, "1"}, want: true},
		{name: "no marker", msgType: "image"},
		{name: "ordinary subtypes", msgType: "image", subTypes: []any{0, "0", nil}},
		{name: "non-image subtype", msgType: "plain", subTypes: []any{1}},
		{name: "standard emoji", msgType: "face"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsMessage(tc.msgType, tc.flag, tc.subTypes...); got != tc.want {
				t.Fatalf("IsMessage(%q, %t, %v) = %t, want %t", tc.msgType, tc.flag, tc.subTypes, got, tc.want)
			}
		})
	}
}
