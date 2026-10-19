# FrostAgent

Outside the isolated Docker gateway deployment described below, remote management API requests require a valid control token, including settings,
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

[![Go Version](https://img.shields.io/badge/Go-1.27.0+-blue.svg)](https://go.dev)
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

Open the dashboard at `http://localhost:8080`. It starts with no instances. Use **实例管理** in the sidebar to create and select an instance, configure providers, models and instance routing together under **模型路由器**, then turn on **是否启用** in Overview. Settings, memory, model routing, MCP configuration, dialogue examples, sticker metadata, and group summaries are stored in SQL. Sticker image bytes remain in the instance file directory. Settings changes apply without a manual process restart.

## Docker deployment

Compose starts FrostAgent behind a Caddy HTTPS authentication gateway. First copy `docker.env.example` to `.env` (or add its two gateway fields to an existing file), then generate a password hash interactively:

```bash
docker run --rm -it caddy:2-alpine caddy hash-password
```

Set `FROSTAGENT_ADMIN_USER` and put the generated bcrypt hash in `FROSTAGENT_ADMIN_PASSWORD_HASH='...'` in `.env`. Keep the single quotes to preserve dollar signs. There is no default password; Compose refuses to start without a hash. These two fields configure only the gateway. Application settings continue to come from SQL.

```bash
docker compose up -d --build
docker compose logs -f gateway frostagent
docker compose cp gateway:/data/caddy/pki/authorities/local/root.crt ./docker-gateway-root.crt
```

Trust the exported local CA certificate in the browser's certificate store, then open `https://localhost:8080` and sign in with the gateway username and password. The certificate is local to this deployment and survives recreation through its named volume. The gateway requires authentication for every page, API and dashboard WebSocket; it consumes the Basic Authorization header before forwarding. Leave the frontend control token empty for this gateway. If existing SQL settings enforce a local backend token, configure Caddy to supply that Bearer token after gateway authentication.

The gateway and FrostAgent share a network namespace. FrostAgent's management listener defaults to `127.0.0.1:8081` and cannot be reached directly by other network peers; the gateway proxies to that listener. Container builds reject non-loopback management addresses and shared management/adapter ports, both on startup and when settings change. In this isolated deployment, the backend treats the authenticated gateway's loopback connection as local even when forwarding headers are present; `MCP_ENFORCE_LOCAL_TOKEN` still applies when enabled. Saved SQL settings remain authoritative within these constraints. Ordinary builds keep their existing listener defaults and remote management token checks.

The adapter listener defaults to `0.0.0.0:1234`. Use `ws://localhost:1234/instances/<instance-id>/ws/onebot` (or `/ws/astrbot`). A client on the same Compose network can use the alias `ws://frostagent:1234/instances/<instance-id>/ws/onebot`. The adapter port serves only adapter WebSockets; management routes are not exposed there.

Compose publishes HTTPS port 8080 and adapter port 1234 on the host's `127.0.0.1` by default. For remote deployment, configure the gateway hostname and certificate, publish the required ports, and add the exact dashboard URL to `HTTP_ALLOWED_ORIGINS` through the local dashboard. Keep gateway authentication and the loopback backend listener. Configure `WS_ALLOWED_ORIGINS` for browser adapters with a different origin.

Before reusing an existing database, set `LISTEN_ADDR` to `127.0.0.1:8081` and `WS_LISTEN_ADDR` to `0.0.0.0:1234` in the original deployment. Unsafe saved addresses cause container startup to fail; no settings are automatically rewritten. If you choose a different loopback management port, update Caddy's upstream; adapter port changes require matching Compose mappings.

The runtime uses UID/GID `10001:10001`, CA certificates and timezone data. The `frostagent-data` volume stores `/app/data`, including SQLite, PostgreSQL bootstrap selection and sticker files. The `frostagent-gateway-data` volume stores gateway certificates. `docker compose down` retains both; `docker compose down -v` deletes them. Back up these volumes and any external PostgreSQL database before upgrading. Bind-mounted application data must be writable by UID/GID `10001:10001`.

The Dockerfile generates protobuf and builds the embedded frontend from source. Use Compose to include the protected gateway when deploying this image. Container `localhost` refers to the shared gateway/backend namespace; use reachable service names or host addresses for external databases, models and sandbox gateways. Node.js/Python stdio MCP tools are not included; use HTTP MCP servers or extend the image. Shell execution continues to use the external sandbox gateway.

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

MPL-2.0 (see LICENSE file)

## Skills

Select an instance and use **Skills → 导入 ZIP** to import portable SKILL.md bundles from Claude Code, Gemini CLI and other Agent Skills harnesses. Enable an imported skill to make it available to the agent, or invoke it with `/skill-name arguments`. Bundled scripts run only in the enabled isolated sandbox. See [Skills format and runtime](.frostagent/whitepaper/skills.md) for supported layouts, limits and compatibility warnings.
