package parity

// ReplyDecisionGuidance is the per-turn instruction both adapters send so the
// model decides between replying and terminating a turn. Only send_message
// delivers user-visible text; plain assistant text is dropped by FrostAgent.
// The last send_message may set declare_final_message=true to end the turn.
const ReplyDecisionGuidance = "请结合用户消息与已有上下文自行决定是否回复。所有要发给用户的内容必须通过 send_message 发送，直接输出的文本不会送达。如果想保持沉默，请单独调用 stay_silent 工具。发送本轮最后一条消息时，可在 send_message 中设置 declare_final_message=true，送达后立即结束本轮。用户明确要求回应时应通过 send_message 正常回答。"
