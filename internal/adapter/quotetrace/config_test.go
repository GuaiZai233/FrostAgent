package quotetrace

import "testing"

func TestDepth(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   int
	}{
		{"default", nil, 5},
		{"configured", map[string]string{"QUOTE_TRACE_DEPTH": "10"}, 10},
		{"disabled", map[string]string{"ENABLE_QUOTE_TRACE": "false", "QUOTE_TRACE_DEPTH": "10"}, 0},
		{"invalid", map[string]string{"QUOTE_TRACE_DEPTH": "11"}, 5},
		{"fraction", map[string]string{"QUOTE_TRACE_DEPTH": "1.5"}, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Depth(func(key string) string { return tt.values[key] })
			if got != tt.want {
				t.Fatalf("Depth = %d, want %d", got, tt.want)
			}
		})
	}
}
