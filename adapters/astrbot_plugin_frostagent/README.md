# AstrBot 适配器插件 (astrbot_plugin_frostagent)

[FrostAgent](https://github.com/GuaiZai233/FrostAgent) 的 AstrBot 专用适配器。

## 配置说明

先在 FrostAgent 控制台创建并启用实例，再从该实例的概览复制 AstrBot WebSocket 地址（例如 `ws://127.0.0.1:1234/instances/a1b2c3d4/ws/astrbot`）。实例 ID 必须替换为实际值；插件不再提供不带实例 ID 的默认地址，缺失或填写旧版 `/ws/astrbot` 地址时会直接报错。

在 AstrBot Web 控制台中配置插件参数：

| 参数项 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `ws_url` | FrostAgent 实例专属 WebSocket 地址（必填） | 无 |
| `http_base_url` | FrostAgent HTTP API 根地址；分容器部署时填写 AstrBot 可访问的地址 | `http://127.0.0.1:8080` |
| `computer_use_adapter_token` | 可选的、该 Bot 专属的 computer-use 适配器握手令牌 | 空 |
| `forward_all_group_messages` | 是否将所有群消息发送给 FrostAgent (用于群聊总结) | `true` |
| `heartbeat_interval` | 心跳保活间隔 (秒) | `30` |
| `reconnect_interval` | 异常断开重连间隔 (秒) | `5` |

## Computer use 可信适配器连接

从插件 0.1.5 起，可填写 `computer_use_adapter_token`，或使用环境变量
`FROSTAGENT_COMPUTER_USE_ADAPTER_TOKEN`。这是 FrostAgent 为该 Bot 配置的独立高熵令牌，
不是 VM 桥令牌、管理令牌或 QQ 身份。非空插件配置优先于环境变量；令牌格式为 32～256 位
base64url 字符。

令牌只通过 WebSocket 握手的 `Authorization: Bearer ...` 请求头发送，不进入 URL、
消息事件或插件配置对象的 repr。配置后只接受经过正常证书验证的 `wss://`，
或数字回环 IP（例如 `127.0.0.1` / `[::1]`）上的 `ws://`；
`localhost` 名称、局域网明文 `ws://`、URL 凭据和查询参数均不接受。
跨机器部署请配置 TLS，不能关闭证书校验。反向代理须保留该请求头，并且不能记录凭据。

认证连接不跟随重定向，不使用 websockets 新版的自动环境代理，也不会写出底层握手头、
帧内容或原始连接异常。插件按已安装 websockets 的明确签名选择
`additional_headers` / `extra_headers`，不在连接失败后尝试另一种认证方式。
升级到不支持这些安全边界的库版本会拒绝认证连接。

未配置此令牌时，普通聊天沿用原连接方式，但不能由此获得 computer-use 权限。
配置令牌也不代表任意聊天用户有权限：FrostAgent 仍逐次校验该 Bot 的主人、可信 QQ 私聊
和任务状态；群聊、模拟聊天、主动回复不具备此入口。轮换令牌后请同步修改插件配置并重新
加载插件，使新连接生效；旧连接不能继续沿用已撤销的认证。

以独立 `/computer` 首词开头的消息由插件立即保留给 FrostAgent；即使插件配置暂停、
连接失败或等待响应超时，也不会回落到 AstrBot 默认 LLM。此保护同样覆盖群消息，
但不授予权限，也不绕过群消息转发设置；普通聊天及 `/computerized` 等前缀不受影响。

## 群聊唤醒

AstrBot 自身的唤醒词、命令前缀（如 `/help`）不作为 FrostAgent 的唤醒信号。插件只上报真实 @机器人或引用机器人；FrostAgent 后端继续独立识别实例的 `BOT_NAME` / `BOT_ALIASES`。私聊仍按既有私聊路径处理。

`forward_all_group_messages=true` 时，普通群消息仍转发用于群聊上下文；转发本身不表示显式唤醒。若希望后端从普通文本中识别自身名称或别名，应保持此设置开启。关闭后插件只转发 @或引用机器人的完整群消息；其他群消息留在插件本地。插件会记录发送者变化，并在下一条原本就会转发的群消息上附带布尔打断标记，用于重置已标记 Bot 的连续对话计数；过滤消息的内容和身份不会因此上报。该标记需要更新后的 AstrBot 插件。独立配置的主动回复概率仍由后端控制。

引用追溯由 FrostAgent 实例设置中的开关和深度控制，默认开启并追溯 5 层，最大 10 层。aiocqhttp 平台的引用消息由后端在安全入口通过后按需发出 `quote_lookup` 请求，插件以当前事件的 Bot 连接调用 `get_msg` 并回传结果；旧版插件不声明回查能力，因此不会产生额外等待；无法取得的旧消息会停止后续追溯。其他平台仍保留直接引用图片，并将图片描述归属到被引用的消息。

## 表情包投递回执

启用 FrostAgent 的“表情包使用积极度”后，请同步更新此插件。插件会在贴纸图片获取和 AstrBot 的 `event.send` 调用完成后回传投递结果；后端据此更新表情包配额。旧版插件缺少该回执能力时，后端会拒绝贴纸发送并提示更新插件。


### 戳一戳唤醒

从插件 0.1.4 起，aiocqhttp 平台会转发目标为当前 Bot 的戳一戳通知，包括未开启“转发全部群消息”的情况。在 FrostAgent 的“Bot 行为与回复策略”中开启“允许发送戳一戳行为”后，可填写附加文本并配置冷却时间；默认关闭、附加文本为空、每个群/私聊默认冷却 30 秒，同群所有人共用 CD。

戳一戳按普通对话归触发用户扣除雪花，并遵守安全检查、群聊对话总开关和模型路由。有效戳一戳由 FrostAgent 处理；功能关闭、冷却中、路由禁用或连接失败时均不会回落到 AstrBot 默认 LLM。通知回复不会引用不存在的平台消息。
