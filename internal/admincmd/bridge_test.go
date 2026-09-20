package admincmd

import (
	"testing"
)

func TestStripLeadingMention(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"[@10001] /reset", "/reset"},
		{"[@10001]/reset", "/reset"},
		{"[@10001]   execute reset", "execute reset"},
		{"@bot /reset", "/reset"},
		{"@bot\t/reset", "/reset"},
		{"/reset", "/reset"},
		{"hello world", "hello world"},
		{"", ""},
	}

	for _, tt := range tests {
		got := StripLeadingMention(tt.input)
		if got != tt.expected {
			t.Errorf("StripLeadingMention(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}
