# WebSocket 连接存活检测

OneBot 与 AstrBot 服务端连接共用 `internal/adapter/heartbeat`。服务端每 30 秒发送 WebSocket Ping 控制帧，客户端应返回 Pong。控制帧发送最多等待 10 秒；发送失败或 Pong 读取预算耗尽后关闭连接，沿用适配器现有的取消与连接注销流程。心跳任务随连接或实例上下文关闭而退出。

Pong 超时预算为 90 秒，按服务端实际执行 WebSocket 读取的累计时间计算。处理一条消息期间的同步安全审查等耗时不计入预算，避免服务端暂停读取时把健康客户端误判为失活。每次 Pong 重置预算；普通消息和应用层 heartbeat JSON 不重置预算。

Ping 使用 Gorilla WebSocket 的并发安全 `WriteControl`，不与业务消息共用普通写入调用。回归测试通过本地真实 WebSocket 覆盖 Pong 保活、缺失 Pong（包括持续发送数据）、慢业务处理以及取消连接后的任务退出。真实 NapCat、LuckyLillia 与 AstrBot 部署仍需集成验证。
