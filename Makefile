SHELL := /bin/bash

PNPM ?= pnpm
NG ?= $(PNPM) exec ng
ESLINT ?= $(PNPM) exec eslint
BUF ?= buf
GO ?= go
TOOLS_BIN := $(CURDIR)/.tools/bin
PROTOC_GEN_GO := $(TOOLS_BIN)/protoc-gen-go
PROTOC_GEN_CONNECT_GO := $(TOOLS_BIN)/protoc-gen-connect-go

export PATH := $(TOOLS_BIN):$(CURDIR)/node_modules/.bin:$(PATH)

.DEFAULT_GOAL := build

.PHONY: build build-api build-web build-web-dev
.PHONY: dev serve-api serve-agent serve-web
.PHONY: proto-generate proto-generate-go proto-generate-web proto-tools
.PHONY: lint test test-api test-web vet extract-i18n clean ci

build: build-api

build-api: build-web proto-generate-go
	mkdir -p ./bin
	$(GO) build -o ./bin/app ./cmd/app
	$(GO) build -o ./bin/agent ./cmd/agent

build-web: proto-generate-web
	$(NG) build web

build-web-dev: proto-generate-web
	$(NG) build web --configuration development

dev: proto-generate-go build-web-dev
	@set -euo pipefail; \
	$(GO) run ./cmd/app & api_pid=$$!; \
	$(NG) serve web --configuration development & web_pid=$$!; \
	trap 'kill $$api_pid $$web_pid 2>/dev/null || true' INT TERM EXIT; \
	wait -n $$api_pid $$web_pid; \
	status=$$?; \
	kill $$api_pid $$web_pid 2>/dev/null || true; \
	exit $$status

serve-api: build-web proto-generate-go
	$(GO) run ./cmd/app

serve-agent: proto-generate-go
	$(GO) run ./cmd/agent

serve-web: proto-generate-web
	$(NG) serve web --configuration development

proto-generate: proto-generate-go proto-generate-web

proto-tools: $(PROTOC_GEN_GO) $(PROTOC_GEN_CONNECT_GO)

$(PROTOC_GEN_GO):
	mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11

$(PROTOC_GEN_CONNECT_GO):
	mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) $(GO) install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0

proto-generate-go: proto-tools
	$(BUF) generate --template buf.gen.yaml

proto-generate-web:
	$(BUF) generate --template buf.gen.web.yaml

lint:
	$(ESLINT) .

test: test-api test-web

test-api: build-web proto-generate-go
	$(GO) test ./...

test-web: proto-generate-web
	$(NG) test web --watch=false

vet: build-web proto-generate-go
	$(GO) vet ./...

extract-i18n:
	$(NG) extract-i18n web

clean:
	rm -rf ./bin ./gen ./dist ./internal/frontend/dist ./.angular/cache ./.nx ./.tools

ci: build test lint vet
