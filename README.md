# FrostAgent

An AI Agent orchestration + API middleware service built with Golang.

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

## Notice

This project is still in early stages and is intended for personal research use. We welcome PRs and guidance from everyone!

## Collaboration with ActionsCat
[ActionsCat](https://github.com/actionscat/actionscat) supports the automation of static orchestration workflows.

After integrating ActionsCat into the adapter, you can operate both simultaneously, and the agent will demonstrate its outstanding capabilities.

## Quick Start

### 1. Build the Project

```bash
go build -o frostagent.exe
```

### 2. Configure Environment Variables

Create a `.env` file or set system environment variables:

```bash
# Upstream API endpoint (e.g., Alibaba Cloud Tongyi Qianwen)
set UPSTREAM_ENDPOINT=https://dashscope.aliyuncs.com/compatible-mode/v1

# Upstream API key
set UPSTREAM_API_KEY=sk-your-api-key-here

# Middleware listening address (default: :8080)
set LISTEN_ADDR=:8080
```

### 3. Start the Service

```bash
go run ./cmd/app
```

## API Usage

### Health Check

```bash
curl http://localhost:8080/health
```

Response:
```json
{"status":"ok"}
```

### Chat Completion Endpoint

```bash
curl -X POST http://localhost:8080/agent/query \
  -H "Content-Type: application/json" \
  -d '{
    "input": "Tell me about the weather in Beijing."
  }'
```

Multi-context / chat history is also supported. `input` is optional when `messages` is provided; if both are provided, `input` is appended as the latest user message.

```bash
curl -X POST http://localhost:8080/agent/query \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "user", "content": "My name is Frost."},
      {"role": "assistant", "content": "Nice to meet you, Frost."},
      {"role": "user", "content": "What is my name?"}
    ]
  }'
```

## Custom Upstream Service

FrostAgent can proxy to any OpenAI-compatible API endpoint. Simply modify the environment variables to switch upstream services.

## Routes

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| POST | `/agent/query` | Chat completion endpoint |

## Environment Variables

| Variable | Description | Default Value                                                        |
|----------|-------------|----------------------------------------------------------------------|
| `UPSTREAM_ENDPOINT` | Upstream API endpoint URL | `https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions` |
| `UPSTREAM_API_KEY` | Upstream API authentication key | `sk-xxx`                                                             |
| `LISTEN_ADDR` | Middleware service listening address | `:8080`                                                              |
| `MAX_CONTEXT_MESSAGES` | Max messages kept in context, including system prompt | `20` |
| `MAX_CONTEXT_CHARS` | Approximate max context characters before trimming | `24000` |

## License

MPL-2.0 (see LICENSE file)

