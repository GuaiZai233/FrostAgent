# FrostAgent

FrostAgent is an AI role-playing and agent orchestration framework written in Golang. It supports multiple protocol adapters, such as OneBot, and can be integrated with various instant messaging applications.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

## Adapters

### WebSocket

Enable a reverse WebSocket client in your local upstream bot/client, setting the URL to `ws://127.0.0.1:1234/instances/<instance-id>/ws/onebot` (the actual port depends on `WS_LISTEN_ADDR` in your environment variables).

### Collaboration with ActionsCat

[ActionsCat](https://github.com/actionscat/actionscat) supports static orchestration of automated workflows.

After connecting ActionsCat via the adapter, you can run both systems in parallel to leverage the agent's advanced capabilities.

Note: Since active maintenance of the ActionsCat project is currently suspended, you may choose alternative solutions for your bot's plugin ecosystem.

### Collaboration with Frameworks Such as AstrBot

When connected via adapters, FrostAgent can replace the LLM response module of agent frameworks like AstrBot without disrupting their rich plugin ecosystems.

You can use the dedicated AstrBot plugin: [astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) to establish the connection. The default FrostAgent endpoint is `ws://127.0.0.1:1234/instances/<instance-id>/ws/astrbot`. Simply configure the communication between AstrBot and its upstream IM platform, and FrostAgent will take over message processing. **Note**: Please disable AstrBot's built-in LLM response module!

## Quick Start

### 1. Build the Project

This project uses the root `Makefile` for build orchestration.

```bash
# Install Node.js dependencies (Angular toolchain, etc.)
# This project uses pnpm as the package manager
pnpm install

# Install buf for protobuf code generation
go install github.com/bufbuild/buf/cmd/buf@latest

# Build everything - backend Go binaries + frontend Angular app
make build
```

Compiled backend binaries will be located in `./bin/`, and frontend static assets will be placed in `internal/frontend/dist/`.

You can also build individual components:

```bash
make build-api    # Build backend with embedded frontend
make build-web    # Build frontend only
```

### 2. Choose a database

SQLite is the default. FrostAgent creates `data/frostagent.db` on first start. Open **Settings > Database Settings** to choose PostgreSQL and enter its connection address; an empty address is rejected. The selection is kept in the local SQLite bootstrap table so it can be read before connecting to PostgreSQL. Switching connects to the target database and loads its existing settings and instances without migrating data. Use the instance backup and restore tools to move data manually. Only one FrostAgent process may use a PostgreSQL database at a time.

**Breaking change:** JSON and `.env` runtime storage are no longer loaded or migrated. Back up existing data before replacing an earlier version. SQL schemas from the v7 baseline upgrade through ordered, non-destructive migrations; a database from a newer binary or before v7 is rejected without modification. The dashboard's **Settings > Backup & Restore** page downloads a versioned per-instance ZIP, `setting.json`, and `memory.json`. ZIP exports are limited to 512 MiB of expanded data and 512 MiB of archive bytes. Exports omit secrets and credential sources. A full ZIP restore creates a new disabled instance; configure credentials and adapters before enabling it.

### 3. Start the Service

```bash
go run ./cmd/app
```

Open the dashboard at `http://localhost:8080`. It starts with no instances. Use **实例管理** in the sidebar to create and select an instance, configure its model router, then turn on **是否启用** in Overview. Settings, memory, model routing, MCP configuration, dialogue examples, sticker metadata, and group summaries are stored in SQL. Sticker image bytes remain in the instance file directory. Settings changes apply without a manual process restart.

## License

MPL-2.0 (see LICENSE file)
