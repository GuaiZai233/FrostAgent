# Telegram 原生接入

FrostAgent 直接通过 Telegram Bot API 收发消息，无需 AstrBot 或公网 Webhook。

## 配置

1. 在 Telegram 的官方 `@BotFather` 创建机器人并取得 Token。
2. 打开 FrostAgent 对应实例的「Bot 服务端设置」→「适配器」，填写 Telegram Bot Token 并启用 Telegram。按需设置代理，保存后重启实例。
3. 私聊机器人并发送 `/start`。群聊时将机器人加入普通群或超级群；如果需要观察普通群消息和主动回复，在 BotFather 关闭 Privacy Mode，并按 Telegram 要求重新加入群。启用 FrostAgent 的群聊唤醒或主动回复设置。
4. 在模型路由中配置对话模型；图片识别可使用视觉模型或支持图片的对话模型。

对应实例配置项：

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `TELEGRAM_ENABLED` | `false` | 启用官方 Bot API 长轮询 |
| `TELEGRAM_BOT_TOKEN` | 空 | BotFather Token，按密钥处理 |
| `TELEGRAM_PROXY_URL` | 空 | 专用 HTTP(S)、SOCKS5 代理；留空使用系统代理环境变量 |

三个配置项均在重启实例后生效。Token 和带认证信息的代理地址不会出现在适配器日志中。每个 Bot Token 只能由一个轮询进程消费；已有 Webhook 或其他消费者会导致冲突，适配器会停止并记录提示，不自动抢占已有接入。

## 首版范围

- 私聊、普通群及不使用 Topic 的超级群：文本和图片、图片说明文字。
- 群聊 `@机器人`、回复机器人、命令、FrostAgent 名称/别名唤醒；复用现有主动回复策略。
- 原生引用和用户提及、长文本分条、思考状态、工具回复及图片发送；表情包库中的静态图片以图片上传。
- 复用模型路由、安全控制、会话顺序、记忆、群聊上下文和主动感知。原生接入与 AstrBot 桥接不能同时消费同一个 Telegram Bot。
- 图片下载和上传限制为 8 MiB；下载后的图片以字节交给模型，不传递含 Bot Token 的下载地址。

暂不接收频道、Forum Topic、编辑消息、匿名发送者或其他 Bot 的消息，也不提供语音识别、视频理解、相册聚合或 Telegram 动态贴纸接收。媒体的 HTTP(S) 地址可用于工具发送；模型提供的本机文件路径不会被读取。表情包仅从当前实例已有的表情包库读取。

Telegram 身份和 QQ 身份独立。QQ 管理员白名单不会授予同号 Telegram 用户管理权限，首版不新增 Telegram 管理员消息指令。QQ 专属工具仍受平台限制。

## 更新确认和故障处理

更新按 Telegram 返回的顺序进入公共对话处理；不同会话可并发执行。使用最多 100 条的未确认窗口，长轮次运行时继续接收后续消息并去重，只确认已完成的连续前缀。停止实例会取消轮询并保留未确认更新，实例重启后可能重新处理最后一批消息。发送超时也可能发生平台已接收而本机未获得确认的情况；此类发送不会自动重试，避免盲目重复发送。明确的 429 限流拒绝会按 `retry_after` 等待并有限重试。

此实现不承诺恰好一次投递。Telegram 对待处理更新的保留时间有限，长期离线不能作为消息归档；FrostAgent 记忆也不会自动合并旧 QQ 身份。

协议参考：[Telegram Bot API](https://core.telegram.org/bots/api)、[Bot FAQ](https://core.telegram.org/bots/faq)。
