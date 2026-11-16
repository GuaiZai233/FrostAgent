package llm

import (
	"fmt"
	"strings"

	"github.com/GuaiZai233/FrostAgent/internal/replyformat"
)

const messageGuidancePrefix = "<frostagent_msg_guidance>\n"

func messageGuidance(limit int) string {
	limitGuidance := ""
	if limit > 1 {
		limitGuidance = fmt.Sprintf("你不是无限分段的，你被允许插入<FROSTAGENT_SEGMENT_MARK>的数量是 **%d**。", limit-1)
	}
	return messageGuidancePrefix +
		"为保证拟真的对话效果，你被允许多段回复用户消息。" + limitGuidance +
		"在正常回复中插入<FROSTAGENT_SEGMENT_MARK>，FrostAgent 后端会自动解析并分段将你的消息发出。请根据语境决定是否这样输出。\n" +
		"如因特殊需求，你想完整输出 mark 的整个原文，请用反引号``包裹它，这样后端不会解析。\n" +
		"</frostagent_msg_guidance>"
}

func isGeneratedMessageGuidance(content string) bool {
	if !strings.HasPrefix(content, messageGuidancePrefix) {
		return false
	}
	// Match the complete generated body for every supported limit so refreshing
	// settings removes old guidance without discarding custom system prompts.
	for limit := 0; limit <= replyformat.MaxSegmentLimit; limit++ {
		if content == messageGuidance(limit) {
			return true
		}
	}
	return false
}

// messageGuidanceMessages rebuilds transient instructions on every iteration,
// including callers supplying a system message and already-running sessions.
func (e *Engine) messageGuidanceMessages(messages []ChatMessage) []ChatMessage {
	result := make([]ChatMessage, 0, len(messages)+1)
	for _, msg := range messages {
		content, _ := msg.Content.(string)
		if msg.Role == "system" && isGeneratedMessageGuidance(content) {
			continue
		}
		result = append(result, msg)
	}
	if !replyformat.Enabled(e.Getenv) {
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
	result[position] = ChatMessage{Role: "system", Content: messageGuidance(replyformat.ParseLimit(e.Getenv(replyformat.LimitEnvKey)))}
	return result
}
