package proactive

import (
	"strings"
	"testing"
)

func TestSpecificReplyGate(t *testing.T) {
	valid := `{"messages":[{"type":"mention_specific","message_id":"fixture-old"},{"type":"plain","text":"回复上文"}]}`
	if !IsSpecificReply(valid) {
		t.Fatal("specific text reply rejected")
	}
	for _, args := range []string{
		`null`, `{}`, `{"messages":[]}`,
		`{"messages":[{"type":"plain","text":"unquoted"}]}`,
		`{"messages":[{"type":"mention_specific","message_id":"fixture-old"}]}`,
		strings.Replace(valid, `"fixture-old"`, `"  "`, 1),
		strings.Replace(valid, `"mention_specific"`, `"quote"`, 1),
		strings.Replace(valid, `"plain"`, `"image"`, 1),
		strings.Replace(valid, `"text":"回复上文"`, `"text":" "`, 1),
		strings.Replace(valid, `"text":"回复上文"`, `"text":"reply","is_sticker":true`, 1),
		strings.Replace(valid, `"text":"回复上文"`, `"text":"reply","url":"https://example.invalid"`, 1),
		strings.Replace(valid, `"messages":`, `"session":"other-session","messages":`, 1),
		strings.Replace(valid, `"messages":`, `"session":"","messages":`, 1),
		strings.Replace(valid, `"type":"plain","text":"回复上文"`, `"type":"mention_specific","message_id":"another"`, 1),
		valid + `{}`,
	} {
		if IsSpecificReply(args) {
			t.Errorf("unsafe proactive send allowed: %s", args)
		}
	}
}

func TestPromptPrefixIncludesSpecificReplyInstruction(t *testing.T) {
	const requested = "触发主动回复逻辑的消息可能不与中心事件直接相关。请调用send_message工具的mention_specific子类，找到对应的message id并引用它，避免莫名其妙。"
	if !strings.HasSuffix(PromptPrefix, requested) {
		t.Fatal("proactive prompt must append the requested instruction verbatim")
	}
}
