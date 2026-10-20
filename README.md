# FrostAgent

Outside the isolated Cloudflare Access deployment described below, remote management API requests require a valid control token, including settings,
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

Management authentication follows the PR #165 decision: put **Cloudflare Access** in front of FrostAgent. The deployment no longer includes Caddy, Basic Auth administrator accounts or a local CA. Operators choose where cloudflared runs; a working Tunnel alone does not configure Access.

### Local testing or host cloudflared (native Linux)

Use the standalone host-network file, without merging it with the bridge files:

```bash
docker compose -f compose.host.yaml up -d --build
```

With a new database, open `http://127.0.0.1:8080`. Adapter URLs are `ws://127.0.0.1:1234/instances/<instance-id>/ws/onebot` or `/ws/astrbot`. This image defaults both listeners to loopback. Host mode has no port publishing: the application's actual loopback binds provide isolation. Local tests need no Tunnel.

An existing cloudflared process on the same Linux host can use `http://127.0.0.1:8080` directly. The production path is `browser → Cloudflare Access → Tunnel → host cloudflared → loopback management listener`. Add the complete public origin, for example `https://frostagent.example.com`, to `HTTP_ALLOWED_ORIGINS` using the local console before enabling its public route.

This isolation guarantee targets native Linux Engine. Docker Desktop host networking requires explicit user opt-in and uses a different layer-four forwarding implementation; do not infer Windows/macOS external-interface exposure from the container bind. Desktop host forwarding is not verified here. Windows/macOS users can use the native build above for a local console. See [Docker host networking](https://docs.docker.com/engine/network/drivers/host/).

### Container cloudflared (bridge networking)

The default `compose.yaml` starts only FrostAgent:

```bash
docker compose up -d --build
```

Management listens on **container** `127.0.0.1:8080` without a published port, so the host browser cannot reach it directly. Adapters listen on container `0.0.0.0:1234`, published only at host `127.0.0.1:1234`. Same-network adapters can use `ws://frostagent:1234/instances/<instance-id>/ws/onebot` or `/ws/astrbot`. Port 1234 serves adapter WebSockets only, excluding management pages, APIs and RPCs.

cloudflared must **share FrostAgent's network namespace**, rather than merely join its bridge network. To run your existing Tunnel connector with this Compose project, put the `TUNNEL_TOKEN` from `docker.env.example` in `.env`, and opt into:

```bash
docker compose -f compose.yaml -f compose.cloudflare.yaml up -d --build
```

This adds only the operator's chosen cloudflared, without another proxy or authentication component. Do not start a duplicate if the connector already runs elsewhere: configure it with `network_mode: container:<FrostAgent-container-ID>` instead. Recreate the connector whenever FrostAgent is recreated so it joins the new namespace. When using the supplied files, always include both during updates; the optional file declares this recreation dependency.

Set the Cloudflare origin to `http://127.0.0.1:8080`. Before publishing the route, configure an Access application and allow policy covering **every path on the entire management hostname**, without API/RPC Bypass paths. The production path is `browser → Cloudflare Access → Tunnel → cloudflared → shared-namespace loopback management listener`. Ordinary bridge peers cannot connect to this listener; spoofed Host or Cloudflare headers cannot change network reachability.

Bootstrap the public Host/Origin through the Linux local console above, or this one-time management request (Bash example; replace the hostname and adjust `base` for a saved custom management port). The disposable Python helper is a configuration tool, not a deployment component:

If `MCP_ENFORCE_LOCAL_TOKEN` is enabled, first enter the existing management control token in the current Bash session without echoing it. Skip this step for the default token-free deployment:

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

If you set the temporary token variable, run `unset FROSTAGENT_BOOTSTRAP_TOKEN` afterwards.

A host-loopback publish does not isolate a container network. Conversely, publishing a port cannot make a container-loopback listener reachable from the host. Keep management off wildcard binds when fixing Tunnel connectivity: host cloudflared uses the host topology, while a separately networked container connector uses the shared namespace topology.

### Settings, upgrades and verification boundaries

SQL settings retain precedence. Startup, hot updates and database switches require a numeric loopback management IP and a different adapter port. Unsafe saved settings fail closed without automatic migration. PR #192's saved `127.0.0.1:8081` remains valid; point the Tunnel at that actual port. New databases default to management port 8080 again. The host image's loopback default does not override saved `WS_LISTEN_ADDR=0.0.0.0:1234`: before switching an existing bridge database to host mode, set it to `127.0.0.1:1234` if adapters should remain host-local.

Container builds trust only actual loopback connector connections, retaining public Host/Origin and forwarding headers. This trust comes from network isolation, not authentication of Cloudflare headers. Access-protected requests need no additional local login by default. Existing explicit `MCP_ENFORCE_LOCAL_TOKEN` remains effective; if enabled, the frontend and one-time management requests still need the existing Bearer token. Ordinary builds retain their remote control-token rules.

The runtime uses UID/GID `10001:10001`; the system CA bundle supports outbound TLS and does not generate a deployment CA. `frostagent-data` preserves `/app/data`, including database/bootstrap selection and sticker files. `docker compose down` retains it; use `down -v` only when intending to delete data. Back up the application volume and external PostgreSQL before upgrades. The old `frostagent-gateway-data` certificate volume is unused and is not automatically deleted. When upgrading PR #192, run `down` with the old files (without `-v`) to stop the old gateway, then choose a new topology, keeping the same Compose project name to reuse application data.

Application settings remain in SQL. The optional connector alone consumes `.env`'s `TUNNEL_TOKEN`. Container `localhost` refers to the current namespace; use reachable addresses for external databases, models and sandboxes. The image has no Node.js/Python stdio MCP runtime; command execution still uses the external sandbox.

CI builds bridge and host images and uses disposable data to verify origin reachability, public Host/Origin, management isolation, port exposure, OneBot/AstrBot WebSockets, SQL listener guards, non-root operation, shutdown and persistence. It uses a synthetic local origin client and a real cloudflared image running only `version`, without opening a public Tunnel. Live Access login/policies, real IM integration and Desktop host forwarding remain unverified.

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
