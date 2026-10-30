<h1 align="center">
  <img src="assets/logo.png" alt="FrostAgent Logo" width="150">
  <br>
  FrostAgent
</h1>

<p align="center">
FrostAgent 是一个基于 Golang 编写的 AI 角色扮演、智能体调度框架，支持适配 Onebot 等多种协议，可以接入即时通信软件使用。
</p>

<p align="center">
  <a href="README.md">English</a> |
  <a href="README_zh_CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://go.dev">
    <img src="https://img.shields.io/badge/Go-1.27.0+-blue.svg" alt="Go Version">
  </a>
  <a href="https://github.com/GuaiZai233/FrostAgent/actions">
    <img src="https://img.shields.io/badge/CI-Passing-brightgreen.svg" alt="CI Status">
  </a>
  <a href="LICENCE">
    <img src="https://img.shields.io/badge/License-MPL%202.0-orange.svg" alt="License">
  </a>
  <a href="https://pkg.go.dev/github.com/GuaiZai233/FrostAgent"><img src="https://pkg.go.dev/badge/github.com/GuaiZai233/FrostAgent.svg" alt="Go Reference"></a>
  <a href="https://codecov.io/gh/GuaiZai233/FrostAgent"><img src="https://codecov.io/gh/GuaiZai233/FrostAgent/branch/main/graph/badge.svg" alt="测试覆盖率"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/GuaiZai233/FrostAgent"><img src="https://api.scorecard.dev/projects/github.com/GuaiZai233/FrostAgent/badge" alt="OpenSSF Scorecard 安全评分"></a>
  </a>
</p>

## 快速开始

### 1. 构建项目

本项目使用根目录 `Makefile` 进行构建编排。本地开发与验证命令见[开发指南](docs/development.md)。

```bash
pnpm install
go install github.com/bufbuild/buf/cmd/buf@v1.75.0
make build
```

CI Actions 与 Docker 基础镜像已固定到不可变引用，并由 Dependabot 每周提交更新 PR。
请定期审查并合并更新，避免固定版本长期失修；Go 工具版本及更新验证方式见
[依赖维护说明](.github/dependency-maintenance.md)。

### 2. 选择数据库

默认使用 SQLite，首次启动会创建 `data/frostagent.db`，也可选择 PostgreSQL。切换时不自动迁移数据；需要迁移时请手动备份还原。同一个 PostgreSQL 数据库只允许一个 FrostAgent 进程使用。

### 3. 启动服务

```bash
go run ./cmd/app
```

打开 `http://localhost:8080`。首次启动不创建实例，需手动创建。

Linux 若限制非 root 进程绑定低于 1024 的端口，请先构建，再执行 `sudo setcap cap_net_bind_service=+ep ./bin/app`，使用 `./bin/app` 代替 `go run ./cmd/app` 启动。重新构建后需重新授权；Docker 镜像已配置此能力，仍以非 root 用户运行。

## Docker 部署

```
docker compose up -d --build
```

GHCR 保留所有版本号镜像、当前 `main` / `latest` 镜像，以及最近 10 个带 SHA 标签的提交镜像；
更早的历史提交镜像会自动清理。清理预览、依赖保留和恢复说明见[镜像保留策略](.github/ghcr-retention.md)。


## 适配器

### WebSocket 连接模式

创建实例后，在侧边栏「概览」下方的「连接」页配置 OneBot 和 AstrBot；原概览页的两个地址复制入口已移至此页。两种适配器分别选择模式，默认保持已有的反向连接：

- **反向 WebSocket（FA 作为服务端）**：上游主动连接 FA。在 NapCat 等 OneBot 实现中启用反向 WebSocket 客户端，将「连接」页复制的 OneBot 地址填入，例如 `ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot`。地址使用实际适配器监听端口，容器部署时需根据端口映射调整可达主机和端口。
- **正向 WebSocket（FA 作为客户端）**：上游提供 WebSocket 服务端。在 FA「连接」页填写上游的 `ws://` 或 `wss://` 地址及可选访问令牌；FA 通过 `Authorization: Bearer <令牌>` 发起连接，并在断线后自动重试。不要在 URL 中填写用户名、密码或令牌。

保存仅影响当前实例，自动应用并断开旧连接；停用实例会停止拨号和重连。正向模式不会同时接受该适配器的普通反向连接，网页「直接对话」仍可使用。两种模式建立连接后均为双向收发；不要将同一上游重复接入多个实例，否则可能重复回复。

Docker 内的 `127.0.0.1` 指容器自身，不是 Windows 宿主机。正向连接宿主机服务时，Docker Desktop 可使用 `host.docker.internal`，但上游监听与防火墙仍必须允许容器访问；同一 Compose 网络中的服务可使用服务名。远程连接应使用 `wss://` 和访问令牌；正向／反向只改变适配器的连接方向，**不会改变 HTTP 管理面板的监听与认证设置**。

### 接入 AstrBot 等框架

在使用适配器的情况下，FrostAgent 可以替代 AstrBot 等智能体框架的 LLM 响应模块，同时不影响其丰富的插件生态。

可以使用此 AstrBot 插件：[astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) 进行连接。反向模式下，把 FA「连接」页复制的 AstrBot 地址填入插件的 `ws_url`。正向模式需要插件 0.2.0 或更新版本：将插件 `ws_mode` 设为 `forward`，配置监听地址／端口及令牌，再将插件的可达地址（例如 `ws://插件主机:1244/`）和相同令牌填入 FA。插件默认只监听本机；非回环监听必须配置令牌，且同一插件只接受一个活跃 FA 连接。配置好 AstrBot 和上游的通信即可。注意：请关闭 AstrBot 自带的 LLM 功能！

## 附加功能
### 按群配置主动回复概率

在「Bot 服务端设置 → 主动回复」中，点击已添加的群聊名称即可单独输入触发概率，群名后的小方框会显示已配置的概率。弹框确认后，点击页面上方「保存」生效；可在弹框内「恢复统一概率」。

负数或非法输入自动设为 `0.01`，大于 `1` 自动设为 `1`，`0` 表示该群不触发主动回复。未单独配置的群沿用统一概率，总开关和白名单限制仍然适用；正常被唤醒的回复不受单群概率影响。移除群聊时会一并移除其概率覆盖。

### 与 ActionsCat 协同

[ActionsCat](https://github.com/actionscat/actionscat) 支持静态编排自动化工作流。

在 ActionsCat 接入适配器后，您可以二者并行，智能体将发挥优秀的能力。

### 与 Alcyone 协同

[Alcyone](https://github.com/GuaiZai233/Alcyone) 是 GuaiTech 编写的计费系统后端实现，帮助 Bot 接入公共场景（如群聊）时统计计费，以量化 Token 等资源消耗。

### 与 Code-interpreter 协同
[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) 是一个精简的沙箱运行时，支持 Bot 运行简单代码。

## 许可证

MPL-2.0（见 [LICENCE](LICENCE)）
