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

func TestCleanTargetID(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		platform  string
		want      string
		expectErr bool
	}{
		{
			name:     "bare numeric ID",
			raw:      "10002",
			platform: "qq",
			want:     "10002",
		},
		{
			name:     "bare alphanumeric mock ID",
			raw:      "user-99",
			platform: "qq",
			want:     "user-99",
		},
		{
			name:     "bare admin mock ID with underscore",
			raw:      "admin_1.test",
			platform: "astrbot",
			want:     "admin_1.test",
		},
		{
			name:     "mention with leading at",
			raw:      "@10002",
			platform: "qq",
			want:     "10002",
		},
		{
			name:     "mention with leading at and mock user",
			raw:      "@user-99",
			platform: "qq",
			want:     "user-99",
		},
		{
			name:     "bracket mention token",
			raw:      "[@10002]",
			platform: "qq",
			want:     "10002",
		},
		{
			name:     "bracket token without at",
			raw:      "[10002]",
			platform: "qq",
			want:     "10002",
		},
		{
			name:     "bracket mention with whitespace padding",
			raw:      "  [@user-99]  ",
			platform: "qq",
			want:     "user-99",
		},
		{
			name:      "empty string",
			raw:       "",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "whitespace only",
			raw:       "   ",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "at sign only",
			raw:       "@",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "bracket at only",
			raw:       "[@]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard all",
			raw:       "all",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard at-all",
			raw:       "@all",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard bracket at-all",
			raw:       "[@all]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard 全体成员",
			raw:       "全体成员",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard bracket 全体成员",
			raw:       "[@全体成员]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "wildcard 0",
			raw:       "0",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "chinese nickname in bracket",
			raw:       "[@测试昵称]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "chinese nickname with at",
			raw:       "@测试昵称",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "chinese nickname bare",
			raw:       "测试昵称",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "multiple concatenated mentions",
			raw:       "[@111111][@222222]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "embedded colon",
			raw:       "qq:10002",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "embedded space",
			raw:       "123 456",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "embedded brackets",
			raw:       "user[123]",
			platform:  "qq",
			expectErr: true,
		},
		{
			name:      "embedded angle brackets",
			raw:       "<user123>",
			platform:  "qq",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CleanTargetID(tt.raw, tt.platform)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("CleanTargetID(%q, %q) expected error, got %q", tt.raw, tt.platform, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CleanTargetID(%q, %q) unexpected error: %v", tt.raw, tt.platform, err)
			}
			if got != tt.want {
				t.Fatalf("CleanTargetID(%q, %q) = %q, want %q", tt.raw, tt.platform, got, tt.want)
			}
		})
	}
}
