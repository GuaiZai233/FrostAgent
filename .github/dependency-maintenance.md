# Maintaining build dependency pins

Pinning prevents an upstream tag move from silently changing our builds. It does
not replace reviewing dependency updates, and it does not make every build input
reproducible (for example, Debian packages are still installed from apt).

## Actions and container images

- Every external GitHub Action in `.github/workflows/` uses a full 40-character
  commit SHA. The adjacent release comment is for humans and Dependabot; it is
  not the reference executed by GitHub.
- All four Dockerfile base images and the Dockerfile frontend use SHA-256
  digests. Keep each readable tag and multi-platform index digest together.
- The maintainer selected explicit `FROM image:tag@sha256:digest` references
  (Option A) so Dependabot can maintain the base images. The old `GO_VERSION`,
  `NODE_VERSION`, and `BUF_VERSION` build arguments have been removed: custom
  builds must now update the tag and digest in `FROM` instead. This is an
  intentional build-time compatibility change; default image versions and
  distributions are unchanged. `DOCKER_WS_LISTEN_ADDR` remains configurable.
- The PostgreSQL CI service is also pinned to its current `pg17` index digest.
- Both optional local-panel relays in `compose.local.yaml` use the same tagged
  socat multi-platform index digest. Update them together and rerun the local
  panel smoke. The dedicated `docker-compose` Dependabot entry tracks only
  `alpine/socat`, without changing the existing application/connector image policy.
- `.github/dependabot.yml` schedules weekly PRs for Actions and Docker base images.
  Review and merge these regularly: a frozen but unmaintained
  dependency is still a security risk. Updates are not automatically merged.
  The configuration takes effect after it reaches the default branch.
- Dependabot does not maintain Go commands embedded in workflows or Makefiles.
  Review those pins, the Dockerfile frontend, and the CI service image alongside
  dependency updates; do not assume every reference is covered by the bot.

Verify an Action's release in its upstream repository and resolve annotated tags
to the commit (not the tag object). Resolve container digests from the registry:

```sh
docker buildx imagetools inspect node:22-bookworm-slim
```

Use the top-level multi-platform index digest, not an amd64-only manifest, and
check that the expected platforms remain present.
Do not change an Action's major, an image's distribution, or the Go toolchain
while merely refreshing its immutable reference.

## Go tools

All remote `go install` commands use explicit module versions, verified through
Go's module checksum infrastructure; do not use `@latest`, branch names, or
disable checksum verification. The existing protoc generators remain at
`protoc-gen-go@v1.36.11` and `protoc-gen-connect-go@v1.20.0` in the Makefile,
Dockerfile, and Windows CI.

CI and the README installation commands freeze the previously floating buf
selection at `v1.75.0`. Docker retains its existing `1.46.0` version. Aligning these
two versions is a separate tool upgrade, not a prerequisite for pinning. When
updating a tool, review all its install sites and run Go and web proto generation,
builds, and the corresponding tests.

## Corepack

The Node 26 web build image does not bundle Corepack. Install the explicit
Corepack version in the Dockerfile before enabling it; pnpm remains selected by
the root `package.json` `packageManager` field. Review this Corepack pin manually
alongside Node image updates, since Dependabot does not maintain embedded npm
install commands. Verify with the dependency pin tests and Docker build/smoke.

## Verification

```sh
python3 -m unittest discover -s scripts -p 'test_dependency_pins.py' -v
```

The CI regression checks cover external Action references, Dockerfile base images
and the frontend, workflow service images, and explicit remote Go tool versions in
workflows, the Dockerfile, Makefile, and quick-start instructions. These are
checks for the repository's line-oriented declarations, not a general-purpose
YAML, Dockerfile, or shell security parser. They do not verify upstream provenance
or transitive downloads inside Actions. The Docker checks also verify that
removing the digest from any current base image fails validation, while local
stage references and `scratch` are not mistaken for external dependencies.

After updates, run the regular CI and Docker build/smoke workflows. Scorecard runs
on the default branch and its normal schedule; a PR alone does not update the
published score.
