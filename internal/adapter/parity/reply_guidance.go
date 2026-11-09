package parity

// ReplyDecisionGuidance is the per-turn instruction both adapters send so the
// model decides between replying and terminating a turn. Once a reply has been
// delivered via send_message, the turn must end with stay_silent, which keeps
// the delivered text in history and never emits another final message.
const ReplyDecisionGuidance = "请结合用户消息与已有上下文自行决定是否回复；确实无需回应时，单独调用 stay_silent。若本轮已通过任何工具向用户发出过内容（如 send_message 或定时指令），必须调用 stay_silent 工具中止此轮对话，不要再输出任何最终文字。仅做内部查询的工具（如记忆检索）不适用此规则，应正常给出回答。用户明确要求回应时应正常回答。"
