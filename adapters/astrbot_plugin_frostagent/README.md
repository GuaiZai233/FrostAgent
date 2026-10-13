# AstrBot 适配器插件 (astrbot_plugin_frostagent)

[FrostAgent](https://github.com/GuaiZai233/FrostAgent) 的 AstrBot 专用适配器。

## 配置说明

先在 FrostAgent 控制台创建并启用实例，再从该实例的概览复制 AstrBot WebSocket 地址（例如 `ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot`）。实例 ID 必须替换为实际值；插件不再提供不带实例 ID 的默认地址，缺失或填写旧版 `/ws/astrbot` 地址时会直接报错。

在 AstrBot Web 控制台中配置插件参数：

| 参数项 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `ws_url` | FrostAgent 实例专属 WebSocket 地址（必填） | 无 |
| `http_base_url` | FrostAgent HTTP API 根地址；分容器部署时填写 AstrBot 可访问的地址 | `http://127.0.0.1:8080` |
| `forward_all_group_messages` | 是否将所有群消息发送给 FrostAgent (用于群聊总结) | `true` |
| `heartbeat_interval` | 心跳保活间隔 (秒) | `30` |
| `reconnect_interval` | 异常断开重连间隔 (秒) | `5` |

## 群聊唤醒

AstrBot 自身的唤醒词、命令前缀（如 `/help`）不作为 FrostAgent 的唤醒信号。插件只上报真实 @机器人或引用机器人；FrostAgent 后端继续独立识别实例的 `BOT_NAME` / `BOT_ALIASES`。私聊仍按既有私聊路径处理。

`forward_all_group_messages=true` 时，普通群消息仍转发用于群聊上下文；转发本身不表示显式唤醒。若希望后端从普通文本中识别自身名称或别名，应保持此设置开启。关闭后插件只转发 @或引用机器人的群消息。独立配置的主动回复概率仍由后端控制。

引用追溯由 FrostAgent 实例设置中的开关和深度控制，默认开启并追溯 5 层，最大 10 层。aiocqhttp 平台的引用消息由后端在安全入口通过后按需发出 `quote_lookup` 请求，插件以当前事件的 Bot 连接调用 `get_msg` 并回传结果；旧版插件不声明回查能力，因此不会产生额外等待；无法取得的旧消息会停止后续追溯。其他平台仍保留直接引用图片，并将图片描述归属到被引用的消息。

## 表情包投递回执

启用 FrostAgent 的“表情包使用积极度”后，请同步更新此插件。插件会在贴纸图片获取和 AstrBot 的 `event.send` 调用完成后回传投递结果；后端据此更新表情包配额。旧版插件缺少该回执能力时，后端会拒绝贴纸发送并提示更新插件。
