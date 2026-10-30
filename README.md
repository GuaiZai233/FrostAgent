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

```bash
docker compose up -d --build
```

The default deployment does not publish the management panel. For local access
on Windows Docker Desktop (Linux containers) or Linux Engine, explicitly add:

```powershell
docker compose -f compose.yaml -f compose.local.yaml up -d --build
```

Open **http://127.0.0.1:8088**. Two separate relays connect through a private Unix
socket; the FA image does not contain socat and no management port is added to
the adapter network. Preserve the existing project name, environment files and
data volume. See [Local Docker panel](docs/docker-local-panel.md) for ports,
trust boundaries, upgrades and disabling the entry point. Use this mode only on
a trusted local host; remote access still requires Cloudflare Access.

GHCR retains all versioned releases, the current `main` / `latest` image, and
the 10 most recent SHA-tagged commit images. Older historical commit images are
automatically removed. See the [image retention policy](.github/ghcr-retention.md)
for cleanup previews and recovery.

## Adapters

### WebSocket

Enable a reverse WebSocket client in your local OneBot implementation (such as NapCat) and set its URL to:

`ws://127.0.0.1:1023/instances/<instance-id>/ws/onebot`

The port depends on the listening address configured on the Settings page.

### Integration with Frameworks Such as AstrBot

With the appropriate adapter, FrostAgent can replace the LLM response module in agent frameworks such as AstrBot without affecting their existing plugin ecosystems.

You can connect to FrostAgent using the [astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) plugin.

The default FrostAgent WebSocket endpoint is:

`ws://127.0.0.1:1234/instances/<instance-id>/ws/astrbot`

Configure communication between AstrBot and the upstream service. Once configured, FrostAgent can take over message handling.

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
