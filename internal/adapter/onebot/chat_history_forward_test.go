package onebot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOneBotHistoryForwardText(t *testing.T) {
	raw := json.RawMessage(`[{"type":"node","data":{"content":[{"type":"text","data":{"text":"generated original"}},{"type":"image","data":{"url":"https://media.invalid/private"}}]}},{"type":"node","data":{"id":"fixture-reference"}}]`)
	text := historyForwardText(raw)
	if !strings.Contains(text, "generated original[image]") || !strings.Contains(text, "fixture-reference") || strings.Contains(text, "media.invalid") {
		t.Fatalf("forward original: %q", text)
	}
}
