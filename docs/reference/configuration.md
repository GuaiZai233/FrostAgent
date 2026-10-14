# 配置参考

当前配置在 SQL 数据库中管理。「系统设置 → Bot 服务端设置」编辑通用设置，「人格设定」编辑系统提示词，「模型路由器」管理供应商与模型。下列大写键名是设置标识，不是要求写入操作系统环境变量。

## 配置范围与生效

| 范围          | 例子                         | 生效方式                             |
| ------------- | ---------------------------- | ------------------------------------ |
| 全局控制面    | 监听地址、沙箱连接、安全模式 | 保存后控制面尝试应用，失败按日志回滚 |
| 当前实例      | 人格、群策略、记忆与计费参数 | 保存后自动应用，必要时重建运行时     |
| 共享模型目录  | 供应商、密钥、模型           | 发布后影响全部引用它的实例           |
| 实例 / 群路由 | 工作负载绑定                 | 发布后作用于对应实例 / 群            |

时长写成 `30s`、`5m`、`10m`；列表通常用英文逗号。表中的默认值来自新实例模板或对应模块的默认行为；既有实例以数据库保存值为准。

## 全局监听与安全

| 键                            | 默认 / 用途                                           |
| ----------------------------- | ----------------------------------------------------- |
| `LISTEN_ADDR`                 | `127.0.0.1:8080`，HTTP 管理监听                       |
| `WS_LISTEN_ADDR`              | `127.0.0.1:1234`，外部适配器监听                      |
| `HTTP_ALLOWED_ORIGINS`        | 管理面允许的跨域完整 Origin 列表                      |
| `WS_ALLOWED_ORIGINS`          | WebSocket Origin 白名单                               |
| `SECURITY_CONTROL_MODE`       | `simple`；可选 `off` / `simple` / `aggressive`        |
| `MCP_CONTROL_TOKEN`           | MCP 和复用其鉴权的管理接口 Token                      |
| `ADMIN_TOKEN`                 | 上述 Token 的兼容来源，也可用于消息接口认证           |
| `ALLOW_REMOTE_MCP_MANAGEMENT` | 默认不放宽远程 MCP 管理                               |
| `MCP_ENFORCE_LOCAL_TOKEN`     | 默认不强制本机 Token；启用后本机也需认证              |
| `DEFAULT_DIALOGUE_TEMPLATE`   | 新实例示例对话模板；默认 `eval/dialogue/dialogue.yml` |

## 人格、上下文与工具循环（实例）

| 键                     | 默认 / 用途                                    |
| ---------------------- | ---------------------------------------------- |
| `SYSTEM_PROMPT`        | 新实例为「你是一个乐于助人的助手。」；支持多行 |
| `BOT_NAME`             | `霜降狐`，群聊文本唤醒主名称                   |
| `BOT_ALIASES`          | `霜降,FrostAgent`，群聊文本唤醒别名            |
| `ADMIN_QQ_IDS`         | 空，无管理员；配置真实平台管理员 ID            |
| `ADMIN_COMMAND_PREFIX` | `/`；单词前缀后需空白                          |
| `MAX_CONTEXT_MESSAGES` | 20，保留消息数量裁剪依据                       |
| `MAX_CONTEXT_CHARS`    | 24000，近似字符预算，非 Token 限额             |
| `AGENT_MAX_ITERATIONS` | 35，单次处理最大工具循环迭代数                 |

## 群聊策略（实例）

| 键                                 | 默认 / 用途                                |
| ---------------------------------- | ------------------------------------------ |
| `GROUP_REPLY_ON_MENTION`           | `true`；`false` 关闭当前群回复路径         |
| `ENABLE_AT_IN_GROUP_MSG`           | `true`，回复前 @发送者                     |
| `ENABLE_REPLY_IN_GROUP_MSG`        | `false`，回复时引用原消息                  |
| `ENABLE_QUOTE_TRACE`               | `true`，追溯引用链                         |
| `QUOTE_TRACE_DEPTH`                | 5，范围 1～10                              |
| `ENABLE_PROACTIVE_REPLY`           | 未开启时不主动回复                         |
| `PROACTIVE_REPLY_PROBABILITY`      | `0` 关闭；有效开启区间 `0.01`～`1.00`      |
| `ENABLE_PROACTIVE_REPLY_WHITELIST` | 开启时只允许白名单群主动回复               |
| `PROACTIVE_REPLY_GROUP_WHITELIST`  | 群号列表；开启白名单且列表空则不允许任何群 |
| `BOT_LOOP_USER_IDS`                | 空；可填写 ID 或 `platform:ID`             |
| `BOT_LOOP_MAX_TURNS`               | 3，标记 Bot 的连续对话轮数                 |
| `BOT_LOOP_MODE`                    | `soft` 注入静默建议；`hard` 超限直接静默   |
| `ONEBOT_ACTION_TIMEOUT`            | `10s`，OneBot 动作回执超时                 |

## 记忆与压缩（实例）

| 键                              | 默认 / 用途                                |
| ------------------------------- | ------------------------------------------ |
| `GROUP_COMPACT_BUFFER_SIZE`     | 20，每批群原消息数量                       |
| `GROUP_COMPACT_MAX_BUFFER_SIZE` | 未配置时由压缩模块推导上限；不要低于批大小 |
| `GROUP_COMPACT_MIN_INTERVAL`    | `30s`，同群最小压缩间隔                    |
| `GROUP_RAW_CONTEXT_MAX_CHARS`   | 12000，近期未压缩原消息临时预算            |
| `MEMORY_EXTRACT_BATCH_MIN`      | 3，自动提取最小累计轮数                    |
| `MEMORY_EXTRACT_BATCH_MAX`      | 5，自动提取最大累计轮数                    |
| `MEMORY_REFLECTION_TIMEOUT`     | `10m`，反思独立超时                        |

模型路由决定这些任务是否可执行；仅设置缓冲大小不会启用停用的工作负载。

## Embedding 记忆检索（实例）

本节适用于包含 [#194](https://github.com/GuaiZai233/FrostAgent/pull/194) 的版本；较早版本没有这些设置。

| 设置                         | 默认 / 用途                                                          |
| ---------------------------- | -------------------------------------------------------------------- |
| `ENABLE_EMBEDDING`           | 默认关闭；`true` 启用向量检索，`false` 恢复关键词检索                |
| 模型路由工作负载 `embedding` | 独立选择支持 `/embeddings` 的模型，不跟随主对话                      |
| 模型的 `embedding` 能力标签  | 标识模型用途；上游仍须实际支持 Embedding                             |
| 模型的「最大输入 Tokens」    | 可选非负整数，空 / 0 表示不设置；使用保守 UTF-8 字节预算截断请求副本 |

先发布可用的实例默认 Embedding 路由，再开启实例开关；群级可继承默认或选择独立模型。SQL 模式保存后自动应用。SQLite 向量仅在内存中，重启后重建；PostgreSQL 必须预先安装并启用 pgvector。普通 PostgreSQL 启动不要求扩展，开启 Embedding 时才校验。

关闭功能保留兼容的 PostgreSQL 向量；应用备份不包含向量。输入被上游明确拒绝为超长时跳过本次相关处理，不删除完整原文。详见[Embedding 使用指南](../usage/embedding)。

## 表情包（实例）

| 键                      | 默认 / 用途                      |
| ----------------------- | -------------------------------- |
| `STICKER_ACTIVITY`      | 0 禁用；1～10 对应间隔 50～5     |
| `STICKER_ACTIVITY_MODE` | `bot_turns`；可选 `all_messages` |

详见[表情包计数语义](../usage/stickers)。修改模式或积极度重置计数。

## 沙箱（全局）

| 键                          | 默认 / 用途                |
| --------------------------- | -------------------------- |
| `SANDBOX_ENABLED`           | `false`，允许运行时启停    |
| `SANDBOX_BASE_URL`          | `http://127.0.0.1:3874`    |
| `SANDBOX_AUTH_TOKEN`        | 网关 Token，启用时必填     |
| `SANDBOX_SESSION_NAMESPACE` | `frostagent`，基础命名空间 |

地址、凭据和命名空间作为控制面配置整体应用。配置方式见[隔离沙箱](../integrations/sandbox)。

## ActionsCat 与计费

| 键                                     | 范围 | 默认 / 用途                            |
| -------------------------------------- | ---- | -------------------------------------- |
| `ACTIONSCAT_ENDPOINT`                  | 实例 | ActionsCat 核心服务地址                |
| `ACTIONSCAT_MANAGEMENT_TOKEN`          | 实例 | 管理 API Token                         |
| `ACTIONSCAT_DISPATCH_TOKEN`            | 实例 | 留空复用管理 Token                     |
| `ALCYONE_BASE_URL`                     | 全局 | 启用计费时默认 `http://127.0.0.1:8081` |
| `ALCYONE_SERVICE_TOKEN`                | 全局 | Alcyone 通信 Token                     |
| `ALCYONE_TIMEOUT`                      | 全局 | `5s`                                   |
| `BILLING_ENABLED`                      | 实例 | `false`                                |
| `BILLING_MAX_OUTPUT_TOKENS`            | 实例 | 2048，预扣输出预算                     |
| `BILLING_SAFETY_MULTIPLIER`            | 实例 | 1.2，输入估算倍率                      |
| `BILLING_PROMPT_PRICE_PER_MILLION`     | 实例 | 输入单价，按实际约定设置               |
| `BILLING_COMPLETION_PRICE_PER_MILLION` | 实例 | 输出单价，按实际约定设置               |

当前 SQL 模式保存实例设置后自动应用，必要时重建运行时，无需额外手动重启。重建可能中断连接和在途任务；应用失败时恢复旧设置并报告错误。具体行为见对应集成章节。

## 安全网关时长（全局 / 实例）

`SECURITY_GATEWAY_TIMEOUT` 与兼容键 `SECURITY_CLASSIFIER_TIMEOUT` 支持全局和实例设置；实例非空值优先于全局，网关键优先于兼容键。未配置时使用分类器默认值。在当前 SQL 模式中保存后由运行时自动重新装配。

## 历史键与迁移边界

- `BRAIN_PATH`、`DIALOGUE_PATH`：当前 SQL 配置不允许用它们改运行时数据文件。
- `ONEBOT_WS_PATH`、`ASTRBOT_WS_PATH`：当前入口由实例路由固定。
- `ENABLE_ONEBOT_ADAPTER`、`ENABLE_ASTRBOT_ADAPTER`：遗留值不能关闭当前两个内置适配器。
- `UPSTREAM_API_KEY`、`CODER_API_KEY`：不能替代模型路由器中的供应商凭据配置。
- 根 `.env` 和进程环境变量：不作为当前应用设置来源；SQL 模型供应商也禁止 `env` 凭据来源。

部分旧键仍出现在底层兼容代码或设置注册表中，不代表它们仍控制当前运行路径。升级与数据转换见[升级说明](../operations/upgrading)。
