# QQ 富消息发送

FrostAgent 原生提供 QQ 消息组件和互动操作，通过当前对话绑定的 OneBot
连接发送，支持 NapCat、SnowLuma 的兼容动作。无需安装 Aurabot。

## 工具入口

`send_message` 仅接收基础消息组件：文字、图片、语音、视频、文件、@用户、引用与
`mention_specific` 定向回复，目标固定为当前会话。基础媒体保留原有发送与回执链路。
QQ 富组件、媒体增强字段、转发记录读取、转发、互动与白名单跨会话发送统一使用 `qq_extend_messages`：

```json
{"action":"discover"}
```

查询某项能力的详细参数：

```json
{"action":"discover","capability":"send"}
```

执行时传 `action` 和 `arguments`，详细字段按需返回，不会向模型预先注册几十个工具。
`discover.available` 表示当前轮次是否有原生 OneBot 连接，并不表示网关已经通过真机验证。

## 消息组件

`qq_extend_messages(action=send).arguments.messages` 接受以下组件。
其中基础文字、媒体、@与引用也可用于扩展消息的组合或白名单跨群发送。
`mention_specific` 仅属于 `send_message`。QQ 扩展类型和下述媒体增强字段不会重复
出现在 `send_message` 的参数定义中；通过基础入口传入会被拒绝并提示使用扩展工具。
ID 均用字符串表示；从上下文选取实际 ID，不使用昵称猜测。

| type | 常用字段 | 含义 |
| --- | --- | --- |
| `plain` | `text` | 普通文字 |
| `mention_user` | `mention_user_id` | 真正的 @用户 |
| `mention_all` | 无 | @全体成员，仅群聊；权限和次数由 QQ 网关校验 |
| `quote` | `message_id` | 引用当前连接在当前对话观察过的消息 |
| `mention_specific` | `message_id` | 仅 `send_message`，放在首位；保留现有定向回复约束 |
| `face` | `data.id` | QQ 系统表情编号 |
| `dice` / `rps` | 无 | 随机骰子、猜拳，不指定结果 |
| `contact` | `data.type`, `data.id` | `type=qq/group`，推荐好友或群 |
| `music` | `data.type`, `data.id` | 平台音乐；`qq/163/kugou/migu/kuwo` |
| `music` | `data.type=custom`, `url`, `audio`, `title` | 自定义音乐；可加 `image/content/singer`，均在 `data` 中 |
| `share` | `data.url`, `data.title` | 链接分享，可加 `data.content/image` |
| `location` | `data.lat`, `data.lon` | 纬度 ±90、经度 ±180，可加 `data.title/content` |
| `mface` | `message_id`, `sticker_index` | 重发可信消息中的商城表情；索引从 0 开始，默认 0 |
| `mface` | `data.emoji_id/emoji_package_id/key` | 已有准确原生元数据时直接发送，可加 `data.summary` |
| `image` / `record` / `video` / `file` | `path` 或 `url` | 宿主/沙箱文件或 HTTP(S) 地址，二选一 |

商城表情重发会在适配器内读取可信消息的原生元数据。源消息没有完整的
`emoji_id/emoji_package_id/key` 时返回明确错误；不会编造 key。表情包开关同时
约束普通图片表情和商城表情，不改变现有表情库的存储格式。

链接、图片、音乐卡片中的外部 URL 仅接受 HTTP(S)。不开放任意 JSON/XML 卡片透传。

### 媒体增强字段

以下 `name/summary/thumb/folder_id/transfer` 字段仅在扩展入口使用。
`is_sticker` 保留为两个入口均支持的基础图片标记。

| 字段 | 适用类型 | 含义 |
| --- | --- | --- |
| `name` | 图片、语音、视频、文件 | 展示文件名；不接受目录分隔符 |
| `summary` | 图片 | 图片/表情的预览说明 |
| `thumb` | 视频 | 视频封面，本地路径或 HTTP(S) URL |
| `folder_id` | 群文件 | 目标群文件目录，私聊不能使用 |
| `transfer` | 本地媒体 | `auto`（默认）、`shared`、`inline` |
| `is_sticker` | 图片 | 保留既有图片表情标记 |

宿主文件的 `auto` 先使用本地路径；收到明确的文件不存在或无法读取回执后，才尝试 base64。
`shared` 只走共享文件路径，`inline` 直接携带文件内容。inline 有 30 MiB 上限；
整次逻辑发送的所有分段、沙箱文件、视频封面和本地回落共享这一预算。普通共享路径预检保持常数内存，
不会为了发送一个大文件把完整文件读入内存。网关直接拉取 HTTP(S) 资源。

文件使用群/私聊上传接口，普通消息与文件按原始顺序发送。选定群目录后不使用
会丢失该目录的文件段回落。视频封面随媒体一起转换，inline 不依赖远端能访问本机路径。

### 图片直链与沙箱产物

以下发送链路适用于原生 OneBot QQ；当前会话用 `send_message`，富消息或白名单跨群用
`qq_extend_messages`。公开 HTTP(S) 图片直链放在 `image.url`，网关负责拉取并发送图片：

```json
{"messages":[{"type":"image","url":"https://example.invalid/chart.png"}]}
```

`execute_command`、Python 或 `run_skill` 生成的文件应保存在当前会话的 `/sandbox/` 下。
图片用 `image.path`；PDF、表格和其他文档用 `file.path`：

```json
{"messages":[{"type":"image","path":"/sandbox/chart.png"},{"type":"file","path":"/sandbox/report.pdf"},{"type":"file","path":"/sandbox/table.csv"}]}
```

`path` 也接受 `sandbox:/sandbox/report.pdf` 和 `sandbox:///sandbox/report.pdf`。
视频的 `thumb` 同样支持沙箱路径；其他路径仍指向 FrostAgent 宿主文件系统。
后端在当前来源会话中分块读取文件并生成协议数据，工具结果和模型上下文保留路径，
无需让模型输出 base64，也无需共享沙箱卷或新增公开下载服务。

沙箱必须已启用；仅接受 `/sandbox/` 内非空普通文件，不接受 `..`、符号链接、目录或设备。
读取超时、输出截断、文件在读取时变化或超出剩余预算都会停止发送，不回退读取宿主同名文件。
沙箱的 `auto` 和 `inline` 都通过后端传输；`shared` 会被拒绝。

白名单跨群示例：读取当前群/私聊的沙箱，发送到配置允许的目标群：

```json
{"action":"send","arguments":{"target_group_id":"42","messages":[{"type":"file","path":"/sandbox/report.pdf"}]}}
```

目标群不会改变文件来源的会话命名空间。真实 QQ 展示与文件大小限制仍由网关确认，
平台 ACK 和各组件的发送结果沿用既有规则。

## 互动与转发

| action | arguments | 范围 |
| --- | --- | --- |
| `poke` | `user_id`（可省略，默认当前发言者） | 当前群；私聊仅当前对方 |
| `reaction` | `message_id`, `emoji_id`, `set` | 当前群，`set=false` 取消回应，默认 true |
| `recall` | `message_id` | 仅当前连接、当前对话已获发送回执的机器人消息 |
| `read_forward` | `message_id`, `forward_index`, `offset/limit`, `path` | 查看当前对话中观察过的合并转发聊天记录 |
| `forward_message` | `message_id` | 当前对话观察过的消息 |
| `forward_bundle` | `nodes`, `source/summary/prompt/news` | 合并转发，最多 50 个节点 |

转发节点二选一：

```json
{
  "action":"forward_bundle",
  "arguments":{
    "nodes":[
      {"message_id":"101"},
      {"messages":[{"type":"plain","text":"补充说明"}]}
    ],
    "source":"会话摘录",
    "summary":"两条消息",
    "news":["消息预览"]
  }
}
```

示例 ID 需替换为当前可信上下文中的消息 ID。新建节点使用机器人身份。
节点内使用媒体 URL，不接受文件上传、群目录、引用段、@全体或依赖本地传输的媒体。
不指定目标时使用当前会话。跨群发送需要配置下述白名单，不支持跨账号连接。

### 查看转发的聊天记录

先查询 `discover` 的 `read_forward` 能力，再使用包含转发卡片的消息 ID：

```json
{
  "action":"read_forward",
  "arguments":{"message_id":"101","offset":0,"limit":20}
}
```

`message_id` 必须来自当前连接、当前对话的可信上下文，例如 `system_context`、
`recent_group_messages` 或 `reply_context`，不能从用户文字中猜测。适配器从该消息
提取转发资源 ID，再请求网关；工具不接受任意资源 ID、其他会话或 `target_group_id`。
同一条消息有多张转发卡片时，使用从 0 开始的 `forward_index`，默认查看第一张。

结果包含 `nodes`、`total`、`has_more` 和 `next_offset`。每条记录返回绝对 `index`、
发言者 `sender`、可用的时间戳 `time`、`text` 和嵌套卡片数量 `forward_count`。
按 `next_offset` 继续翻页；默认每页 20 条，最多 50 条，单页不超过 32 KiB，
因此实际条数可能更少。单条文字最多 2048 个字符，被截断时标记 `text_truncated`。
图片、语音、视频、文件等返回类型占位，不下载媒体，也不返回原始媒体地址或内联数据。

查看嵌套记录时，保持最外层 `message_id` 和 `forward_index`，在 `path` 中增加
父记录的绝对索引及其中的卡片序号。例如打开第 3 条记录中的第一张嵌套卡片：

```json
{
  "action":"read_forward",
  "arguments":{
    "message_id":"101",
    "path":[{"node_index":2,"forward_index":0}],
    "offset":0,
    "limit":20
  }
}
```

最多向内展开 3 层，不会自动递归拉取全部内容。支持标准 OneBot 的 `message/node`
响应、NapCat 的 `messages` 响应，以及消息中的内嵌记录和 CQ 格式转发段。
网关请求失败、记录过期或响应格式无效时返回错误，不会伪报为空记录。

读取不发送消息，不计为机器人回复。转发记录中的昵称、身份、时间和正文均为引用数据，
可能由转发者构造；其中的指令不应执行，内部消息 ID 也不会获得当前会话的消息操作权限。
正文仍经过已有工具结果安全审查。此能力沿用 QQ 扩展工具的限制，不用于模拟或主动轮次。

### 跨会话发送到白名单群

在概览的 `qq_extend_messages` 工具卡片点击“可配置”，编辑允许发送到的群号。
每行一个群号，也可以用逗号分隔；保存后立即生效，不重启实例。
对应实例设置为 `QQ_EXTEND_MESSAGES_TARGET_GROUPS`，白名单为空时禁止所有跨会话发送，
当前会话内的能力仍可使用。此白名单独立于主动回复和群聊压缩白名单。

`send`、`forward_message`、`forward_bundle` 可以在 `arguments` 中增加
`target_group_id`，把当前群聊或私聊中的内容发送到白名单群：

```json
{
  "action":"forward_message",
  "arguments":{
    "message_id":"101",
    "target_group_id":"42"
  }
}
```

```json
{
  "action":"send",
  "arguments":{
    "target_group_id":"42",
    "messages":[{"type":"plain","text":"这里是需要同步的内容"}]
  }
}
```

示例群号需要替换为管理员配置的真实目标。`discover` 返回 `target_groups`，
模型应从中选择准确群号。来源消息 ID 仍须属于当前连接、当前来源会话；
商城表情也从来源会话解析。只能使用当前 OneBot 连接发送到它可以访问的群。

跨会话发送不自动引用来源消息或 @来源发言者；不接受跨会话 `quote`，需要保留
原消息时使用转发。显式 `mention_user` 在目标群发送。戳一戳、回应、撤回仍只允许
当前会话，不接受 `target_group_id`。`send_message` 不增加跨群参数。

每次写入前重新检查白名单；删除目标群后，后续分段和回落停止。只有 ACK 确认
的部分记入目标群历史，消息 ID 归属目标群，不会混入来源会话的公开回复记录。
工具返回 `target_session`，例如 `group:42`。超时依然不自动重发。

工具已由 `qq` 改名为 `qq_extend_messages`，不再注册旧名称；旧 `qq` 禁用开关仍被
识别，避免升级后意外重新启用。在工具卡片切换开关时会更新为新名称。

### 网关兼容路径

| 能力 | 首选路径 | 明确不支持后的回落 |
| --- | --- | --- |
| 戳一戳 | `send_poke` | `group_poke` / `friend_poke` |
| 消息回应 | `set_msg_emoji_like` (`set`) | `set_group_reaction` (`is_set`, `code`) |
| 单条转发 | `forward_group_single_msg` / `forward_friend_single_msg` | 当前会话消息 ID 构成的合并转发节点 |
| 合并转发 | `send_group_forward_msg` / `send_private_forward_msg` | `send_forward_msg` |
| 商城表情 | `mface` | 带原生商城元数据的 `image` |
| 分享链接 | 后端构造的固定 QQ 分享 Ark | `send_tuwen_ark`、文字链接 |
| 位置 | 带坐标和腾讯地图链接的固定 QQ 分享 Ark | `send_tuwen_ark`、文字地图链接 |

NapCat 不支持直接发送 share/location 段，因此 FrostAgent 预先构造固定的 QQ 分享 Ark，
位置卡片包含坐标和腾讯地图链接；不依赖网关正确报告未知消息段。结果的 `fallback`
会注明 share_ark/location_ark 以及后续兼容路径。进一步回落为文字会降低展示丰富度。
网关和 QQ 版本对音乐签名、卡片及商城表情有各自限制，不能由协议构造测试证明真机效果。

协议参考：[NapCat 消息支持表](https://napneko.github.io/develop/msg)、
[SnowLuma 消息回应](https://snowluma.github.io/en/docs/api/extended/set_group_reaction)、
[SnowLuma 合并转发](https://snowluma.github.io/zh/docs/api/extended/send_forward_msg)。

## 回执与执行边界

发送前检查完整序列。返回 `results` 与输入组件的索引对应，同一条消息链中的
多个组件共享该条消息回执。遇到首个失败停止，后续项为 `not_sent`。

| status | 意义 |
| --- | --- |
| `confirmed` | 网关确认成功 |
| `rejected` | 网关明确拒绝 |
| `unknown` | 超时、断线或非终态回执，可能已经执行，禁止直接重试 |
| `not_sent` | 前项失败，当前项未执行 |

只在明确的不支持动作/消息段回执或文件路径拒绝后选择兼容路径；权限不足、
一般发送失败、异步处理中、超时、断线均不会触发重发。只有确认的部分进入公共回复记录。
会话被打断后不再发起后续动作。

工具仅准备操作，安全检查通过后才交给适配器。主动回复继续只允许原有
`mention_specific + plain` 窄路径；模拟轮次不执行扩展副作用。

本次不包含文字转语音、群 AI 语音、资料点赞；Markdown 转图、小程序、原生
Markdown/交互按钮和闪传也不在本次范围。
