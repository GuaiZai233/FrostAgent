# FrostAgent

FrostAgent is an AI role-playing and agent orchestration framework written in Golang. It supports adapters for multiple protocols, including OneBot, and can be integrated with instant messaging applications.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

## Collaboration with ActionsCat

[ActionsCat](https://github.com/actionscat/actionscat) supports statically orchestrated automated workflows.

After connecting ActionsCat through an adapter, you can run both projects side by side and take advantage of the agent's capabilities.

Note that development of ActionsCat has been suspended. You may choose another solution to provide a plugin ecosystem for your bot.

## Collaboration with Frameworks Such as AstrBot

When used with an adapter, FrostAgent can replace the LLM response module in agent frameworks such as AstrBot without affecting their rich plugin ecosystems.

## Quick Start

### 1. Build the Project

This project uses a root `Makefile` for build orchestration.

```bash
# Install Node.js dependencies (Angular toolchain, etc.)
# This project uses pnpm as the package manager
pnpm install

# Install buf for protobuf code generation
go install github.com/bufbuild/buf/cmd/buf@latest

# Build everything - backend Go binaries + frontend Angular app
make build
```

Compiled backend binaries are placed in `./bin/` and the frontend static assets go to `internal/frontend/dist/`.

You can also build individual parts:

```bash
make build-api    # Backend and embedded frontend
make build-web    # Frontend only
```

### 2. Configure Environment Variables

Create a `.env` file or configure the relevant fields as system environment variables.

### 3. Start the Service

```bash
go run ./cmd/app
```

## License

MPL-2.0 (see LICENSE file)
