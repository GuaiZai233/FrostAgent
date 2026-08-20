# AstrBot 适配器插件 (astrbot_plugin_frostagent)

本插件为 [FrostAgent](https://github.com/GuaiZai233/FrostAgent) 的 AstrBot 专用适配器，基于双向 WebSocket 协议将 AstrBot 接收到的聊天消息无缝转发至 FrostAgent 智能体核心，并支持流式中间工具输出、群聊 running compact 滚动压缩与跨平台独立记忆反思。

## ✨ 核心特性

- **双向长连接**：基于 WebSocket 保持与 FrostAgent 的实时双向通信，支持自动断线重连与周期心跳保活。
- **中间工具流式推送**：大模型执行外部工具（如搜索、发图、子智能体协作）时，中间输出即时推送到聊天窗口。
- **群聊无感总结与记忆反思**：支持全量群聊背景消息摄入，保持 FrostAgent 的 running compact 和多智能体长期记忆沉淀。
- **多平台命名空间隔离**：自动为 AstrBot 平台会话加上平台前缀（如 `astrbot:group:<id>` / `astrbot:user:<id>`），避免多适配器会话混淆。

## 📦 安装方法

1. 将本目录 `adapters/astrbot_plugin_frostagent` 复制到 AstrBot 的 `data/plugins/` 目录下。
2. 在 AstrBot 插件管理页面启用 `frostagent_adapter`。

## ⚙️ 配置说明

在 AstrBot Web 控制台中配置插件参数：

| 参数项 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `ws_url` | FrostAgent WebSocket 地址 | `ws://127.0.0.1:1234/ws/astrbot` |
| `forward_all_group_messages` | 是否将所有群消息发送给 FrostAgent (用于群聊总结) | `true` |
| `heartbeat_interval` | 心跳保活间隔 (秒) | `30` |
| `reconnect_interval` | 异常断开重连间隔 (秒) | `5` |
