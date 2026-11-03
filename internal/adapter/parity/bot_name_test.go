package parity

import "testing"

func TestContainsBotName(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		aliases []string
		want    bool
	}{
		{"lowercase", "hello frostagent", []string{"FrostAgent"}, true},
		{"uppercase", "FROSTAGENT hello", []string{"FrostAgent"}, true},
		{"spaces in text", "hello FrOsT AgEnT", []string{"FrostAgent"}, true},
		{"spaces in alias", "hello frostagent", []string{"Frost Agent"}, true},
		{"spaces between letters", "F R O S T A G E N T", []string{"Frost Agent"}, true},
		{"unicode spaces", "Frost\u3000\u00a0Agent", []string{"Frost Agent"}, true},
		{"mixed Chinese and English", "霜降 b O t 你好", []string{"霜降 Bot"}, true},
		{"Chinese literal", "霜降你好", []string{"霜降"}, true},
		{"Chinese spaces preserved", "霜 降你好", []string{"霜降"}, false},
		{"Chinese alias spaces preserved", "霜降你好", []string{"霜 降"}, false},
		{"numeric spaces preserved", "1 2 3", []string{"123"}, false},
		{"literal punctuation", "F R O S T . B O T", []string{"Frost.Bot"}, true},
		{"punctuation is not wildcard", "frostXbot", []string{"Frost.Bot"}, false},
		{"regex alias literal", "普通聊天", []string{".*"}, false},
		{"regex alias exact", "literal .* alias", []string{".*"}, true},
		{"tabs remain boundaries", "Frost\tAgent", []string{"FrostAgent"}, false},
		{"newlines remain boundaries", "Frost\nAgent", []string{"FrostAgent"}, false},
		{"other alphabet case preserved", "ébot", []string{"ÉBot"}, false},
		{"substring still matches", "prefixfrostagentsuffix", []string{"Frost Agent"}, true},
		{"no aliases", "FrostAgent", nil, false},
		{"empty alias skipped", "anything", []string{""}, false},
		{"empty text", "", []string{"FrostAgent"}, false},
		{"multiple aliases", "frost agent", []string{"unrelated", "FrostAgent"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsBotName(tt.text, tt.aliases); got != tt.want {
				t.Fatalf("ContainsBotName(%q, %q) = %v, want %v", tt.text, tt.aliases, got, tt.want)
			}
		})
	}
}
