# API 入口

管理控制台使用 ConnectRPC 和少量 REST 入口。扩展客户端应以仓库 `api/proto/frostagent/v1/` 与当前服务实现为准，先区分全局控制面和实例路径。`gen/proto/frostagent/v1/` 是生成的 Go 代码，不是协议源码目录。

## 路由范围

| 入口                                               | 用途                                                       |
| -------------------------------------------------- | ---------------------------------------------------------- |
| `/api/instances`                                   | 实例列表与创建                                             |
| `/api/instances/restore`                           | `POST` 原始全量 ZIP 为新实例；可选查询参数 `name` 指定名称 |
| `/api/instances/global/extra-env`                  | 全局额外 env 注入项，保存后重启进程生效                    |
| `/api/instances/<ID>/...`                          | 实例启停、配置复制、备份与导入等生命周期操作               |
| `/api/storage`                                     | 读取 / 切换数据库，`GET` / `POST`                          |
| `/api/security/...`                                | 全局安全状态与审计                                         |
| `/instances/<ID>/frostagent.v1.<Service>/<Method>` | 实例 ConnectRPC 服务                                       |
| `/instances/<ID>/ws/onebot`                        | OneBot v11 WebSocket                                       |
| `/instances/<ID>/ws/astrbot`                       | AstrBot WebSocket                                          |
| `/instances/<ID>/api/v1/messages/send`             | 外部业务发送消息                                           |
| `/instances/<ID>/api/actionscat/...`               | ActionsCat 管理代理                                        |
| `/instances/<ID>/api/skills...`                    | Skills REST：列表、ZIP 导入、详情、启停和删除              |

ConnectRPC 服务包括 BotStatus、Settings、ModelRouter、Memory、Dialogue、Sticker、Log 与 MCP。Skills 使用 REST：`GET /api/skills` 列表、`POST /api/skills/import` 上传原始 ZIP，`/api/skills/items/<name>` 支持 GET 详情、POST `{"enabled": true}` 启停与 DELETE 删除；调用时加上实例路径前缀。

全局模型路由入口用于共享目录；业务调用优先使用明确实例路径，避免全局兼容入口选择默认实例。

普通构建远程管理调用统一携带 `Authorization: Bearer <管理控制令牌>`，包括生命周期、备份、存储、日志和 ConnectRPC。容器构建的 Cloudflare Access 拓扑、本机令牌例外及代理要求见[管理面认证](../operations/security#管理面认证)。

## 外部消息发送

该接口是实际发送动作，需要启用实例、有效目标和已连接适配器。可以在全局设置配置 `MCP_CONTROL_TOKEN` 或 `ADMIN_TOKEN`，作为当前接口的可用认证来源；当实例中存在更高优先级的消息专用 Token 时需使用对应值。

```http
POST /instances/<实例ID>/api/v1/messages/send
Authorization: Bearer <消息接口Token>
Content-Type: application/json
```

```json
{
  "platform": "qq",
  "message_type": "group",
  "target_id": "<群ID>",
  "content": "任务已完成。",
  "metadata": {
    "adapter_id": "astrbot"
  }
}
```

将 `<群ID>` 替换为实际目标；`adapter_id` 选择当前实例注册的 `onebot` 或 `astrbot`，避免 `qq` 平台兼容回退选择错误传输。私聊使用 `message_type: "private"` 和目标用户 ID。

请求也支持 `session`、消息段数组与附件；附件需要当前适配器支持的媒体类型和可用 URL，不能只填宿主机路径。显式传入 `instance_id` 时必须与路径绑定的实例一致。

成功响应示例：

```json
{ "status": "ok", "delivered": true, "count": 1 }
```

`count` 是发送消息对象数量。未配置认证返回 503，认证失败返回 401，参数不合法返回 400，投递失败返回 502。检查响应内容与适配器回执，不能只按 HTTP 连接成功判断消息送达。

## 客户端开发

先使用 Buf 生成协议，再用 ConnectRPC 客户端调用对应服务。当前控制台的 `apps/web/src/api/client.ts` 与 `instance-state.ts` 展示实例请求如何绑定。MCP / ActionsCat 管理接口另有控制面鉴权规则，参见 [MCP](../integrations/mcp)。

这些入口具有管理和发送权限，部署时纳入管理面的认证与网络边界。协议升级后重新生成客户端，不维护手写的过期 DTO 副本。
