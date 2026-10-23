# FrostAgent

FrostAgent is an AI role-playing and agent orchestration framework written in Go. It supports protocols such as OneBot and integrates with instant messaging applications.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.27.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](LICENCE)

## Releases

Version 1.0.0 is prepared as a source-only release. The version in `package.json` does not mean it has been published; check [GitHub Releases](https://github.com/GuaiZai233/FrostAgent/releases) for available releases. Once published, download the source archive or check out the `v1.0.0` tag, then build locally. Prebuilt binaries and registry-hosted container images are deferred; the repository's Dockerfile and Compose configuration continue to build from source.

See the [1.0.0 release notes](docs/releases/v1.0.0.md) and the [source installation, upgrade, and maintainer release guide](docs/releasing.md). The three private workspace packages share the project version, while the AstrBot adapter retains its independent plugin version.

## Adapters

### WebSocket

Enable a reverse WebSocket client in your local upstream OneBot implementation, such as NapCat, and set its URL to `ws://127.0.0.1:1023/instances/<instance-id>/ws/onebot` (the port depends on the listen address configured on the settings page).

### Integration with AstrBot and Similar Frameworks

Through an adapter, FrostAgent can replace the LLM response module of agent frameworks such as AstrBot while preserving their plugin ecosystems.

Use the [astrbot_plugin_frostagent](adapters/astrbot_plugin_frostagent) plugin to connect AstrBot. The default FrostAgent endpoint is `ws://127.0.0.1:1023/instances/<instance-id>/ws/astrbot`. Configure communication between AstrBot and its upstream platform, and FrostAgent can take over message processing. **Note: Disable AstrBot's built-in LLM functionality!**

## Quick Start

### 1. Build the Project

The root `Makefile` orchestrates the build. See the [development guide](docs/development.md) for local development and verification commands.

```bash
pnpm install
go install github.com/bufbuild/buf/cmd/buf@latest
make build
```

### 2. Choose a Database

SQLite is used by default, creating `data/frostagent.db` on first start. PostgreSQL is also supported. Switching databases does not automatically migrate data; back up and restore manually if migration is needed. Only one FrostAgent process may use a given PostgreSQL database at a time.

### 3. Start the Service

```bash
go run ./cmd/app
```

Open `http://localhost:8080`. No instances are created on first start; create them manually.

On Linux systems that restrict ports below 1024, build the binary and grant it permission to bind the default adapter port: `sudo setcap cap_net_bind_service=+ep ./bin/app`. Then start `./bin/app` instead of `go run ./cmd/app`. Reapply the capability after rebuilding. The Docker image already grants this capability while retaining its non-root user.

## Docker Deployment

```bash
docker compose up -d --build
```

The deployment uses host networking and is intended for Linux; Docker Desktop requires host networking to be enabled first. With a new database, the default management URL is `http://localhost:8080`, and adapters listen on `127.0.0.1:1023`. Existing SQL listener settings take precedence, so check listen addresses before upgrading from a bridge deployment. Data remains in the `frostagent-data` volume.

We recommend placing **Cloudflare Access** in front of the service for management authentication. Remote requests still require the application's management control token.

## Additional Features

### Integration with ActionsCat

[ActionsCat](https://github.com/actionscat/actionscat) supports static orchestration of automated workflows.

After connecting ActionsCat through the adapter, you can run both systems in parallel to extend the agent's capabilities.

### Integration with Alcyone

[Alcyone](https://github.com/GuaiZai233/Alcyone) is a billing backend developed by GuaiTech. It helps bots track usage and billing in shared environments such as group chats, accounting for tokens and other resource consumption.

### Integration with Code-interpreter

[Code-interpreter](https://github.com/GuaiZai233/code-interpreter) is a lightweight sandbox runtime that lets bots execute simple code.

## License

MPL-2.0 (see [LICENCE](LICENCE))
