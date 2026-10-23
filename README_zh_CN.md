# FrostAgent

基于 Go 的 AI 角色扮演与智能体调度框架，通过 OneBot、AstrBot 等适配器接入即时通信平台。

[English](README.md) | 中文

[![Go Version](https://img.shields.io/badge/Go-1.27.3+-blue.svg)](https://go.dev)
[![CI](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml/badge.svg)](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENCE)

## 版本发布

1.0.0 按源码版本准备：发布后下载源码归档或检出 tag，自行构建；暂不分发预编译程序或镜像。仓库版本号不代表已经发布，以 [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases) 为准。参见[发布说明](docs/releases/v1.0.0.md)和[安装、升级与发布指南](docs/releasing.md)。

## 快速开始

以下 Bash 命令在仓库根目录执行，需 Go ≥ 1.27.3、Node.js 22、pnpm 10.33.0 和 Make。Windows 原生 PowerShell 命令见[安装指南](docs/releasing.md#windows--powershell)。

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@v1.46.0
make build
go run ./cmd/app
```

打开 <http://localhost:8080>，依次完成：

1. 在侧边栏底部「实例管理」创建并选择实例。
2. 在「模型路由器」配置供应商、模型和当前实例的模型路由。
3. 在「人格设定」填写名称、唤醒别名和系统提示词。
4. 接入下方适配器，打开概览中的「是否启用」。

默认使用 SQLite，无需另装数据库。已有部署升级前，请先阅读[升级与备份](docs/configuration.md#升级前必读)。

## 适配器

将上游的反向 WebSocket 地址设置为以下之一；`<实例ID>` 替换为实际 ID，端口以 `WS_LISTEN_ADDR` 设置为准。

| 接入方式                                                     | 默认地址                                            |
| ------------------------------------------------------------ | --------------------------------------------------- |
| OneBot                                                       | `ws://127.0.0.1:1234/instances/<实例ID>/ws/onebot`  |
| [AstrBot 插件](adapters/astrbot_plugin_frostagent/README.md) | `ws://127.0.0.1:1234/instances/<实例ID>/ws/astrbot` |

使用 AstrBot 时关闭其自带 LLM 响应模块，其他插件可继续使用。跨容器连接的地址见 [Docker 部署](docs/docker.md)。[ActionsCat 集成](.frostagent/whitepaper/actionscat.md) 可按需启用。

## Docker 部署

先在 [Docker 指南](docs/docker.md) 中选择宿主机或容器 cloudflared 方案。默认桥接部署不开放宿主机管理端口，不能直接用浏览器访问。

**公网管理必须先配置 Cloudflare Access；Tunnel 本身不是认证。** 普通构建的远程管理使用[控制令牌](docs/configuration.md#管理访问认证)，不要直接暴露未保护的控制台。

## 按需阅读

| 想做什么                                | 文档                                                                           |
| --------------------------------------- | ------------------------------------------------------------------------------ |
| 调整人格、模型、Embedding、数据库或备份 | [配置与数据](docs/configuration.md)                                            |
| 用 Docker 部署或升级                    | [Docker 部署](docs/docker.md)                                                  |
| 导入技能包                              | [Skills](.frostagent/whitepaper/skills.md)：导入后默认禁用，脚本需启用隔离沙箱 |
| 开发、构建与验证                        | [开发指南](docs/development.md)                                                |
| 了解内部设计                            | [设计文档](.frostagent/whitepaper/)                                            |

## 许可证

[MPL-2.0](LICENCE)
