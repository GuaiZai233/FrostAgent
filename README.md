<h1 align="center">
  <img src="assets/logo.png" alt="FrostAgent Logo" width="150">
  <br>
  FrostAgent
</h1>

<p align="center">
FrostAgent is an AI role-playing and agent orchestration framework written in Go. It supports multiple protocols, including OneBot, and can be integrated with instant messaging platforms.
</p>

<p align="center">
  <a href="README.md">English</a> |
  <a href="README_zh_CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://go.dev"><img src="https://img.shields.io/badge/Go-1.27.0+-blue.svg" alt="Go Version"></a>
  <a href="https://github.com/GuaiZai233/FrostAgent/actions"><img src="https://img.shields.io/badge/CI-Passing-brightgreen.svg" alt="CI Status"></a>
  <a href="LICENCE"><img src="https://img.shields.io/badge/License-MPL%202.0-orange.svg" alt="License"></a>
  <a href="https://pkg.go.dev/github.com/GuaiZai233/FrostAgent"><img src="https://pkg.go.dev/badge/github.com/GuaiZai233/FrostAgent.svg" alt="Go Reference"></a>
  <a href="https://codecov.io/gh/GuaiZai233/FrostAgent"><img src="https://codecov.io/gh/GuaiZai233/FrostAgent/branch/main/graph/badge.svg" alt="Codecov Coverage"></a>
  <a href="https://scorecard.dev/viewer/?uri=github.com/GuaiZai233/FrostAgent"><img src="https://api.scorecard.dev/projects/github.com/GuaiZai233/FrostAgent/badge" alt="OpenSSF Scorecard"></a>
</p>

## Quick Start

### 1. Build the Project

This project uses the `Makefile` in the repository root to orchestrate the build process. For local development and verification commands, see the [Development Guide](docs/development.md).

```bash
pnpm install
go install github.com/bufbuild/buf/cmd/buf@v1.75.0
make build
```

CI Actions and Docker base images are pinned to immutable references. Dependabot
opens weekly update PRs; review and merge them regularly so pinned versions do not
become stale. See [dependency maintenance](.github/dependency-maintenance.md) for
the Go tool versions and update checks.

### 2. Choose a Database

SQLite is used by default. On the first startup, FrostAgent creates `data/frostagent.db`. PostgreSQL is also supported.

Switching databases does not automatically migrate data. If migration is required, back up and restore the data manually. Only one FrostAgent process may use the same PostgreSQL database at a time.

### 3. Start the Service

```bash
go run ./cmd/app
```

Open [http://localhost:8080](http://localhost:8080) in your browser. No instance is created automatically on the first startup, so you must create one manually.

On Linux, if the system restricts non-root processes from binding to ports below 1024, build the application first, then run:

```bash
sudo setcap cap_net_bind_service=+ep ./bin/app
```

Start the service using `./bin/app` instead of `go run ./cmd/app`. This capability must be reapplied after each rebuild. The Docker image is already configured with this capability and still runs as a non-root user.

## Docker Deployment

For the first deployment, copy `docker.env.example` to `.env` and set
`FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN` to a random ASCII token of at least 32 characters:

```bash
docker compose up -d --build
```

Open **http://127.0.0.1:8080/#/settings/credentials** (Settings → Access credentials), save the same token under API /
control-plane credentials, then refresh and create or select an instance. The
image listens on container `0.0.0.0:8080`; Compose publishes host
`127.0.0.1:8080` directly, without socat. Management APIs require a valid token
over published ports or bridge networks, even with a spoofed localhost Host.

The bootstrap input only initializes SQL when no control token exists. Existing
`MCP_CONTROL_TOKEN` / `ADMIN_TOKEN` credentials win and are never overwritten by
the environment. Remove the bootstrap input after initialization. Preserve the
project name, data volume and required overrides during updates. See
[Docker deployment and updates](docs/docker.md) for existing loopback settings,
retiring old relays, `docker run`, and image updates. Remote access still uses
Cloudflare Access.

GHCR retains all versioned releases, the current `main` / `latest` image, and
the 10 most recent SHA-tagged commit images. Older historical commit images are
automatically removed. See the [image retention policy](.github/ghcr-retention.md)
for cleanup previews and recovery.

## Adapters

### WebSocket Connection Modes

After creating an instance, open **Connections**, immediately below **Overview** in the sidebar. The OneBot and AstrBot address-copy actions now live here. Each adapter has its own **Enable** checkbox, checked by default. When enabled, it selects exactly one of these modes; existing instances default to reverse mode:

- **Reverse WebSocket (FA as server)**: the upstream connects to FA. Enable a reverse WebSocket client in NapCat or another OneBot implementation and paste the OneBot URL copied from Connections, for example `ws://127.0.0.1:11023/instances/<instance-id>/ws/onebot`. Adjust the reachable host and published port for your deployment.
- **Forward WebSocket (FA as client)**: the upstream runs a WebSocket server. Enter its `ws://` or `wss://` URL and optional access token in Connections. FA sends `Authorization: Bearer <token>` and reconnects automatically. Keep credentials out of the URL.

For reverse mode, set the optional **Inbound authentication token** separately for each adapter (`ONEBOT_WS_INBOUND_TOKEN` / `ASTRBOT_WS_INBOUND_TOKEN`, saved in the selected instance's SQL settings). Once set, every real handshake on both the dedicated WS listener and management listener must send `Authorization: Bearer <token>`; URL/query tokens are not accepted. Use a strong random token containing printable ASCII without spaces, configure the same token in the upstream client, and use `wss://` on untrusted networks. These credentials are independent of outbound `ONEBOT_WS_TOKEN` / `ASTRBOT_WS_TOKEN` and the management control token.

When the inbound token is unset, the dedicated `WS_LISTEN_ADDR` listener retains legacy no-token behavior. **The Docker bridge image's management listener (default 8080) never accepts anonymous real adapter connections**, even through a loopback connector or when `WS_LISTEN_ADDR` is loopback-only: set an inbound token to use that entry point. The enforced-loopback Linux host image and native builds keep their existing no-token behavior when unset. Browser Direct Chat still uses management authentication, not inbound tokens. Default Compose publication remains `127.0.0.1` for both ports; manually exposing the adapter port requires configuring inbound tokens and network/TLS protection first.

Saving applies only to the selected instance and replaces its old connections. Disabling an instance stops dialing and reconnecting. Unchecking an adapter and saving closes its real upstream connections, stops dialing and reconnecting, and rejects new ordinary reverse connections, while retaining its mode, URL and token and preserving browser Direct Chat. Both adapters may be unchecked; missing `ONEBOT_WS_ENABLED` / `ASTRBOT_WS_ENABLED` settings remain enabled for compatibility. In forward mode, ordinary reverse connections to that adapter are rejected. Both modes provide bidirectional messaging after connection; connecting the same upstream to multiple instances can produce duplicate replies.

Inside Docker, `127.0.0.1` refers to the container, not the Windows host. Docker Desktop's `host.docker.internal` can address a host service, provided its listener and firewall permit container access; containers on the same Compose network can use service names. Use `wss://` and an access token across untrusted networks. **Adapter connection modes do not change the HTTP management panel's listener or authentication.**

### Integration with Frameworks Such as AstrBot

With the appropriate adapter, FrostAgent can replace the LLM response module in agent frameworks such as AstrBot without affecting their existing plugin ecosystems.

You can connect to FrostAgent using the [astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) plugin.

Plugin 0.2.1+ supports `ws_reverse_token` in reverse mode: set it to the FA AstrBot inbound token. It is sent only in the Authorization header, including on reconnect, and is separate from the plugin's forward-mode `ws_access_token`.

In reverse mode, paste the AstrBot URL from FA's Connections page into the plugin's `ws_url`. Forward mode requires plugin 0.2.0 or newer: set the plugin's `ws_mode` to `forward`, configure its listener and access token, then enter its reachable URL (for example `ws://plugin-host:1244/`) and the same token in FA. The plugin defaults to loopback; non-loopback listeners require a token, and only one FA connection may be active. Configure communication between AstrBot and the upstream service to let FrostAgent take over message handling.

**Note:** Disable AstrBot's built-in LLM functionality.

## Additional Features

### Per-group Proactive Reply Probability

In **Bot Server Settings → Proactive Replies**, click an added group's name to set its own trigger probability. A small box beside the name shows the override. Confirm the dialog, then save the page to apply it; use the dialog's reset action to inherit the shared probability again.

Negative or invalid input becomes `0.01`, values above `1` become `1`, and `0` disables proactive replies for that group. Unconfigured groups use the shared probability. The master switch and whitelist still apply, while explicit wake-ups are unaffected. Removing a group also removes its probability override.

### Integration with ActionsCat

[ActionsCat](https://github.com/actionscat/actionscat) supports statically orchestrated automated workflows.

After connecting ActionsCat through an adapter, you can run both systems alongside each other, combining static workflow orchestration with the capabilities of AI agents.

### Integration with Alcyone

[Alcyone](https://github.com/GuaiZai233/Alcyone) is a billing system backend developed by GuaiTech. It helps track usage and calculate charges when bots operate in public settings, such as group chats, making resource consumption, including token usage, measurable.

### Integration with Code-interpreter

[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) is a lightweight sandbox runtime that enables bots to execute simple code.

## License

MPL-2.0 (see [LICENCE](LICENCE)).
