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
  <a href="https://go.dev">
    <img src="https://img.shields.io/badge/Go-1.27.3+-blue.svg" alt="Go Version">
  </a>
  <a href="https://github.com/GuaiZai233/FrostAgent/actions">
    <img src="https://img.shields.io/badge/CI-Passing-brightgreen.svg" alt="CI Status">
  </a>
  <a href="LICENCE">
    <img src="https://img.shields.io/badge/License-MPL%202.0-orange.svg" alt="License">
  </a>
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

### 2. Choose a Database

SQLite is used by default. On the first startup, FrostAgent creates `data/frostagent.db`. PostgreSQL is also supported.

Switching databases does not automatically migrate data. If migration is required, back up and restore the data manually. Only one FrostAgent process may use the same PostgreSQL database at a time.

### 3. Start the Service

```bash
go run ./cmd/app
```

Open [http://localhost:8080](http://localhost:8080) in your browser. No instance is created automatically on the first startup, so you must create one manually.

## Docker Deployment

For the first deployment, copy `docker.env.example` to `.env` and set `FROSTAGENT_BOOTSTRAP_CONTROL_TOKEN` to a random ASCII token of at least 32 characters before starting:

```bash
docker compose up -d --build
```

Open [http://127.0.0.1:8080/#/settings/frontend](http://127.0.0.1:8080/#/settings/frontend) and save the same token under "API / Control Plane Access Credentials".

## Adapters

### WebSocket Connection Modes

After creating an instance, configure the connection on the "Connections" page.

- **Reverse WebSocket (FrostAgent as server)**: The upstream connects to FrostAgent. Enable a reverse WebSocket client in the upstream messaging platform implementation and paste the copied OneBot URL, for example `ws://127.0.0.1:11023/instances/<instance-id>/ws/onebot`.
- **Forward WebSocket (FrostAgent as client)**: The upstream provides a WebSocket server. Enter its `ws://` or `wss://` URL and optional access token on the FrostAgent "Connections" page.

### Integration with Frameworks Such as AstrBot

With the appropriate adapter, FrostAgent can replace the LLM response module in agent frameworks such as AstrBot without affecting their existing plugin ecosystems.

You can connect to FrostAgent using the [astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) plugin.

**Note:** Disable AstrBot's built-in LLM functionality to avoid conflicts.

## Additional Features

### Integration with ActionsCat

[ActionsCat](https://github.com/actionscat/actionscat) supports statically orchestrated automated workflows.

After connecting ActionsCat through an adapter, you can run both systems alongside each other, combining static workflow orchestration with the capabilities of AI agents.

### Integration with Alcyone

[Alcyone](https://github.com/GuaiZai233/Alcyone) is a billing system backend developed by GuaiTech. It helps track usage and calculate charges when bots operate in public settings, such as group chats, making resource consumption, including token usage, measurable.

### Integration with Code-interpreter

[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) is a lightweight sandbox runtime based on [Foxerine](https://github.com/Foxerine/code-interpreter), enabling bots to execute simple Shell commands and similar operations.

## License

MPL-2.0 (see [LICENCE](LICENCE)).
