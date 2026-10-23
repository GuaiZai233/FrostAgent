# FrostAgent

Remote management API requests require a valid control token, including settings,
backups, logs, instance operations and database selection. Configure the management
token locally in Bot server settings, then enter it in the web console's frontend
settings. `MCP_CONTROL_TOKEN` takes priority over the compatible `ADMIN_TOKEN`.
`ALLOW_REMOTE_MCP_MANAGEMENT` does not bypass management API authentication.
Local loopback access remains available unless `MCP_ENFORCE_LOCAL_TOKEN` is enabled.
Reverse proxies must preserve the public Host or a forwarding header. If a proxy
rewrites Host to localhost and removes all forwarding metadata, enable
`MCP_ENFORCE_LOCAL_TOKEN` so its requests cannot use the local exemption.

FrostAgent is an AI role-playing and agent orchestration framework written in Golang. It supports multiple protocol adapters, such as OneBot, and can be integrated with various instant messaging applications.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENCE)

## Releases

Version 1.0.0 is prepared as a source-only release. A version in `package.json` does not mean it has been published; use the [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases) page to check availability. Download the source archive or check out the published `v1.0.0` tag, then build locally. Prebuilt executables and registry-hosted Docker images are deferred; the Dockerfiles and Compose files build from source.

See the [1.0.0 release notes](docs/releases/v1.0.0.md) and the [source installation, upgrade and maintainer release guide](docs/releasing.md). The three private workspace packages share the project version; the AstrBot adapter keeps its independent plugin version.

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

This project uses the root `Makefile` for build orchestration. See the [development guide](docs/development.md) for local development and verification commands.

```bash
# Install Node.js dependencies (Vite and TypeScript)
# This project uses pnpm as the package manager
pnpm install --frozen-lockfile

# Install buf for protobuf code generation
go install github.com/bufbuild/buf/cmd/buf@latest

# Build everything - backend Go binaries + frontend Vite app
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

Open the dashboard at `http://localhost:8080`. It starts with no instances. Use **实例管理** in the sidebar to create and select an instance, configure providers, models and instance routing together under **模型路由器**, then turn on **是否启用** in Overview. Settings, memory, model routing, MCP configuration, dialogue examples, sticker metadata, and group summaries are stored in SQL. Sticker image bytes remain in the instance file directory. Settings changes apply without a manual process restart.

## Docker deployment

```bash
docker compose up -d --build
```

Uses host networking on Linux; Docker Desktop requires host networking to be enabled. With a new database, open `http://localhost:8080`; management and adapters default to `127.0.0.1:8080` and `127.0.0.1:1234`. Existing SQL listener settings take precedence, so check them before upgrading from bridge mode. Application data is kept in the `frostagent-data` volume. Configure **Cloudflare Access** separately if exposing the console through a reverse proxy; it does not replace FrostAgent's management control-token checks.

## Bot settings and persona

Configure the Bot name, wake aliases and system prompt under **人格设定**. Server settings use Chinese forms with a red sticky bar for pending changes. **保存** applies the draft; **还原** or leaving the page discards unsaved edits. Additional environment injection is saved with the draft and takes effect after restarting FrostAgent.

`MAX_CONTEXT_CHARS`, `UPSTREAM_API_KEY`, `CODER_API_KEY`, `ALLOW_REMOTE_MCP_MANAGEMENT` and adapter selection switches are no longer offered as regular settings. Configure credentials on model endpoints and bind Coder through the subagent workload in **模型路由器**. Billing prices use nonnegative integer minor units (100 units = 1 snowflake = 0.01 CNY); the safety multiplier must be positive. Defaults appear as gray placeholders inside inputs. Leave an input empty and save to use its default or inherited value.

## Shared providers and instance routing

**模型路由器** keeps Endpoints, Models, instance default routes and group overrides in one page. Provider URLs, credentials, enabled states and model definitions are shared across all instances in the selected database, even before an instance exists. Default workload models and group overrides belong to the selected Bot. Publishing applies changes to the shared catalog and the current instance's routes; if one scope fails, the page reports any completed publication and keeps the remaining edits for retry. Published provider changes apply to new requests immediately; an ongoing turn retains its URL and credential snapshot. Deleting an instance preserves shared providers. Models referenced by instance routes cannot be deleted.

Schema v11 stores the shared catalog in `provider_endpoints`, `provider_models`, `provider_secrets` and `provider_revision`. `model_bindings` and `model_revisions` remain instance scoped. Upgrades from v7–v10 copy providers and remap conflicting IDs, names and route references transactionally, retaining the old tables as inactive migration archives. Fresh databases use only the new structure. Manually modified databases containing duplicate Windows credential endpoint IDs are rejected without changing their schema version or source data.

Instance backups include only referenced provider/model definitions and omit credentials. Copying or restoring an instance reuses matching shared definitions and their existing credentials. Conflicting definitions receive new IDs and empty credentials, with imported routes remapped accordingly; other instances and shared credentials remain unchanged.

## Embedding memory retrieval

Configure an OpenAI-compatible `/embeddings` model in the shared catalog, mark its `embedding` capability, bind the Embedding route, and enable the instance memory setting. Group routes may inherit or select a model. Existing tags, extraction, summaries and reflection remain; enabled searches use vector similarity, with 20 results for the bot/tools and unchanged web pagination. Indexes refresh after the existing extraction windows and reflection, with background rebuilding when missing or incompatible.

SQLite vectors remain in memory. PostgreSQL requires pgvector installed and enabled by the deployment administrator (`CREATE EXTENSION vector`); FrostAgent does not install extensions. Disabling restores keyword search and retains compatible PostgreSQL indexes. Exports and ZIP backups exclude vectors. Models may set an optional maximum input token budget; the conservative UTF-8 byte budget truncates request copies. Explicit upstream length rejections skip processing without deleting stored text. Schema v12 adds input limits and summary refresh markers.

## License

MPL-2.0 (see [LICENCE](LICENCE))

## Skills

Select an instance and use **Skills → 导入 ZIP** to import portable SKILL.md bundles from Claude Code, Gemini CLI and other Agent Skills harnesses. Enable an imported skill to make it available to the agent, or invoke it with `/skill-name arguments`. Bundled scripts run only in the enabled isolated sandbox. See [Skills format and runtime](.frostagent/whitepaper/skills.md) for supported layouts, limits and compatibility warnings.
