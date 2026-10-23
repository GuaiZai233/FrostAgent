# FrostAgent

远程管理 API 必须提供有效控制令牌，覆盖设置、备份、日志、实例操作和数据库切换。
先在本机的 Bot 服务端设置中配置“管理控制令牌”，再在网页“前端设置”中填写相同值。
`MCP_CONTROL_TOKEN` 优先于兼容的 `ADMIN_TOKEN`，`ALLOW_REMOTE_MCP_MANAGEMENT` 无法绕过管理入口鉴权。
本机访问默认免令牌；启用 `MCP_ENFORCE_LOCAL_TOKEN` 后本机也必须认证。
反向代理应保留原 Host 或转发标识。如果代理把 Host 改成本机地址并清除全部转发标识，必须启用本机强制令牌，避免远程请求被识别成本机请求。

FrostAgent 是一个基于 Golang 编写的 AI 角色扮演、智能体调度框架，支持适配 Onebot 等多种协议，可以接入即时通信软件使用。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENCE)

## 版本发布

1.0.0 按源码版本准备。`package.json` 中的版本号不代表已经发布，实际可用版本以 [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases) 为准。发布后可下载源码归档或检出 `v1.0.0` tag，再在本地构建。预编译程序和镜像仓库分发延后；仓库中的 Dockerfile、Compose 继续从源码构建。

参见 [1.0.0 发布说明](docs/releases/v1.0.0.md)和[源码安装、升级及维护者发布指南](docs/releasing.md)。三个私有 workspace 包与项目版本保持一致，AstrBot 适配器继续使用独立插件版本。

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

本项目使用根目录 `Makefile` 进行构建编排。本地开发与验证命令见[开发指南](docs/development.md)。

```bash
# 安装 Node.js 依赖（Vite 与 TypeScript）
# 本项目使用 pnpm 作为包管理器
pnpm install --frozen-lockfile

# 安装 buf 用于 protobuf 代码生成
go install github.com/bufbuild/buf/cmd/buf@latest

# 构建全部 - 后端 Go 二进制文件 + 前端 Vite 应用
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

**破坏性更新：**旧版 JSON 和 `.env` 运行数据不再加载，也不会自动迁移；替换旧版本前请自行备份。SQL Schema 从 v7 基线开始通过顺序、无损的 Migration 升级；新版程序创建的更高版本数据库或低于 v7 的库会原样保留并拒绝加载。在「设置 > 备份与还原」可下载带版本号的实例全量 ZIP、`setting.json` 和 `memory.json`。导出时密钥及凭据来源置空；全量还原会创建一个默认停用的新实例，启用前需重新配置凭据和适配器。

### 3. 启动服务

```bash
go run ./cmd/app
```

打开 `http://localhost:8080`。首次启动不创建实例；在侧边栏底部的「实例管理」中创建并选择实例，在“模型供应商”中统一配置 Provider 和模型，再在“实例模型路由”中选择当前 Bot 的模型，最后打开概览中的「是否启用」。设置、记忆、模型路由、MCP、示例对话、贴图元数据和群摘要存入 SQL；贴图图片字节仍在实例文件目录中。设置修改后自动应用，无需手动重启进程。

## Docker 部署

```bash
docker compose up -d --build
```

使用 host 网络，适用于 Linux；Docker Desktop 需先启用 host networking。新数据库默认管理地址为 `http://localhost:8080`，适配器监听 `127.0.0.1:1234`。已有 SQL 监听设置优先，从桥接部署升级前请检查监听地址，数据仍保存在 `frostagent-data` 卷中。
建议前置 **Cloudflare Access** 负责管理认证；远程请求仍需应用管理控制令牌。

## Bot 设置与人格

Bot 名称、唤醒别名和系统提示词在「人格设定」中配置。Bot 服务端设置使用中文表单；修改后会出现顶部红色提示，点击「保存」统一应用，点击「还原」或离开页面丢弃未保存更改。额外 env 注入项也在保存时写入，重启 FrostAgent 后生效。

旧的 `MAX_CONTEXT_CHARS`、`UPSTREAM_API_KEY`、`CODER_API_KEY`、`ALLOW_REMOTE_MCP_MANAGEMENT` 和适配器选择开关不再作为常规设置提供。模型凭据在「模型路由器」的端点中设置，Coder 使用子智能体模型绑定。计费价格填非负整数最小单位（100 单位＝1 雪花＝0.01 元），安全倍率必须大于 0。默认值以输入框内的灰色占位显示，留空保存就使用默认值或继承值。

## 共享模型供应商与实例路由

“模型供应商”无需选中实例即可使用。Provider 的地址、凭据、启停状态和模型目录在当前数据库内共享；每个实例独立保存七类任务的默认模型与群覆盖。修改 Provider 后所有实例的新请求即时使用新配置，已开始的对话保留其地址与凭据快照。删除实例不会删除共享供应商；删除仍被实例路由引用的模型会被拒绝。

Schema v11 使用 `provider_endpoints`、`provider_models`、`provider_secrets`、`provider_revision` 保存共享目录，`model_bindings` 与 `model_revisions` 继续按实例隔离。v7–v10 升级在事务内复制 Provider、重映射冲突 ID/名称和路由，旧表保留为不再使用的迁移存档；新建数据库直接使用新结构。若人工改库导致重复的 Windows 凭据 Endpoint ID，则拒绝升级并保留原版本与数据，不猜测或覆盖系统凭据。

实例备份只携带该实例引用的供应商和模型定义，密钥及凭据来源置空。还原或复制时，匹配的共享定义保留已有凭据；相同 ID 的定义有冲突时创建新 ID，更新导入实例的路由，并要求补填密钥。其他实例的策略和共享凭据不被改写。

## Embedding 记忆检索

在模型供应商中配置支持 OpenAI-compatible `/embeddings` 的模型，标记 `embedding` 能力，并在实例模型路由中绑定 Embedding；群可以继承或单独指定模型。然后在 Bot 服务端设置的“记忆设置”开启“启用Embedding”。原有标签、摘要提取和反思保留，检索改用向量；消息窗口处理后更新，首次启用或索引失效时后台重建。工具搜索支持自然语言 `query` 或 `tags`，优先 `query`；网页继续分页。

SQLite 向量仅保留在内存中，重启后重建。PostgreSQL 需要部署者预先安装 pgvector，并在目标数据库执行 `CREATE EXTENSION vector`；缺少扩展时不能开启。关闭功能恢复关键词检索并保留 PostgreSQL 向量。导出及 ZIP 不携带向量。

模型的“最大输入 Tokens”可选；使用保守 UTF-8 字节预算截断请求副本。未设置时，明确超限的输入跳过本次处理，完整原文仍保存。Schema v12 增加输入限额与摘要待刷新标记；详见[记忆设计文档](.frostagent/whitepaper/memory.md)。

## 许可证

MPL-2.0（见 [LICENCE](LICENCE)）

## Skills 技能系统

选择实例后，在 **Skills → 导入 ZIP** 上传 Claude Code、Gemini CLI 等 harness 的 SKILL.md 技能包，支持单个技能、多个技能及仓库外层目录。导入后默认禁用，启用后由 Agent 按需加载，也可使用 `/skill-name 参数` 手动调用。随包脚本仅通过已启用的隔离沙箱执行。支持范围、大小限制和专属字段兼容提示见 [Skills 文档](.frostagent/whitepaper/skills.md)。
