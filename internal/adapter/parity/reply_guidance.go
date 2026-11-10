package parity

// ReplyDecisionGuidance is the per-turn instruction both adapters send so the
// model decides between replying and terminating a turn. Once a reply has been
// delivered via send_message, the turn must end with stay_silent, which keeps
// the delivered text in history and never emits another final message.
const ReplyDecisionGuidance = "请结合用户消息与已有上下文自行决定是否回复。如果想保持沉默，请单独调用 stay_silent 工具。如果需要结束本轮，请必须最终调用 stay_silent 中止此轮对话。用户明确要求回应时应正常回答。"
