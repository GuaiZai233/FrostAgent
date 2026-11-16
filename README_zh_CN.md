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
    <img src="https://img.shields.io/badge/Go-1.27.3+-blue.svg" alt="Go Version">
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

### 2. 选择数据库

默认使用 SQLite，首次启动会创建 `data/frostagent.db`，也可选择 PostgreSQL。切换时不自动迁移数据；需要迁移时请手动备份还原。同一个 PostgreSQL 数据库只允许一个 FrostAgent 进程使用。

### 3. 启动服务

```bash
go run ./cmd/app
```

打开 `http://localhost:8080`。首次启动不创建实例，需手动创建。

## Docker 部署

**首次部署先复制 `docker.env.example` 为 `.env`，将其中`FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN` 填为至少 32 位的随机 ASCII 令牌，再启动：**

```
docker compose up -d --build
```

打开 `http://127.0.0.1:8080/#/settings/credentials`（设置 → 访问凭据），在「API / 控制平面访问凭据」中
填写同一令牌并保存。

## 适配器

### WebSocket 连接模式

创建实例后，在「连接」页配置。

- **反向 WebSocket（FrostAgent 作为服务端）**：Endpoint 主动连接 FrostAgent。在消息平台 Endpoint 实现中启用反向 WebSocket 客户端，将复制的 OneBot 地址填入，例如 `ws://127.0.0.1:11023/instances/<实例ID>/ws/onebot`。
- **正向 WebSocket（FrostAgent 作为客户端）**：Endpoint 提供 WebSocket 服务端。在 FrostAgent「连接」页填写 Endpoint 的 `ws://` 或 `wss://` 地址及可选访问令牌。

## 附加功能

### 上下文自动压缩

群聊和私聊均支持后台 compact。私聊保留最近一轮原消息，并在后续对话中使用滚动摘要。配置与会话生命周期说明见[上下文自动压缩](docs/context-compact.md)。

### 与 ActionsCat 协同

[ActionsCat](https://github.com/actionscat/actionscat) 支持静态编排自动化工作流。

在 ActionsCat 接入适配器后，您可以二者并行，智能体将发挥优秀的能力。

### 与 Alcyone 协同

[Alcyone](https://github.com/GuaiZai233/Alcyone) 是 GuaiTech 编写的计费系统后端实现，帮助 Bot 接入公共场景（如群聊）时统计计费，以量化 Token 等资源消耗。

### 与 Code-interpreter 协同
[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) 是一个精简的沙箱运行时，基于 [Foxerine](https://github.com/Foxerine/code-interpreter) 而来，支持 Bot 运行简单 Shell Exec 等操作。

## 许可证

MPL-2.0（见 [LICENCE](LICENCE)）
