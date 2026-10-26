# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

# Keep tags and multi-platform index digests together for Dependabot updates.
# See .github/dependency-maintenance.md before changing these references.

FROM bufbuild/buf:1.75.0@sha256:adf82c7170be6629ec81dc907921ac1699407cd11c4f7fd7521b13c74152f849 AS buf

FROM node:26-bookworm-slim@sha256:28147155d47dc31abe0a46e0d12b59b2fb3d0a2dcc5dc65fce76a074180b16fe AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json ./apps/web/package.json
COPY libs/frostagent-proto/package.json ./libs/frostagent-proto/package.json
COPY patches ./patches
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY --from=buf /usr/local/bin/buf /usr/local/bin/buf
COPY buf.yaml buf.gen.web.yaml ./
COPY api/proto ./api/proto
COPY apps/web ./apps/web
COPY libs/frostagent-proto ./libs/frostagent-proto
RUN pnpm exec buf generate --template buf.gen.web.yaml \
    && pnpm --filter @frostagent/web run build

FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
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
ARG DOCKER_WS_LISTEN_ADDR=0.0.0.0:1234
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/GuaiZai233/FrostAgent/internal/instanceconfig.DefaultListenAddr=127.0.0.1:8080 -X github.com/GuaiZai233/FrostAgent/internal/instanceconfig.DefaultWSListenAddr=${DOCKER_WS_LISTEN_ADDR} -X github.com/GuaiZai233/FrostAgent/internal/instanceconfig.ManagementLoopbackOnly=true" \
    -o /out/frostagent ./cmd/app

FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 frostagent \
    && useradd --uid 10001 --gid frostagent --create-home --shell /usr/sbin/nologin frostagent \
    && install -d -o frostagent -g frostagent -m 0700 /app/data
WORKDIR /app
COPY --from=build /out/frostagent /usr/local/bin/frostagent
USER 10001:10001
EXPOSE 1234
VOLUME ["/app/data"]
ENTRYPOINT ["/usr/local/bin/frostagent"]
