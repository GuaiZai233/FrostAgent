# FrostAgent

FrostAgent 是一个基于 Golang 编写的 AI 角色扮演、智能体调度框架，支持适配 Onebot 等多种协议，可以接入即时通信软件使用。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

# 适配器

## Websocket

在本地上游启用一个反向 Websocket 客户端，URL 填 `ws://127.0.0.1:1234/ws/frostagent` (端口取决于环境变量中的`WS_LISTEN_ADDR`)。

## 与 ActionsCat 协同

[ActionsCat](https://github.com/actionscat/actionscat) 支持静态编排自动化工作流。

在 ActionsCat 接入适配器后，您可以二者并行，智能体将发挥优秀的能力。

注意，由于 ActionsCat 项目暂缓维护，您可以选择其他方案实现 Bot 的插件生态。

## 与 AstrBot 等框架协同

在使用适配器的情况下，FrostAgent 可以替代 AstrBot 等智能体框架的 LLM 响应模块，同时不影响其丰富的插件生态。

可以使用此 AstrBot 插件：[astrbot_plugin_frostagent](adapters\astrbot_plugin_frostagent) 进行连接。FrostAgent 默认地址为 `ws://127.0.0.1:1234/ws/astrbot`。配置好 AstrBot 和上游的通信即可。之后，FrostAgent 就可以接管消息了。注意：请关闭 AstrBot 自带的 LLM 功能！

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

### 2. 配置环境变量

创建 `.env` 文件或在系统环境变量中设置相关字段。

管理面与适配器 WebSocket 默认只监听 `127.0.0.1`。若将任一监听地址改为
非回环地址，必须同时配置对应访问 Token：

- `ADMIN_API_TOKEN` 保护管理页面与 ConnectRPC API。浏览器访问时会弹出
  HTTP Basic 登录框，用户名可任意填写，密码填写该 Token；API 客户端也可发送
  `Authorization: Bearer <token>`。
- `WS_ACCESS_TOKEN` 保护 OneBot 与 AstrBot WebSocket，连接方必须在升级请求中发送
  `Authorization: Bearer <token>`。
- `HTTP_ALLOWED_ORIGINS` 可按英文逗号分隔配置允许跨域访问管理面的精确 Origin；
  同源访问始终允许。

若需要从其他机器访问，请同时使用 TLS 或带鉴权的反向代理。

### 3. 启动服务

```bash
go run ./cmd/app
```

## 许可证

MPL-2.0 (see LICENSE file)
