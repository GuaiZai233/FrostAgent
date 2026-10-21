# AstrBot 接入

仓库的 `adapters/astrbot_plugin_frostagent` 是专用 AstrBot 插件。它将消息交给 FrostAgent，并把 FrostAgent 动作转换为 AstrBot 的消息发送，使实例与 AstrBot 插件生态配合使用。

## 配置步骤

1. 在 FrostAgent 创建、配置并启用实例。
2. 按 AstrBot 的插件安装方式安装仓库中的 `astrbot_plugin_frostagent` 插件目录。
3. 在插件设置填写当前实例的 AstrBot WebSocket 地址。
4. 若跨容器，填写 AstrBot 可访问的 HTTP API 根地址。
5. 关闭 AstrBot 自带的 LLM 回复功能，避免两个回复引擎同时处理。
6. 检查插件连接日志，再测试私聊、真实 @机器人和贴图。

```text
ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot
```

旧版不带实例 ID 的 `/ws/astrbot` 地址不能用于当前接入。

## 插件参数

| 参数                         | 默认值                  | 用途                                   |
| ---------------------------- | ----------------------- | -------------------------------------- |
| `ws_url`                     | 空，必填                | 实例专属 WebSocket 地址                |
| `http_base_url`              | `http://127.0.0.1:8080` | 获取贴图的 HTTP 根地址                 |
| `forward_all_group_messages` | `true`                  | 转发普通群消息供背景上下文和群总结使用 |
| `heartbeat_interval`         | `30`                    | 心跳间隔，秒                           |
| `reconnect_interval`         | `5`                     | 异常断开后重连间隔，秒                 |

## 群聊唤醒

AstrBot 自己的唤醒词与 `/help` 等命令前缀不作为 FrostAgent 唤醒信号。插件上报真实 @机器人或引用机器人，FrostAgent 再识别实例的 `BOT_NAME` 与 `BOT_ALIASES`。

关闭 `forward_all_group_messages` 后，插件只转发 @或引用机器人的完整群消息。未转发的消息无法进入 FrostAgent 的名称唤醒、群摘要与消息计数。插件可通过下一条转发消息携带的打断标记重置已标记 Bot 的连续对话计数；这个标记不会附带过滤消息正文。

插件 0.1.4 起，aiocqhttp 还会单独转发指向当前 Bot 的戳一戳通知，不受「转发全部群消息」开关限制。后端默认不唤醒，需要另行开启并配置[戳一戳冷却](../usage/group-chat#戳一戳唤醒)。这些通知不会回落到 AstrBot 默认 LLM。

## 引用与贴图

引用追溯由 FrostAgent 的 `ENABLE_QUOTE_TRACE` 与 `QUOTE_TRACE_DEPTH` 控制，默认开启、深度 5，最大 10。aiocqhttp 平台通过新版插件的 `quote_lookup` 能力按需回查消息；旧插件不声明该能力时不会额外等待。

新版插件在图片获取和 AstrBot `event.send` 完成后回传贴图投递结果。启用[表情包积极度](../usage/stickers)时请同步更新插件；缺少回执能力的旧插件会被后端拒绝发送贴图。

插件接入说明的源码入口见 [适配器 README](https://github.com/GuaiZai233/FrostAgent/blob/main/adapters/astrbot_plugin_frostagent/README.md)。
