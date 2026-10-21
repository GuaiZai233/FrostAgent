# MCP 服务器

MCP 让实例调用外部服务器提供的业务工具。每个实例独立配置服务器和工具开关；模型只能调用已经暴露的业务工具，不能通过对话添加或修改 MCP 服务器。

## 添加服务器

打开「MCP服务器」，选择新增，填写名称、传输方式与连接参数。支持可视化表单和 JSON 编辑，界面可识别 Claude Desktop 的 `mcpServers` 格式以及单服务配置。

| 传输            | 需要填写                               |
| --------------- | -------------------------------------- |
| Stdio           | 命令、独立参数数组、环境变量、工作目录 |
| Streamable HTTP | HTTP URL、所需请求头                   |
| SSE             | SSE URL、所需请求头                    |

Stdio 命令由管理员配置并在 FrostAgent 服务账号下启动。每个 argv 单独填写，不把整段命令行塞进一个参数。相对工作目录和文件路径按部署时的实际工作目录解析。

下面是单服务配置的结构示例；启用前将 URL 换成实际 MCP 服务：

```json
{
  "id": "example-tools",
  "name": "示例工具服务",
  "enabled": false,
  "transport": {
    "type": "streamable_http",
    "url": "https://mcp.example.com/mcp",
    "headers": {}
  },
  "tools": {}
}
```

## 同步与启停工具

启用服务器后检查连接状态和最后错误，按需重连或同步目录。工具面板显示完整名称和参数 JSON Schema，支持单工具开关。服务器停用、工具禁用或连接失效时，对应调用会报错；先确认目录，再让模型执行。

第三方 MCP 工具具有其自身的文件、网络或业务写入权限。只开放实例需要的工具，凭据和权限由对应服务控制。

## 管理入口权限

本机管理默认允许可信回环访问。普通构建的远程管理统一要求全局 `MCP_CONTROL_TOKEN`（未设置时兼容 `ADMIN_TOKEN`），客户端发送：

```http
Authorization: Bearer <控制Token>
```

网页在「系统设置 → 前端设置」保存相同令牌。`MCP_ENFORCE_LOCAL_TOKEN=true` 会让本机管理也需要 Token；旧 `ALLOW_REMOTE_MCP_MANAGEMENT` 不能绕过统一鉴权。容器的 Cloudflare Access 部署另有回环隔离要求，详见[管理面认证](../operations/security#管理面认证)。

无法操作时先区分连接服务失败和管理请求 401 / 403，再检查 Token、服务账号权限、工作目录与服务日志。
