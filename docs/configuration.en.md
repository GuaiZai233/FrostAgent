# Configuration and data

[Back to README](../README.md) · [中文](configuration.md)

For first use, create an instance, configure a model and connect an adapter. Consult the sections below as needed.

## Persona and settings

- Set the Bot name, wake aliases and system prompt under **人格设定**.
- In Bot server settings, **保存** applies the pending draft; **还原** or leaving the page discards unsaved changes.
- Gray placeholders show defaults or inherited values. Clear an input and save to remove its override.
- Regular settings apply on save; extra environment injection requires a restart. Legacy JSON and `.env` runtime configuration are no longer loaded.
- Configure model credentials on Endpoints in **模型路由器** and bind Coder through the subagent workload. Legacy `UPSTREAM_API_KEY`, `CODER_API_KEY`, `MAX_CONTEXT_CHARS` and adapter selection switches are no longer regular settings.
- Billing prices use nonnegative integer minor units: 100 units = 1 snowflake = 0.01 CNY. The safety multiplier must be positive.

## Model routing

**模型路由器** brings Endpoints, Models, default routes and group overrides together:

| Configuration                                                | Scope                                                               |
| ------------------------------------------------------------ | ------------------------------------------------------------------- |
| Provider URLs, credentials, enabled states and model catalog | Shared across the selected database, even before an instance exists |
| Default models for seven workloads and group overrides       | Per instance; groups may inherit defaults                           |

Publishing affects new requests immediately; ongoing turns retain their URL and credential snapshots. If only the catalog or instance routes publish successfully, the page reports the partial result and retains remaining edits for retry. Deleting an instance preserves shared providers; models referenced by routes cannot be deleted.

## Embedding memory retrieval

1. Add an OpenAI-compatible `/embeddings` model in **模型路由器** and mark its `embedding` capability.
2. Bind the instance or group Embedding route.
3. Enable **启用Embedding** under the Bot server's memory settings.

Tags, extraction, summaries and reflection remain unchanged. Searches use vectors; indexes refresh after extraction windows and reflection, rebuilding in the background when missing or incompatible. Tools accept `query` or `tags`, preferring `query`, with up to 20 results for bots/tools; web pagination is unchanged.

- **SQLite** keeps vectors in memory and rebuilds them after restart.
- **PostgreSQL** requires the administrator to install pgvector and run `CREATE EXTENSION vector` in the target database before enabling the feature.
- Disabling restores keyword search and retains PostgreSQL vectors. Backups exclude vectors.
- The optional **最大输入 Tokens** limit uses a conservative UTF-8 byte budget to truncate request copies. Explicit length rejections skip processing without deleting stored text.

See the [memory design](../.frostagent/whitepaper/memory.md) for internals.

## Databases and backups

SQLite at `data/frostagent.db` is the default. **设置 → 数据库设置** switches to PostgreSQL with a required connection address; the selection is kept in the local SQLite bootstrap table. Only one FrostAgent process may use a PostgreSQL database at a time.

**Switching loads the target's existing data; it does not migrate data.** Use **设置 → 备份与还原** to move instances. SQL stores settings, memory, routes, MCP, dialogue examples, sticker metadata and group summaries. Sticker image files remain in instance directories and must also be retained when backing up application data.

Backup and restore boundaries:

- Export a full instance ZIP, `setting.json` or `memory.json`. ZIP archives and their expanded data are each limited to 512 MiB.
- Exports clear secrets and credential sources, and omit Embedding vectors.
- A full restore creates a **new disabled instance**. Configure credentials and adapters before enabling it.
- Backups include only referenced providers/models. Restore or copy reuses matching shared definitions and existing credentials. Conflicting definitions receive new IDs and empty credentials, with imported routes remapped; other instances and existing shared credentials remain unchanged.

## Before upgrading

Back up complete application data, external PostgreSQL and credential sources first. Instance ZIPs omit secrets and are not full rollback backups. Rollback requires both the old binary and its pre-upgrade data; do not connect an old binary to an upgraded database. See the [upgrade and rollback steps](releasing.md#升级备份与回退) and [Docker volume handling](docker.en.md#upgrades-and-data).

- Legacy JSON and `.env` runtime data are not imported automatically. Back them up before replacing older versions.
- SQL schemas upgrade transactionally in order from v7. Databases below v7 or newer than the running binary supports are rejected without modification.
- Upgrading v7–v10 provider catalogs remaps conflicting IDs, names and routes, retaining old tables as migration archives. Duplicate Windows credential Endpoint IDs caused by manual database edits stop the upgrade without overwriting credentials.

See the [development guide](development.md) for schema rules and manual recovery of legacy JSON group directories.

## Management authentication

Ordinary builds require a control token for remote management APIs, including settings, backups, logs, instances and database selection. Set **管理控制令牌** locally in Bot server settings, then enter the same token in the console's **前端设置**.

- `MCP_CONTROL_TOKEN` takes priority over compatible `ADMIN_TOKEN`. `ALLOW_REMOTE_MCP_MANAGEMENT` does not bypass management authentication.
- Loopback access is token-free unless `MCP_ENFORCE_LOCAL_TOKEN` is enabled.
- Reverse proxies must preserve the public Host or forwarding metadata. If they rewrite Host to localhost and strip all forwarding metadata, enable `MCP_ENFORCE_LOCAL_TOKEN` to prevent remote requests from using the local exemption.

The isolated Docker deployment uses Cloudflare Access and needs no extra local login by default; explicit local-token enforcement still applies. Read the [Docker security requirements](docker.en.md) before deployment.
