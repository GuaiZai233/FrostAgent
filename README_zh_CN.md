# FrostAgent

FrostAgent 是用 Go 编写的 AI 角色扮演与智能体调度框架，支持 OneBot v11 和 AstrBot 接入，通过 Web 控制台管理多实例、人格、模型路由、记忆与工具。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml/badge.svg)](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENSE)

## 文档

完整中文文档位于 [`docs/`](docs/index.md)，包含：

- [项目介绍](docs/guide/introduction.md)、[快速开始](docs/guide/quick-start.md)、[安装与构建](docs/guide/installation.md)、[部署与网络](docs/guide/deployment.md)。
- [OneBot](docs/adapters/onebot.md) / [AstrBot](docs/adapters/astrbot.md) 接入，以及模型、人格、群聊、记忆、贴图与管理员指令。
- [配置参考](docs/reference/configuration.md)、[数据库与备份](docs/operations/storage.md)、[安全控制](docs/operations/security.md)和[故障排查](docs/operations/troubleshooting.md)。

本地查看 VitePress 文档站：

```bash
pnpm install --frozen-lockfile
pnpm docs:dev
```

构建与预览使用 `pnpm docs:build`、`pnpm docs:preview`。文档站与运行中的 Bot 控制台分别构建。

## 快速开始

准备 Go ≥ 1.25.3、Node.js 22+、pnpm 10.33.0、Buf、Make 与 Bash：

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make build
go run ./cmd/app
```

Windows 无 Make / Bash 的构建步骤见[安装与构建](docs/guide/installation.md)。

打开 `http://127.0.0.1:8080`，创建并选择实例，在「模型路由器」添加供应商、模型和主对话绑定，发布后在「概览」启用实例。可以先通过「直接对话」验证，再连接真实平台。

当前配置和持久化业务数据由 SQL 管理，默认使用 `data/frostagent.db`，可在控制台选择 PostgreSQL。无需复制 `.env.example`；旧 `.env` 和 JSON 目录不会自动迁移，参见[升级说明](docs/operations/upgrading.md)。模型供应商和密钥由实例共享，人格、路由、记忆、会话与 MCP 配置属于实例。

## 适配器

从实例概览复制地址，上游启用反向 WebSocket 客户端：

```text
ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot
ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot
```

AstrBot 使用仓库的 [专用插件](adapters/astrbot_plugin_frostagent)，并关闭 AstrBot 自带 LLM 回复。分容器时替换为可达主机，同时配置贴图 HTTP 根地址。

## 开发与许可

开发入口见 [Development.md](Development.md) 和[完整开发指南](docs/development/index.md)。控制台采用 Vite + 原生 TypeScript，构建后嵌入 Go 主程序。

基于 [MPL-2.0](LICENSE) 许可证发布。
