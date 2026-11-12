# 思考状态展示

实例的 Bot 行为与回复策略提供“展示思考状态”（`ENABLE_THINKING_STATUS`）。默认关闭，保存后热生效；仅在私聊实际进入 agent 思考轮次时展示平台原生输入标识。排队、被拒绝的输入、命令和网页模拟会话不发送状态；工具执行期间继续续发。

OneBot 使用 NapCat / SnowLuma 的 `set_input_status`（`user_id` 为当前私聊用户，`event_type=1` 为文字输入），每 4 秒续发。状态发送独立于模型请求；拒绝、超时后本轮停止续发，不影响正常回复。轮次结束、中断、连接关闭或实例停用后停止；最终回复前等待本地状态任务退出。平台状态通过收到回复或客户端自然超时消失，不发送未经确认的“取消”状态码。

AstrBot 需同步升级独立插件至 0.2.4 或更新版本。插件仅为支持原生输入状态的 QQ（aiocqhttp）/ Telegram 私聊声明布尔 `metadata.thinking_status_supported=true`；后端仅向声明能力的事件发送 `action=thinking_status`，使用 `hook_<message_id>` 路由，标记为中间动作。该动作不代表回复、不写入历史、不触发默认 LLM，也不改变最终回复。旧插件不会收到该动作。

插件发送有界的异步状态请求，后续状态不堆积；最终回复、noop、断线、超时和事件清理会取消并等待在途请求。Telegram 使用 AstrBot 事件的 `send_typing()`，沿用其 chat/topic 路由；Bot API 的 typing 状态最长维持 5 秒，收到 Bot 消息时清除。QQ 的实际可见性和维持时间由上游及客户端决定。

参考：[NapCat 实现](https://github.com/NapNeko/NapCatQQ/blob/main/packages/napcat-onebot/action/extends/SetInputStatus.ts)、[SnowLuma API](https://snowluma.github.io/zh/docs/api/extended/set_input_status)、[Telegram sendChatAction](https://core.telegram.org/bots/api#sendchataction)。
