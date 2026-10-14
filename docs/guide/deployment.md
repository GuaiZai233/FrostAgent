# 部署与网络

## 单机部署

从固定工作目录运行 `app`，首次使用默认监听：

| 入口                  | 默认值               |
| --------------------- | -------------------- |
| HTTP 控制台与管理 API | `127.0.0.1:8080`     |
| 外部适配器 WebSocket  | `127.0.0.1:1234`     |
| 本地数据库            | `data/frostagent.db` |

在「系统设置 → Bot 服务端设置」中修改全局监听地址。全局设置保存后由控制面尝试自动应用；绑定失败时恢复旧值。改变管理地址后要从新地址重新打开页面。

## 远程访问与反向代理

管理页面拥有实例管理、模型凭据、数据库切换等权限。需要远程访问时，在可信网络或带认证的反向代理后提供管理入口。当前服务没有通用的内置登录页；某些接口的控制 Token 不等同于整个控制台的用户登录认证。

反向代理应转发控制台、API 和 WebSocket Upgrade。直接对话使用管理入口同源的 `/instances/<实例ID>/ws/<适配器>?mock=true`，因此管理反向代理也必须支持这个 WebSocket 路径。

对外使用 HTTPS 时，浏览器模拟连接需要同源 `wss://`；上游也按部署情况使用 `ws://` 或 `wss://`。不要在 HTTPS 页面连接不安全的 `ws://`。

## Origin 设置

- `HTTP_ALLOWED_ORIGINS`：管理 API 跨域来源，多个完整 Origin 用英文逗号分隔。
- `WS_ALLOWED_ORIGINS`：浏览器 WebSocket 来源白名单。
- Origin 是协议、主机与端口，例如 `https://bot.example.com`，不带路径。

同源控制台通常无需增加白名单。发生跨域问题时，检查页面实际 Origin、代理 Host 与转发路径，按实际来源配置。

## 分容器或多机接入

容器内的 `127.0.0.1` 指向容器自身。上游和 FrostAgent 分容器时，需要使用服务名或可路由地址，并在 FrostAgent 的监听配置中允许对应网络接口。

例如 AstrBot 容器可以使用：

```text
ws_url: ws://frostagent:1234/instances/<实例ID>/ws/astrbot
http_base_url: http://frostagent:8080
```

以上是地址示例。仓库未提供可直接使用的 Docker 镜像或 Compose 部署模板，容器化时需自行构建镜像、配置端口、外部认证与持久卷。`http_base_url` 用于获取贴图，不能用 WebSocket 地址代替。

## 服务进程与数据

可以用操作系统的服务管理器托管 `app`，设置固定工作目录、运行账号和异常重启策略。确保运行账号可写 `data/`，也能读取模型 Secret 文件、示例对话模板和需要启动的 MCP 程序。

使用 PostgreSQL 时仍要保存本地 `data/frostagent.db` 中的数据库选择。当前实现为同一个 PostgreSQL 数据库加进程租约，不支持多进程同时写同一库。数据库和应用备份方式见[数据库与备份](../operations/storage)。
