package memory

import (
	"strings"
	"testing"
)

func TestValidateEvidence(t *testing.T) {
	srcMsg := "张三: 我平时每天早上喝生椰拿铁，李四明天参加考试。"

	tests := []struct {
		name     string
		evidence string
		wantOK   bool
		wantVal  string
	}{
		{
			name:     "valid verbatim quote",
			evidence: "喝生椰拿铁",
			wantOK:   true,
			wantVal:  "喝生椰拿铁",
		},
		{
			name:     "valid quote with surrounding spaces",
			evidence: "  我平时每天早上喝生椰拿铁  ",
			wantOK:   true,
			wantVal:  "我平时每天早上喝生椰拿铁",
		},
		{
			name:     "trivial quote less than 3 runes",
			evidence: "喝",
			wantOK:   false,
		},
		{
			name:     "two runes quote",
			evidence: "拿铁",
			wantOK:   false,
		},
		{
			name:     "foreign quote not in source",
			evidence: "周末去游泳打球",
			wantOK:   false,
		},
		{
			name:     "empty quote",
			evidence: "",
			wantOK:   false,
		},
		{
			name:     "whitespace only",
			evidence: "   ",
			wantOK:   false,
		},
		{
			name:     "oversized quote > 500 runes",
			evidence: strings.Repeat("长", 501),
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ValidateEvidence(srcMsg, tt.evidence)
			if ok != tt.wantOK {
				t.Errorf("ValidateEvidence(%q) ok = %v, want %v", tt.evidence, ok, tt.wantOK)
			}
			if ok && got != tt.wantVal {
				t.Errorf("ValidateEvidence(%q) = %q, want %q", tt.evidence, got, tt.wantVal)
			}
		})
	}
}

func TestSanitizeTags(t *testing.T) {
	tags := []string{
		"  coffee  ",
		"drink",
		"coffee", // duplicate
		"",       // empty
		"   ",    // empty
		strings.Repeat("a", 60), // oversized tag
	}

	sanitized := SanitizeTags(tags)
	if len(sanitized) != 3 {
		t.Fatalf("expected 3 sanitized tags, got %d: %+v", len(sanitized), sanitized)
	}
	if sanitized[0] != "coffee" {
		t.Errorf("tag 0 mismatch: %q", sanitized[0])
	}
	if sanitized[1] != "drink" {
		t.Errorf("tag 1 mismatch: %q", sanitized[1])
	}
	if len(sanitized[2]) != MaxTagLength {
		t.Errorf("tag 2 length not bounded to %d: got %d", MaxTagLength, len(sanitized[2]))
	}
}

func TestSanitizeSummary(t *testing.T) {
	s := "  用户自述每天早上喝生椰拿铁  "
	got := SanitizeSummary(s)
	want := "用户自述每天早上喝生椰拿铁"
	if got != want {
		t.Errorf("SanitizeSummary = %q, want %q", got, want)
	}

	oversized := strings.Repeat("字", 600)
	gotOversized := SanitizeSummary(oversized)
	if len([]rune(gotOversized)) != MaxSummaryRunes {
		t.Errorf("expected summary bounded to %d runes, got %d", MaxSummaryRunes, len([]rune(gotOversized)))
	}
}
