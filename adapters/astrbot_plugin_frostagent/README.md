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
