# AstrBot 支持移除

FrostAgent 内核不再提供 AstrBot Go 适配器、WebSocket 端点、正向连接、设置项或网页模拟模式。独立 Python 插件无法再通过旧 `/instances/{id}/ws/astrbot` 地址连接此版本。QQ 接入使用 OneBot。

按 Maintainer 选择，历史 `aiocqhttp` / `astrbot` 平台前缀也不再作为 QQ/OneBot 别名：旧记忆、摘要、群目录、访问控制、模型路由和白名单不会自动关联到 OneBot。已规范化为 `qq` 或无平台前缀的记录不受影响。

不为这些旧前缀记录执行自动迁移或删除；旧 JSON `brain.json` 中的条目及含旧平台来源的合并归档保留原样，不因裸 `group_id` 回退迁入 QQ 群库。QQ 旧记忆的既有迁移继续执行。数据库内旧连接配置不再被运行时读取，也不再作为可管理的连接字段。需要继续关联的数据与配置须由维护者另行迁移。
