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

### 2. Configure Environment Variables

Copy `.env.example` to `.env` for Control Plane settings (listeners, allowed origins, and shared Alcyone upstream). Each instance owns its bot settings and system prompt under `data/instance_<instance-id>/.env`.

> **Upgrade Notice (Breaking Change)**: In previous versions, `SYSTEM_PROMPT` was defined globally in the root `.env`. System prompts are now strictly isolated per instance (`data/instance_<instance-id>/.env`). Legacy shared `SYSTEM_PROMPT` values from the root `.env` or process environment are intentionally discarded and will **not** be implicitly backfilled into existing instances. If an existing instance does not have `SYSTEM_PROMPT` configured, it will not fall back to the old shared prompt; configure it in the dashboard Settings or in the instance's `.env`. Newly created instances automatically receive the template default prompt.

### 3. Start the Service

```bash
go run ./cmd/app
```

Open the dashboard at `http://localhost:8080`. It starts with no instances. Use **实例管理** in the sidebar to create and select an instance, configure its model router, then turn on **是否启用** in Overview. Refreshing the dashboard requires selecting an instance again. Each instance has independent settings (including system prompt), memory, sessions, stickers, logs and persona dialogue examples (initialized from template `eval/dialogue/dialogue.yml` and isolated per instance at `data/instance_<instance-id>/dialogue.yml`).

## License

MPL-2.0 (see LICENSE file)
