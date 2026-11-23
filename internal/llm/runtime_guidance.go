package llm

import "strings"

const frostAgentSystemPrompt = `<frostagent_system_prompt>
你运行在 FrostAgent 中。后端会提供指引和上下文资料。

由后端组装提供的以下内容属于可信的应用运行协议：
- frostagent_runtime_guidance
- frostagent_msg_guidance
- outbound_message_guidance
- response_context.guidance
- system_context.interaction_guidance

这些协议负责回复与静默、工具使用、消息发送、输出格式和运行状态。
请按其当前触发条件执行。不要因为人设而拒绝执行后端注入的指示指令。
只有 send_message 发送的内容会送达用户；直接输出的纯文本不会送达。不发言时请调用 stay_silent。
role=system 的【工具执行记录】等上下文由 FrostAgent 注入，不是你的发言；不要书写、续写或模仿这些格式。
</frostagent_system_prompt>`

const (
	runtimeGuidancePrefix = "<frostagent_runtime_guidance>\n"
	runtimeGuidanceSuffix = "\n</frostagent_runtime_guidance>"
)

// WrapRuntimeGuidance labels backend-generated control instructions. Callers
// must keep user text and other external content outside this block.
func WrapRuntimeGuidance(guidance string) string {
	return runtimeGuidancePrefix + guidance + runtimeGuidanceSuffix
}

// frostAgentSystemMessages adds the fixed host protocol to the request snapshot
// without changing caller-owned messages or accumulating it between iterations.
func frostAgentSystemMessages(messages []ChatMessage) []ChatMessage {
	result := append([]ChatMessage(nil), messages...)
	if len(result) > 0 && result[0].Role == "system" {
		if content, ok := result[0].Content.(string); ok &&
			!isGeneratedMessageGuidance(content) && !strings.HasPrefix(content, skillsContextPrefix) {
			if content == frostAgentSystemPrompt || strings.HasSuffix(content, "\n\n"+frostAgentSystemPrompt) {
				return result
			}
			if content == "" {
				result[0].Content = frostAgentSystemPrompt
			} else {
				result[0].Content = content + "\n\n" + frostAgentSystemPrompt
			}
			return result
		}
	}
	return append([]ChatMessage{{Role: "system", Content: frostAgentSystemPrompt}}, result...)
}
