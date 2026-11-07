package llm

import (
	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
	"strings"
)

const messageGuidance = "<frostagent_msg_guidance>\n" +
	"你被允许多段回复用户消息。在正常回复中插入<FROSTAGENT_SEGMENT_MARK>，FrostAgent 后端会自动解析并分段将你的消息发出。请根据语境决定是否这样输出。\n" +
	"如因特殊需求，你想完整输出 mark 的整个原文，请用反引号``包裹它，这样后端不会解析。\n" +
	"</frostagent_msg_guidance>"

// messageGuidanceMessages rebuilds transient instructions on every iteration,
// including callers supplying a system message and already-running sessions.
func (e *Engine) messageGuidanceMessages(messages []ChatMessage) []ChatMessage {
	result := make([]ChatMessage, 0, len(messages)+1)
	for _, msg := range messages {
		if msg.Role == "system" && msg.Content == messageGuidance {
			continue
		}
		result = append(result, msg)
	}
	if e.Getenv(replyformat.EnvKey) != "true" {
		return result
	}
	position := 0
	if len(result) > 0 && result[0].Role == "system" {
		if content, ok := result[0].Content.(string); !ok || !strings.HasPrefix(content, skillsContextPrefix) {
			position = 1
		}
	}
	result = append(result, ChatMessage{})
	copy(result[position+1:], result[position:])
	result[position] = ChatMessage{Role: "system", Content: messageGuidance}
	return result
}
