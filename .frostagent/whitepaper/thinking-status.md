# 思考状态展示

实例的 Bot 行为与回复策略提供“展示思考状态”（`ENABLE_THINKING_STATUS`）。默认开启（未设置也视为开启，显式设置 `false` 才关闭），保存后热生效；仅在私聊实际进入 agent 思考轮次时展示平台原生输入标识。排队、被拒绝的输入、命令和网页模拟会话不发送状态；工具执行期间继续续发。

OneBot 使用 NapCat / SnowLuma 的 `set_input_status`（`user_id` 为当前私聊用户，`event_type=1` 为文字输入），每 4 秒续发。状态发送独立于模型请求；拒绝、超时后本轮停止续发，不影响正常回复。轮次结束、中断、连接关闭或实例停用后停止；最终回复前等待本地状态任务退出。平台状态通过收到回复或客户端自然超时消失，不发送未经确认的“取消”状态码。

参考：[NapCat 实现](https://github.com/NapNeko/NapCatQQ/blob/main/packages/napcat-onebot/action/extends/SetInputStatus.ts)、[SnowLuma API](https://snowluma.github.io/zh/docs/api/extended/set_input_status)。
