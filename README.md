# FrostAgent

FrostAgent is a Go framework for AI role-playing and agent orchestration. It connects to chat platforms through OneBot v11 or AstrBot and provides a web dashboard for instances, personas, model routing, memory, and tools.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.0+-blue.svg)](https://go.dev)
[![CI](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml/badge.svg)](https://github.com/GuaiZai233/FrostAgent/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENSE)

## Documentation

The complete Chinese documentation is in [`docs/`](docs/index.md): [introduction](docs/guide/introduction.md), [quick start](docs/guide/quick-start.md), [installation](docs/guide/installation.md), [configuration](docs/reference/configuration.md), [backups](docs/operations/storage.md), and [troubleshooting](docs/operations/troubleshooting.md).

Run the VitePress site locally:

```bash
pnpm install --frozen-lockfile
pnpm docs:dev
```

Use `pnpm docs:build` and `pnpm docs:preview` for production output and preview. The documentation site is built separately from the Bot dashboard.

## Quick Start

Prepare Go ≥ 1.27.0, Node.js 22+, pnpm 10.33.0, Buf, Make, and Bash:

```bash
pnpm install --frozen-lockfile
go install github.com/bufbuild/buf/cmd/buf@latest
make build
go run ./cmd/app
```

For a Windows PowerShell build without Make or Bash, see [installation](docs/guide/installation.md).

Open `http://127.0.0.1:8080`, create and select an instance, add a provider and model in **模型路由器**, publish its dialogue binding, and enable it from **概览**. Validate a temporary session through **直接对话** before connecting a real platform.

Current application settings and durable data use SQL, with `data/frostagent.db` as the default SQLite database and PostgreSQL selectable in the dashboard. No `.env.example` is required. Legacy `.env` and JSON directories are not automatically migrated; see [upgrade notes](docs/operations/upgrading.md). Providers, credentials, and models are shared; personas, routes, memory, sessions, and MCP configuration belong to each instance.

## Embedding Memory Retrieval

Versions containing [#194](https://github.com/GuaiZai233/FrostAgent/pull/194) add the seventh workload, `embedding`. Configure an OpenAI-compatible `/embeddings` model with the `embedding` capability, publish its independent instance or group binding, and enable `ENABLE_EMBEDDING` in memory settings. Tags, extraction, summaries, and reflection remain; enabled searches use vector similarity, tools prefer `query` over `tags`, and web pagination remains. Earlier versions use keyword retrieval.

SQLite vectors stay in memory and rebuild after a process restart. PostgreSQL requires deployment administrators to install pgvector and enable `CREATE EXTENSION vector` in the target database; FrostAgent does not install extensions. Disabling restores keyword search and retains compatible PostgreSQL vectors. Application exports and ZIP backups exclude vectors.

Models may configure an optional maximum input token budget. A conservative UTF-8 byte budget truncates request copies while preserving stored text; explicit length rejections skip the relevant processing. Schema v12 stores input limits and summary refresh markers. See the [Embedding guide](docs/usage/embedding.md) for setup, index updates, and troubleshooting.

## Adapters

Copy the instance-specific address from Overview and configure a reverse WebSocket client in the upstream:

```text
ws://127.0.0.1:1234/instances/<instance-id>/ws/onebot
ws://127.0.0.1:1234/instances/<instance-id>/ws/astrbot
```

For AstrBot, install the [dedicated plugin](adapters/astrbot_plugin_frostagent) and disable AstrBot's own LLM replies. Across containers, use reachable hosts and configure the plugin's HTTP base URL for sticker images.

## Development and License

See [Development.md](Development.md) and the [development guide](docs/development/index.md). The dashboard uses Vite and native TypeScript and is embedded in the Go executable.

Released under [MPL-2.0](LICENSE).
