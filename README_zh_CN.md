# FrostAgent

FrostAgent 是一个基于 Golang 编写的 AI 角色扮演、智能体调度框架，支持适配 Onebot 等多种协议，可以接入即时通信软件使用。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

## 数据库

默认使用 `data/frostagent.db`。在“系统设置 > 数据库设置”中可以填写 PostgreSQL 连接地址并立即切换；切换只加载目标数据库已有的数据，不会自动迁移，实例可通过“备份与还原”手动搬迁。数据库选择保存在本地 SQLite 引导表中，连接地址为空时拒绝切换。

# 适配器

## Websocket

在本地上游启用一个反向 Websocket 客户端，URL 填 `ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot`（端口取决于设置页面中的监听地址）。

## 与 ActionsCat 协同

[ActionsCat](https://github.com/actionscat/actionscat) 支持静态编排自动化工作流。

在 ActionsCat 接入适配器后，您可以二者并行，智能体将发挥优秀的能力。

注意，由于 ActionsCat 项目暂缓维护，您可以选择其他方案实现 Bot 的插件生态。

## 与 AstrBot 等框架协同

在使用适配器的情况下，FrostAgent 可以替代 AstrBot 等智能体框架的 LLM 响应模块，同时不影响其丰富的插件生态。

可以使用此 AstrBot 插件：[astrbot_plugin_frostagent](adapters\astrbot_plugin_frostagent) 进行连接。FrostAgent 默认地址为 `ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot`。配置好 AstrBot 和上游的通信即可。之后，FrostAgent 就可以接管消息了。注意：请关闭 AstrBot 自带的 LLM 功能！

## 快速开始

### 1. 构建项目

本项目使用根目录 `Makefile` 进行构建编排。

```bash
# 安装 Node.js 依赖（Angular 工具链等）
# 本项目使用 pnpm 作为包管理器
pnpm install

# 安装 buf 用于 protobuf 代码生成
go install github.com/bufbuild/buf/cmd/buf@latest

# 构建全部 - 后端 Go 二进制文件 + 前端 Angular 应用
make build
```

编译后的后端二进制文件位于 `./bin/`，前端静态资源位于 `internal/frontend/dist/`。

也可以单独构建：

```bash
make build-api    # 构建后端和嵌入的前端
make build-web    # 仅构建前端
```

### 2. 选择数据库

默认使用 SQLite，首次启动会创建 `data/frostagent.db`。在「设置 > 数据库设置」可选择 PostgreSQL 并填写连接地址；空地址会被拒绝。数据库选择保存在本地 SQLite 引导表中，以便连接 PostgreSQL 前读取。切换时连接目标数据库并加载其中已有的设置与实例，不自动迁移数据；需要迁移时请手动使用实例备份与还原功能。同一个 PostgreSQL 数据库只允许一个 FrostAgent 进程使用。

**破坏性更新：**旧版 JSON 和 `.env` 运行数据不再加载，也不会自动迁移；替换旧版本前请自行备份。v1.0 之前，遇到不兼容的 SQL schema 版本可能自动重建并清空数据。在「设置 > 备份与还原」可下载带版本号的实例全量 ZIP、`setting.json` 和 `memory.json`。导出时密钥及凭据来源置空；全量还原会创建一个默认停用的新实例，启用前需重新配置凭据和适配器。

### 3. 启动服务

```bash
go run ./cmd/app
```

打开 `http://localhost:8080`。首次启动不创建实例；在侧边栏底部的「实例管理」中创建并选择实例，配置模型路由器，再打开概览中的「是否启用」。设置、记忆、模型路由、MCP、示例对话、贴图元数据和群摘要存入 SQL；贴图图片字节仍在实例文件目录中。设置修改后自动应用，无需手动重启进程。

## 许可证

MPL-2.0 (see LICENSE file)
