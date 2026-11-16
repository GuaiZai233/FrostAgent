package onebot

import (
	"regexp"
	"strings"
)

var (
	historyCQCode       = regexp.MustCompile(`\[CQ:([A-Za-z0-9_]+)(?:,[^\[\]]*)?\]`)
	historyCQTextEscape = strings.NewReplacer("&amp;", "&", "&#91;", "[", "&#93;", "]")
	historyCQDataEscape = strings.NewReplacer("&amp;", "&", "&#91;", "[", "&#93;", "]", "&#44;", ",")
)

// historyCQText interprets string-format messages only at the archive boundary.
// Tokenize before unescaping so escaped CQ examples remain ordinary text.
func historyCQText(message string) (string, string) {
	var text strings.Builder
	replyTo, start := "", 0
	for _, match := range historyCQCode.FindAllStringIndex(message, -1) {
		text.WriteString(historyCQTextEscape.Replace(message[start:match[0]]))
		kind, params, _ := strings.Cut(message[match[0]+len("[CQ:"):match[1]-1], ",")
		switch kind {
		case "reply":
			replyTo = historyCQParam(params, "id")
		case "at":
			text.WriteString("[@" + historyCQParam(params, "qq") + "]")
		default:
			// Discard all media parameters, including URLs and inline payloads.
			text.WriteString("[" + kind + "]")
		}
		start = match[1]
	}
	text.WriteString(historyCQTextEscape.Replace(message[start:]))
	return text.String(), replyTo
}

func historyCQParam(params, name string) string {
	for _, param := range strings.Split(params, ",") {
		key, value, ok := strings.Cut(param, "=")
		if ok && key == name {
			return historyCQDataEscape.Replace(value)
		}
	}
	return ""
}
