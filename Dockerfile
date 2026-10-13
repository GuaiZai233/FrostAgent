# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27
ARG NODE_VERSION=22
ARG BUF_VERSION=1.46.0

FROM bufbuild/buf:${BUF_VERSION} AS buf

FROM node:${NODE_VERSION}-bookworm-slim AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json ./apps/web/package.json
COPY libs/frostagent-proto/package.json ./libs/frostagent-proto/package.json
COPY patches ./patches
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY --from=buf /usr/local/bin/buf /usr/local/bin/buf
COPY buf.yaml buf.gen.web.yaml tsconfig.base.json ./
COPY api/proto ./api/proto
COPY apps/web ./apps/web
COPY libs/frostagent-proto ./libs/frostagent-proto
RUN pnpm exec buf generate --template buf.gen.web.yaml \
    && pnpm --filter @frostagent/web run build

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY --from=buf /usr/local/bin/buf /usr/local/bin/buf
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 \
    && go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0
COPY buf.yaml buf.gen.yaml ./
COPY api/proto ./api/proto
RUN buf generate --template buf.gen.yaml
COPY cmd/app ./cmd/app
COPY internal ./internal
COPY subagent ./subagent
COPY --from=web /src/internal/frontend/dist ./internal/frontend/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X FrostAgent/internal/instanceconfig.DefaultListenAddr=0.0.0.0:8080 -X FrostAgent/internal/instanceconfig.DefaultWSListenAddr=0.0.0.0:1234" \
    -o /out/frostagent ./cmd/app

FROM debian:bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 frostagent \
    && useradd --uid 10001 --gid frostagent --create-home --shell /usr/sbin/nologin frostagent \
    && install -d -o frostagent -g frostagent -m 0700 /app/data
WORKDIR /app
COPY --from=build /out/frostagent /usr/local/bin/frostagent
USER 10001:10001
EXPOSE 8080 1234
VOLUME ["/app/data"]
ENTRYPOINT ["/usr/local/bin/frostagent"]
