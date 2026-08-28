package llm

import "testing"

func TestLooksLikeMessagePayload(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name:   "send_message output",
			input:  `{"messages":[{"type":"plain","text":"hello"}]}`,
			expect: true,
		},
		{
			name:   "send_sticker output",
			input:  `{"messages":[{"type":"image","path":"/data/sticker/abc.png","is_sticker":true}]}`,
			expect: true,
		},
		{
			name:   "empty messages array",
			input:  `{"messages":[]}`,
			expect: false,
		},
		{
			name:   "error result",
			input:  `{"error":"no matching sticker found"}`,
			expect: false,
		},
		{
			name:   "plain text",
			input:  `search results: ...`,
			expect: false,
		},
		{
			name:   "no messages key",
			input:  `{"result":"ok"}`,
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := looksLikeMessagePayload(tt.input)
			if got != tt.expect {
				t.Errorf("looksLikeMessagePayload(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}
