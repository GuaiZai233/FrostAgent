package onebot

import (
	"encoding/json"
	"strings"
)

// Only include text supplied in an outbound node. Referenced messages remain
// IDs; never load another conversation to render the archive.
func historyForwardText(raw json.RawMessage) string {
	var nodes []struct {
		Data struct {
			ID      json.RawMessage `json:"id"`
			Content json.RawMessage `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &nodes) != nil {
		return "[forward]"
	}
	var text strings.Builder
	for _, node := range nodes {
		text.WriteString("[forward]")
		if id := historyRawID(node.Data.ID); id != "" {
			text.WriteString("[转发消息 " + id + "]")
		} else {
			content, _ := historyMessageText(node.Data.Content)
			text.WriteString(content)
		}
	}
	return text.String()
}
