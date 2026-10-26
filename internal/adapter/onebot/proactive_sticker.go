package onebot

import (
	"encoding/json"
	"github.com/GuaiZai233/FrostAgent/internal/adapter/parity"
	"github.com/GuaiZai233/FrostAgent/internal/tools"
	"regexp"
	"strings"
)

var outboundCQCode = regexp.MustCompile(`\[CQ:[^\[\]]*\]`)

// filterProactiveStickerCQ removes stickers from the legacy string-message
// path. Structured messages have already passed the engine's sticker filter;
// their plain components must not be interpreted as CQ codes here.
func filterProactiveStickerCQ(reply string) string {
	var payload struct {
		Messages []tools.Msg `json:"messages"`
	}
	if json.Unmarshal([]byte(reply), &payload) == nil && len(payload.Messages) > 0 {
		return reply
	}
	return outboundCQCode.ReplaceAllStringFunc(reply, func(code string) string {
		parts := strings.Split(code[len("[CQ:"):len(code)-1], ",")
		if parity.IsStickerType(parts[0]) {
			return ""
		}
		if parts[0] == "image" {
			for _, param := range parts[1:] {
				key, value, ok := strings.Cut(param, "=")
				if !ok {
					continue
				}
				if ((key == "sub_type" || key == "subType") && parity.IsStickerSubType(value)) || (key == "is_sticker" && value == "true") {
					return ""
				}
			}
		}
		return code
	})
}
