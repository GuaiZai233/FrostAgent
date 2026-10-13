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

Open the dashboard at `http://localhost:8080`. It starts with no instances. Use **实例管理** in the sidebar to create and select an instance, configure providers, models and instance routing together under **模型路由器**, then turn on **是否启用** in Overview. Settings, memory, model routing, MCP configuration, dialogue examples, sticker metadata, and group summaries are stored in SQL. Sticker image bytes remain in the instance file directory. Settings changes apply without a manual process restart.

## Docker deployment

From the repository root, start the service with Docker Compose:

```bash
docker compose up -d --build
docker compose logs -f frostagent
```

Open `http://localhost:8080` and create an instance as described above. The adapter endpoint is `ws://localhost:1234/instances/<instance-id>/ws/onebot` (or `/ws/astrbot`). An upstream client on the same Compose network can use `ws://frostagent:1234/instances/<instance-id>/ws/onebot`.

The Dockerfile builds protobuf code and the embedded frontend from source. The runtime runs as UID/GID `10001:10001` and includes CA certificates and timezone data. Only the image's fallback listeners use `0.0.0.0:8080` and `0.0.0.0:1234`; ordinary builds keep their loopback defaults. Saved SQL settings take precedence. Configure settings in the dashboard; `.env` files and application environment variables are not loaded.

Compose publishes both ports on the host's `127.0.0.1` by default. For access from another machine, adjust the host port bindings and configure the exact dashboard URL in `HTTP_ALLOWED_ORIGINS` through the dashboard, with your existing access control in front of the management interface. Adjust `WS_ALLOWED_ORIGINS` if a browser adapter uses a different origin. When reusing a database with loopback listener settings, change those settings to `0.0.0.0` before moving it into the container. If you change container-side ports in SQL, update the Compose mappings as well.

Requests through Docker's bridge network reach FrostAgent from a non-loopback address, even when the browser uses `localhost`. For token-protected MCP and security APIs, configure `MCP_CONTROL_TOKEN` or `ADMIN_TOKEN` in backend settings and enter the matching token in frontend settings.

The `frostagent-data` named volume stores `/app/data`, including SQLite, the PostgreSQL bootstrap selection and sticker files. `docker compose down` retains it; `docker compose down -v` deletes it. Back up the volume and any external PostgreSQL database before upgrading. For a bind mount instead, grant UID/GID `10001:10001` write access to its directory.

To use Docker directly:

```bash
docker build -t frostagent:local .
docker run -d --name frostagent --init --restart unless-stopped \
  -p 127.0.0.1:8080:8080 -p 127.0.0.1:1234:1234 \
  -v frostagent-data:/app/data frostagent:local
```

From inside the container, `localhost` refers to the container itself. Use a service name or a reachable host address for PostgreSQL, model providers and sandbox gateways. The image does not include Node.js, Python or other stdio MCP executables; use HTTP MCP servers or extend the image with the tools you need. Command execution continues to use the external sandbox gateway.

## Shared providers and instance routing

**模型路由器** keeps Endpoints, Models, instance default routes and group overrides in one page. Provider URLs, credentials, enabled states and model definitions are shared across all instances in the selected database, even before an instance exists. Default workload models and group overrides belong to the selected Bot. Publishing applies changes to the shared catalog and the current instance's routes; if one scope fails, the page reports any completed publication and keeps the remaining edits for retry. Published provider changes apply to new requests immediately; an ongoing turn retains its URL and credential snapshot. Deleting an instance preserves shared providers. Models referenced by instance routes cannot be deleted.

Schema v11 stores the shared catalog in `provider_endpoints`, `provider_models`, `provider_secrets` and `provider_revision`. `model_bindings` and `model_revisions` remain instance scoped. Upgrades from v7–v10 copy providers and remap conflicting IDs, names and route references transactionally, retaining the old tables as inactive migration archives. Fresh databases use only the new structure. Manually modified databases containing duplicate Windows credential endpoint IDs are rejected without changing their schema version or source data.

Instance backups include only referenced provider/model definitions and omit credentials. Copying or restoring an instance reuses matching shared definitions and their existing credentials. Conflicting definitions receive new IDs and empty credentials, with imported routes remapped accordingly; other instances and shared credentials remain unchanged.

## License

MPL-2.0 (see LICENSE file)
