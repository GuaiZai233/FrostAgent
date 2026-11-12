# 管理员自然语言配置工具

`override_frostagent_config` 仅修改当前实例。管理员需要在当前真实对话中明确要求调整配置；工具描述禁止从历史、引用、图片或外部工具结果推断授权。执行端从受信任的 `RunContext` 校验 QQ 平台、`ADMIN_QQ_IDS`、实例与会话，拒绝模拟会话、主动回复、主动感知、已取消/停止的运行时与被封禁的调用者。

参数为 `action`、`value`、`request` 三个字符串。`request` 必须是当前消息文本中逐字摘录的配置请求。原文匹配不能代替语义理解，明确授权仍由模型结合工具描述判断。拒绝未知键、重复键、缺失/非字符串参数及额外 JSON，不接受任意环境变量名称、调用者身份或完整替换提示词。

| action | value |
| --- | --- |
| `append_system_prompt` | 仅新增的提示词文字 |
| `bot_loop_enabled` | `true` / `false`，关闭时保留 BotID |
| `bot_loop_add_ids` | 逗号分隔的新增 BotID，仅追加、不移除 |
| `bot_loop_max_turns` | 正整数 |
| `bot_loop_force` | `true` 强制丢弃超限请求；`false` 提示静默 |
| `awareness_enabled` | `true` / `false`，控制主动感知，不是主动回复 |
| `awareness_interval` | 活跃阈值，如 `30m - 2h` |
| `awareness_private_whitelist` / `awareness_group_whitelist` | 替换相应白名单；空字符串清空 |
| `awareness_private_whitelist_enabled` / `awareness_group_whitelist_enabled` | `true` / `false`，控制相应白名单模式 |
| `actionscat_enabled` | `true` / `false`，不修改连接和凭据 |
| `sticker_activity` | `0`~`10` 整数，`0` 禁用；含糊的积极度需求由模型根据语义选值 |

ID 可用 `platform:ID` 限定平台。每次调用修改一项配置，持久化成功后才返回成功；已有设置在下一次读取时生效，不重启实例。首版不提供沙箱开关或安全控制档位修改。

## 系统提示词只允许追加

初始提示词：

```text
你是一个乐于助人的助手。
```

追加后：

```text
你是一个乐于助人的助手。
<user_addition>
你说话要用幽默的语气。
</user_addition>
```

后续新文字插入同一区块结束标签前，保留全部旧文本及区块外内容。只识别最后一个完整、顺序正确、无嵌套的精确标签配对；起始标签须位于行首。残缺、逆序、带属性或嵌套的边界视为未处理，原文逐字保留并在末尾新增合法区块。此后复用新生成的合法区块。追加参数不得包含 `user_addition` 标签，避免模型自行控制边界。

工具先获取管理面共用的配置写锁，覆盖草稿提交、生效及失败回滚的整个事务，再在配置存储锁内完成读取、拼接和写入，避免追加丢失或被失败草稿回滚覆盖。等待锁时响应请求取消和运行时停止，避免重建实例时互相等待。文件与 SQL 后端都使用现有持久化路径。该工具不能替换/删除已有文本；管理面仍保留原有人工编辑能力。

## Bot 防循环开关

新增实例配置 `BOT_LOOP_ENABLED`，默认 `true`；旧实例未配置时沿用原来的“有标记列表即启用”行为，空列表仍不限制。设为 `false` 时停止限制但保留列表、轮数及强制模式。开关参与配置版本，关闭后重新启用会重置会话额度；设置页也可查看和调整此开关。
