# FrostAgent

一个 AI Agent 编排 + API 中间件服务，基于 Golang。

[English](README.md) | [中文](README_zh_CN.md)

[![Go Version](https://img.shields.io/badge/Go-1.25.3+-blue.svg)](https://go.dev)
[![CI Status](https://img.shields.io/badge/CI-Passing-brightgreen.svg)](https://github.com/GuaiZai233/FrostAgent/actions)
[![License](https://img.shields.io/badge/License-MPL%202.0-orange.svg)](https://github.com/GuaiZai233/FrostAgent/LICENSE)

## 提示

项目仍处于早期阶段，供个人研究使用。欢迎大佬们 PR 并指导！

## 快速开始

### 1. 构建项目

```bash
go build -o frostagent.exe
```

### 2. 配置环境变量

创建 `.env` 文件或在系统环境变量中设置：

```bash
# 上游 API 端点 (比如: 阿里云通义千问)
set UPSTREAM_ENDPOINT=https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions

# 上游 API 密钥
set UPSTREAM_API_KEY=sk-your-api-key-here

# 中间件监听地址 (默认: :8080)
set LISTEN_ADDR=:8080
```

### 3. 启动服务

```bash
go run ./cmd/app
```

## API 使用

### 健康检查

```bash
curl http://localhost:8080/health
```

响应：
```json
{"status":"ok"}
```

### 聊天完成接口

```bash
curl -X POST http://localhost:8080/agent/query \
  -H "Content-Type: application/json" \
  -d '{
    "input": "请告诉我北京的天气。"
  }'
```

## 自定义上游服务

FrostAgent 可以代理到任何 OpenAI 兼容的 API 端点，修改环境变量即可切换上游服务。

## 路由列表

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| POST | `/agent/query` | 聊天完成接口 |

## 环境变量

| 变量 | 说明 | 默认值                                                                  |
|------|------|----------------------------------------------------------------------|
| `UPSTREAM_ENDPOINT` | 上游 API 端点 URL | `https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions` |
| `UPSTREAM_API_KEY` | 上游 API 认证密钥 | `sk-xxx`                                                             |
| `LISTEN_ADDR` | 中间件服务监听地址 | `:8080`                                                              |

## 许可证

MPL-2.0 (see LICENSE file)

