# FrostAgent

An AI role-playing and agent orchestration framework written in Go, connecting to messaging platforms through OneBot, AstrBot and other adapters.

English | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.3+-blue.svg)](https://go.dev)
[![CI](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml/badge.svg)](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENCE)

## Releases

1.0.0 is prepared as a source-only release: build from the published source archive or tag; prebuilt binaries and registry images are deferred. A version in the repository does not prove publication—check [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases). See [release notes](docs/releases/v1.0.0.md) and the [installation, upgrade and release guide](docs/releasing.md).

## Quick Start

Run this Bash example from the repository root with Go ≥ 1.27.3, Node.js 22, pnpm 10.33.0 and Make installed. For native Windows PowerShell commands, see the [installation guide](docs/releasing.md#windows--powershell).

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@v1.46.0
make build
go run ./cmd/app
```

Open <http://localhost:8080>, then:

1. Create and select an instance under **实例管理** at the bottom of the sidebar.
2. Configure providers, models and the instance's routes in **模型路由器**.
3. Set its name, wake aliases and system prompt in **人格设定**.
4. Connect an adapter below and turn on **是否启用** in Overview.

SQLite is the default; no separate database is needed. For an existing deployment, read [upgrades and backups](docs/configuration.en.md#before-upgrading) first.

## Adapters

Set the upstream reverse WebSocket URL below, replacing `<instance-id>` with your instance ID. The actual port follows the `WS_LISTEN_ADDR` setting.

| Adapter                                                        | Default URL                                              |
| -------------------------------------------------------------- | -------------------------------------------------------- |
| OneBot                                                         | `ws://127.0.0.1:1234/instances/<instance-id>/ws/onebot`  |
| [AstrBot plugin](adapters/astrbot_plugin_frostagent/README.md) | `ws://127.0.0.1:1234/instances/<instance-id>/ws/astrbot` |

Disable AstrBot's built-in LLM response module; other plugins can remain active. See [Docker deployment](docs/docker.en.md) for cross-container addresses. [ActionsCat integration](.frostagent/whitepaper/actionscat.md) is optional.

## Docker deployment

Choose host or container cloudflared in the [Docker guide](docs/docker.en.md). The default bridge deployment does not expose a host management port, so a host browser cannot open it directly.

**Public management requires Cloudflare Access; a Tunnel alone is not authentication.** Ordinary builds use a [control token](docs/configuration.en.md#management-authentication) for remote management. Do not expose an unprotected console.

## Read as needed

| Task                                                       | Guide                                                                                                   |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| Configure persona, models, Embedding, databases or backups | [Configuration and data](docs/configuration.en.md)                                                      |
| Deploy or upgrade with Docker                              | [Docker deployment](docs/docker.en.md)                                                                  |
| Import skill bundles                                       | [Skills](.frostagent/whitepaper/skills.md): disabled after import; scripts require the isolated sandbox |
| Develop, build and verify                                  | [Development](docs/development.md)                                                                      |
| Understand internals                                       | [Design documents](.frostagent/whitepaper/)                                                             |

## License

[MPL-2.0](LICENCE)
