# FrostAgent

除下文所述的隔离 Cloudflare Access 部署外，远程管理 API 必须提供有效控制令牌，覆盖设置、备份、日志、实例操作和数据库切换。
先在本机的 Bot 服务端设置中配置“管理控制令牌”，再在网页“前端设置”中填写相同值。
`MCP_CONTROL_TOKEN` 优先于兼容的 `ADMIN_TOKEN`，`ALLOW_REMOTE_MCP_MANAGEMENT` 无法绕过管理入口鉴权。
本机访问默认免令牌；启用 `MCP_ENFORCE_LOCAL_TOKEN` 后本机也必须认证。
反向代理应保留原 Host 或转发标识。如果代理把 Host 改成本机地址并清除全部转发标识，必须启用本机强制令牌，避免远程请求被识别成本机请求。

FrostAgent 是一个基于 Golang 编写的 AI 角色扮演、智能体调度框架，支持适配 Onebot 等多种协议，可以接入即时通信软件使用。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.0+-blue.svg)](https://go.dev)
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

**破坏性更新：**旧版 JSON 和 `.env` 运行数据不再加载，也不会自动迁移；替换旧版本前请自行备份。SQL Schema 从 v7 基线开始通过顺序、无损的 Migration 升级；新版程序创建的更高版本数据库或低于 v7 的库会原样保留并拒绝加载。在「设置 > 备份与还原」可下载带版本号的实例全量 ZIP、`setting.json` 和 `memory.json`。导出时密钥及凭据来源置空；全量还原会创建一个默认停用的新实例，启用前需重新配置凭据和适配器。

### 3. 启动服务

```bash
go run ./cmd/app
```

打开 `http://localhost:8080`。首次启动不创建实例；在侧边栏底部的「实例管理」中创建并选择实例，在“模型供应商”中统一配置 Provider 和模型，再在“实例模型路由”中选择当前 Bot 的模型，最后打开概览中的「是否启用」。设置、记忆、模型路由、MCP、示例对话、贴图元数据和群摘要存入 SQL；贴图图片字节仍在实例文件目录中。设置修改后自动应用，无需手动重启进程。

## Docker 部署

管理认证沿用 PR #165 的决定，由前置 **Cloudflare Access** 负责。部署不再包含 Caddy、Basic Auth 管理员账号或本地 CA。Tunnel 的运行位置由部署用户选择；Tunnel 连通本身不代表已经配置 Access。

### 本地测试或宿主机 cloudflared（原生 Linux）

单独使用 host 网络文件，不与桥接文件叠加：

```bash
docker compose -f compose.host.yaml up -d --build
```

新数据库默认可在 `http://127.0.0.1:8080` 打开控制台；适配器为 `ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot` 或 `/ws/astrbot`。该镜像将管理与适配器默认地址都设为回环；host 模式没有端口映射，隔离来自应用实际监听的 `127.0.0.1`。本地测试无需配置 Tunnel。

若 cloudflared 已运行在同一 Linux 宿主机，它直接回源到 `http://127.0.0.1:8080`。生产路径为：`浏览器 → Cloudflare Access → Tunnel → 宿主机 cloudflared → 回环管理监听`。先通过本地控制台把完整的公开地址（例如 `https://frostagent.example.com`）加入 `HTTP_ALLOWED_ORIGINS`，再启用公网路由。

此回环隔离方案针对原生 Linux Engine。Docker Desktop 的 host networking 需用户主动启用，且是不同的四层转发实现，不能据此保证 Windows/macOS 外部接口的暴露范围；本 PR 不验证 Desktop 宿主机转发。Windows/macOS 本地控制台可按上文使用原生构建。参见 [Docker host 网络说明](https://docs.docker.com/engine/network/drivers/host/)。

### 容器内 cloudflared（桥接网络）

默认 `compose.yaml` 只启动 FrostAgent：

```bash
docker compose up -d --build
```

管理只监听**容器内** `127.0.0.1:8080`，不发布管理端口，因此宿主机浏览器不能直接访问。适配器监听容器内 `0.0.0.0:1234`，仅映射到宿主机 `127.0.0.1:1234`；同一 Docker 网络的适配器可用 `ws://frostagent:1234/instances/<实例ID>/ws/onebot` 或 `/ws/astrbot`。1234 只提供适配器 WebSocket，管理页面、API 和 RPC 均不可通过此端口使用。

cloudflared 必须与 FrostAgent **共享网络命名空间**，而不只是加入同一桥接网络。若希望由此 Compose 运行已有的 Tunnel connector，将 `docker.env.example` 中的 `TUNNEL_TOKEN` 填入 `.env`，并使用可选文件：

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build
```

它只增加用户选择的 cloudflared，没有额外反向代理或认证组件。已在其他部署中运行的 connector 不必重复启动；可为其设置 `network_mode: container:<FrostAgent容器ID>`。FrostAgent 被重新创建后，connector 也须重新创建以加入新的命名空间；同一 Compose 中请始终带上两个文件执行更新，可选文件声明了此重建依赖。

在 Cloudflare 配置回源地址 `http://127.0.0.1:8080`，并在发布路由前为**整个管理域名的全部路径**配置 Access 应用与允许策略，不留管理 API/RPC 的 Bypass 路径。生产路径为：`浏览器 → Cloudflare Access → Tunnel → cloudflared → 同命名空间回环管理监听`。普通桥接网络容器无法连接该回环监听，伪造 Host 或 Cloudflare 请求头不能改变网络可达性。

首次配置公开 Host/Origin 可先使用上述 Linux 本地控制台，或者执行以下一次性管理请求（Bash 示例；替换域名，现有自定义管理端口需同步修改 `base`）。辅助 Python 容器仅用于本次配置，不是运行组件：

若已启用 `MCP_ENFORCE_LOCAL_TOKEN`，先在当前 Bash 会话输入现有的管理控制令牌（不回显）；默认免令牌部署跳过这一步：

```bash
read -r -s -p 'Existing management control token: ' FROSTAGENT_BOOTSTRAP_TOKEN
printf '\n'
export FROSTAGENT_BOOTSTRAP_TOKEN
```

```bash
docker run --rm -i --env FROSTAGENT_BOOTSTRAP_TOKEN --network container:$(docker compose ps -q frostagent) python:3.13-alpine python - https://frostagent.example.com <<'PY'
import json, os, sys, urllib.request as http
base = "http://127.0.0.1:8080/api/instances/global/"
headers = {"Content-Type": "application/json"}
token = os.environ.get("FROSTAGENT_BOOTSTRAP_TOKEN", "").strip()
if token:
    headers["Authorization"] = "Bearer " + token
request = http.Request(base + "backup/settings", headers=headers)
with http.urlopen(request, timeout=10) as response:
    config = json.load(response)
config.pop("extra_env", None)
origins = set(filter(None, config["settings"].get("HTTP_ALLOWED_ORIGINS", "").split(",")))
config["settings"]["HTTP_ALLOWED_ORIGINS"] = ",".join(sorted(origins | {sys.argv[1]}))
request = http.Request(base + "import/settings", json.dumps(config).encode(), headers)
with http.urlopen(request, timeout=10) as response:
    print(response.read().decode())
PY
```

若设置了临时令牌变量，完成后执行 `unset FROSTAGENT_BOOTSTRAP_TOKEN`。

只设置宿主机 `127.0.0.1` 发布地址不能隔离容器网络；反过来，将端口发布到宿主机也不能让容器内回环监听变得可达。不要把管理监听改成 `0.0.0.0` 来修复 Tunnel 连接。宿主机 cloudflared 使用 host 方案；独立网络的容器 cloudflared 使用共享命名空间方案。

### 配置、升级和验证边界

SQL 设置仍优先；启动、热更新和数据库切换必须保持管理地址为数值回环 IP，且与适配器不同端口。不安全的存量地址拒绝启动，不自动迁移。PR #192 已有的 `127.0.0.1:8081` 仍有效，需让 Tunnel 回源到该实际端口；新库默认管理端口改回 8080。host 镜像的回环默认值不会覆盖 SQL 中已保存的 `WS_LISTEN_ADDR=0.0.0.0:1234`；从桥接部署切换到 host 前，须按需要先改为 `127.0.0.1:1234`，避免适配器监听宿主机所有接口。

容器构建仅信任实际来自回环的 connector 连接，保留公共 Host/Origin 和转发头；这份信任来自网络隔离，不是对 Cloudflare 请求头的认证。通过 Access 的正常请求默认无需另一套本地登录。既有 `MCP_ENFORCE_LOCAL_TOKEN` 仍有效；若用户显式启用了它，前端及一次性管理请求仍需原有 Bearer Token。普通构建的远程控制令牌规则保持不变。

镜像使用 UID/GID `10001:10001`，系统 CA 信任包用于出站 TLS，不生成部署 CA。`frostagent-data` 保留 `/app/data` 中的数据库、引导选择和贴图文件。`docker compose down` 保留卷；只有明确要删除数据时才使用 `down -v`。升级前备份应用卷与外部 PostgreSQL；旧 `frostagent-gateway-data` 证书卷不再使用，不自动删除。PR #192 升级时先用旧部署文件执行 `down`（不带 `-v`），停用旧 gateway，再选择新拓扑启动，保留相同 Compose 项目名以复用数据卷。

应用配置仍来自 SQL，`.env` 中的 `TUNNEL_TOKEN` 只供可选 connector 使用。容器内 `localhost` 指向当前网络命名空间；外部数据库、模型与沙箱使用可达地址。镜像不包含 Node.js/Python 等 stdio MCP 运行时，命令执行仍交给外部沙箱。

CI 构建桥接及 host 镜像，使用临时数据验证回源路径、公共 Host/Origin、管理隔离、端口暴露、OneBot/AstrBot WebSocket、SQL 监听约束、非 root、退出与持久化。测试使用本地合成回源客户端和只运行 `version` 的真实 cloudflared 镜像，不建立公网 Tunnel；Cloudflare Access 云端登录/策略、真实 IM 接入及 Desktop 宿主机转发不属于已验证范围。

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

MPL-2.0 (see LICENSE file)

## Skills 技能系统

选择实例后，在 **Skills → 导入 ZIP** 上传 Claude Code、Gemini CLI 等 harness 的 SKILL.md 技能包，支持单个技能、多个技能及仓库外层目录。导入后默认禁用，启用后由 Agent 按需加载，也可使用 `/skill-name 参数` 手动调用。随包脚本仅通过已启用的隔离沙箱执行。支持范围、大小限制和专属字段兼容提示见 [Skills 文档](.frostagent/whitepaper/skills.md)。
