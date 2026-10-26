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
go install github.com/bufbuild/buf/cmd/buf@latest
make build
```

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


## 适配器

### Websocket

在本地上游 OneBot 实现（如 NapCat）启用一个反向 Websocket 客户端，URL 填 `ws://127.0.0.1:1023/instances/<实例ID>/ws/onebot`（端口取决于设置页面中的监听地址）。

### 接入 AstrBot 等框架

在使用适配器的情况下，FrostAgent 可以替代 AstrBot 等智能体框架的 LLM 响应模块，同时不影响其丰富的插件生态。

可以使用此 AstrBot 插件：[astrbot_plugin_frostagent](adapters\astrbot_plugin_frostagent) 进行连接。FrostAgent 默认地址为 `ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot`。配置好 AstrBot 和上游的通信即可。之后，FrostAgent 就可以接管消息了。注意：请关闭 AstrBot 自带的 LLM 功能！

## 附加功能
### 与 ActionsCat 协同

[ActionsCat](https://github.com/actionscat/actionscat) 支持静态编排自动化工作流。

在 ActionsCat 接入适配器后，您可以二者并行，智能体将发挥优秀的能力。

### 与 Alcyone 协同

[Alcyone](https://github.com/GuaiZai233/Alcyone) 是 GuaiTech 编写的计费系统后端实现，帮助 Bot 接入公共场景（如群聊）时统计计费，以量化 Token 等资源消耗。

### 与 Code-interpreter 协同
[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) 是一个精简的沙箱运行时，支持 Bot 运行简单代码。

## 许可证

MPL-2.0（见 [LICENCE](LICENCE)）
