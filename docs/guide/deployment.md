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

管理页面拥有实例管理、模型凭据、数据库切换等权限。普通构建的远程管理 API / RPC 统一要求有效控制令牌，包括设置、备份、日志、实例操作和数据库切换。先在本机「Bot 服务端设置」配置「管理控制令牌」，再在远程网页「前端设置」填写相同值。`MCP_CONTROL_TOKEN` 优先于兼容的 `ADMIN_TOKEN`；`ALLOW_REMOTE_MCP_MANAGEMENT` 不能绕过统一鉴权。

本机默认免令牌；开启 `MCP_ENFORCE_LOCAL_TOKEN` 后本机也要认证。反向代理应保留公共 Host 或转发标识；如果代理把 Host 改成本机地址且清除全部转发标识，必须开启本机强制令牌。不要通过改写 Host 把远程请求伪装成本机访问。完整说明见[管理面认证](../operations/security#管理面认证)。

仓库提供的容器构建采用回环网络隔离，并由 Cloudflare Access 负责远程认证，接入方式与普通构建不同，见[Docker 与 Cloudflare](./docker)。

反向代理应转发控制台、API 和 WebSocket Upgrade。直接对话使用管理入口同源的 `/instances/<实例ID>/ws/<适配器>?mock=true`，因此管理反向代理也必须支持这个 WebSocket 路径。

对外使用 HTTPS 时，浏览器模拟连接需要同源 `wss://`；上游也按部署情况使用 `ws://` 或 `wss://`。不要在 HTTPS 页面连接不安全的 `ws://`。

## Origin 设置

- `HTTP_ALLOWED_ORIGINS`：管理 API 跨域来源，多个完整 Origin 用英文逗号分隔。
- `WS_ALLOWED_ORIGINS`：浏览器 WebSocket 来源白名单。
- Origin 是协议、主机与端口，例如 `https://bot.example.com`，不带路径。

本机同源控制台通常无需增加白名单。通过公开域名或代理访问时，先将完整的公开地址加入 `HTTP_ALLOWED_ORIGINS`，再启用路由。发生跨域问题时，检查页面实际 Origin、代理 Host 与转发路径，按实际来源配置。Origin 白名单不代替访问认证。

## 分容器或多机接入

容器内的 `127.0.0.1` 指向容器自身。上游和 FrostAgent 分容器时，需要使用服务名或可路由地址，并在 FrostAgent 的监听配置中允许对应网络接口。

普通构建中，若管理监听及安全网络策略允许相应访问，AstrBot 容器可以使用：

```text
ws_url: ws://frostagent:1234/instances/<实例ID>/ws/astrbot
http_base_url: http://frostagent:8080
```

`http_base_url` 用于获取贴图，不能用 WebSocket 地址代替。仓库的默认 Compose 仅发布适配器端口，**不发布管理端口**，因此上面的 HTTP 地址不适用于默认 Compose。容器部署需按[Docker 部署指南](./docker)选择网络拓扑，贴图 HTTP 根地址也必须使用插件能访问且满足认证要求的入口。

## 服务进程与数据

可以用操作系统的服务管理器托管 `app`，设置固定工作目录、运行账号和异常重启策略。确保运行账号可写 `data/`，也能读取模型 Secret 文件、示例对话模板和需要启动的 MCP 程序。

使用 PostgreSQL 时仍要保存本地 `data/frostagent.db` 中的数据库选择。当前实现为同一个 PostgreSQL 数据库加进程租约，不支持多进程同时写同一库。数据库和应用备份方式见[数据库与备份](../operations/storage)。
