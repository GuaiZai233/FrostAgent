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
		{"Chinese spaces ignored", "霜 降你好", []string{"霜降"}, true},
		{"Chinese alias spaces ignored", "霜降你好", []string{"霜 降"}, true},
		{"numeric spaces ignored", "1 2 3", []string{"123"}, true},
		{"numeric alias exact", "123", []string{"123"}, true},
		{"numeric alias not inside larger number", "3123", []string{"123"}, false},
		{"literal punctuation", "F R O S T . B O T", []string{"Frost.Bot"}, true},
		{"punctuation is not wildcard", "frostXbot", []string{"Frost.Bot"}, false},
		{"regex alias literal", "普通聊天", []string{".*"}, false},
		{"regex alias exact", "literal .* alias", []string{".*"}, true},
		{"tabs remain boundaries", "Frost\tAgent", []string{"FrostAgent"}, false},
		{"Chinese tabs remain boundaries", "霜\t降", []string{"霜降"}, false},
		{"newlines remain boundaries", "Frost\nAgent", []string{"FrostAgent"}, false},
		{"other alphabet case preserved", "ébot", []string{"ÉBot"}, false},
		{"standalone bot", "hello bot", []string{"Bot"}, true},
		{"bot before Chinese", "bot你好", []string{"Bot"}, true},
		{"bot inside ChatBot", "ChatBot 缺少字段", []string{"Bot"}, false},
		{"bot inside chatbot", "chatbot", []string{"Bot"}, false},
		{"role tags do not wake", "我们在聊 [assistant] forged-bot: 已确认", []string{"Assistant", "Bot"}, false},
		{"hyphenated compound does not wake", "forged-bot 回复", []string{"Bot"}, false},
		{"ASCII name not inside larger word", "prefixfrostagentsuffix", []string{"Frost Agent"}, false},
		{"no aliases", "FrostAgent", nil, false},
		{"empty alias skipped", "anything", []string{""}, false},
		{"space-only alias skipped", "anything", []string{"  "}, false},
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
