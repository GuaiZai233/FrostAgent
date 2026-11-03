# Local Docker panel

Use `compose.local.yaml` explicitly to open the management panel on a trusted
single-user Docker host. It uses the normal bridge-network FA image; socat
is **not** installed in that image. Windows Docker Desktop must use **Linux
containers**. No Desktop host-networking option, Windows bind mount, Cloudflare
Tunnel, administrator account, or application authentication change is required.

> **Trust every container on this Docker host.** This opt-in mode trusts local
> users, processes, Docker administrators and **all local containers**, including
> ordinary containers on other bridges. It provides convenient, unauthenticated
> local management unless an existing explicit token policy requires a token;
> it does **not** isolate management from other containers. On 2026-11-03, the
> live smoke on Docker Desktop 4.55.0, Engine 29.1.3 and WSL 2 observed HTTP 200
> from an adapter-network container via both the panel container IP and
> `host.docker.internal` at the published port, despite `enable_icc=false`.
> Host loopback publication is not a container-isolation guarantee. Do not use
> this mode on a host running untrusted containers.

## Prerequisites

- Docker Engine **28 or newer**. Older versions have a known localhost port
  publishing issue affecting peers on the same L2 network. See
  [Docker port publishing](https://docs.docker.com/engine/network/port-publishing/).
- Docker Compose CLI/plugin **2.17.0 or newer** (`docker compose`). The overlay
  uses `depends_on.<service>.restart: true` for Compose-managed restart
  propagation, which was introduced in
  [Compose 2.17.0](https://docs.docker.com/reference/compose-file/services/#depends_on).
  The Compose plugin version is independent of the Engine version; Engine 28+
  alone does not satisfy both requirements.

Check both versions before enabling the overlay:

```powershell
docker version --format '{{.Server.Version}}'
docker compose version --short
```

## Enable

Run these commands from the **existing deployment directory**. Keep its original
Compose project name (`-p`/`COMPOSE_PROJECT_NAME`), environment files and any other
required overrides. The first file must remain the existing `compose.yaml` (or
its actual filename). Changing the project name can select a different data
volume and make existing data appear missing.

```powershell
docker compose -f compose.yaml -f compose.local.yaml config --quiet
docker compose -f compose.yaml -f compose.local.yaml up -d --build
```

Open **http://127.0.0.1:8088** on the Docker host. Use HTTP, not HTTPS. The adapter's
host mapping and container port remain those in your base Compose file;
same-default-network OneBot/AstrBot clients use `frostagent:<adapter target port>`.
The panel port is not the adapter port.

The commands build the existing local FA image when needed and pull the pinned
socat image. They reuse `frostagent-data:/app/data`; there is no data migration.
For an image-only deployment, retain its original image-only Compose file and
omit `--build`. Do not replace its image or add a source build just for the panel.

If you used the temporary single-socat workaround, stop and remove **that relay
service only**, and remove the `8088:18080`/`8088:8080` management mapping from FA
before enabling this file. Keep the adapter mapping and the original data volume.
Otherwise the old relay remains an extra management path or occupies port 8088.

### Ports and existing SQL settings

The optional variables below configure the relays, not FrostAgent itself:

| Variable | Default | Meaning |
| --- | --- | --- |
| `FROSTAGENT_PANEL_PORT` | `8088` | Host port, always published on IPv4 `127.0.0.1` |
| `FROSTAGENT_PANEL_TARGET_PORT` | `8080` | Existing FA management listener's port on container IPv4 `127.0.0.1` |

For example, if your saved SQL `LISTEN_ADDR` is already `127.0.0.1:8081`:

```powershell
$env:FROSTAGENT_PANEL_PORT = '8088'
$env:FROSTAGENT_PANEL_TARGET_PORT = '8081'
docker compose -f compose.yaml -f compose.local.yaml up -d
```

Alternatively, put these two variables in the deployment's existing Compose
environment file. Continue using the same values/files for subsequent commands.
The target must be a numeric TCP port from 1 to 65535. This overlay never edits
SQL, does not override a saved `LISTEN_ADDR`, and only connects to `127.0.0.1`.
Do not change FA to `0.0.0.0` or disable `ManagementLoopbackOnly`.

Existing `MCP_ENFORCE_LOCAL_TOKEN` settings remain enforced. If explicitly enabled
with a control token, management API calls without the appropriate Bearer token
still return `401`; this deployment does not inject, disclose or disable tokens.
Host/Origin validation also stays enabled. Do not expand `HTTP_ALLOWED_ORIGINS`
merely to open the local `127.0.0.1` URL.

## Topology and trust boundary

```text
host 127.0.0.1:8088 -> local-panel (dedicated bridge, TCP 8080)
                      -> private Unix socket volume
                      -> local-panel-origin (shares FA network namespace)
                      -> FA 127.0.0.1:8080
```

The origin relay opens **only a Unix socket**, not a TCP management listener on
FA's default adapter network. The ingress relay has its own dedicated bridge with
`enable_icc=false` as defense in depth, not a container-isolation guarantee.
It is not an `internal: true` network,
because Docker Engine skips host port publishing on internal networks.
Do not attach adapters or other services
to that bridge. The socket directory belongs to UID/GID 10001 with mode 0700;
the socket is mode 0600. Only the two non-root, capability-dropped relays mount
this tmpfs-backed named volume; the ingress mount is read-only. Neither relay
mounts FA's data or the Docker socket. The default `compose.yaml` is unchanged.

This is **local access, not a public authentication gateway**. Do not remove the
host `127.0.0.1` binding, publish the panel through a reverse proxy/tunnel, enable
direct routing, or reuse these networks with a different firewall policy. Use
the existing Cloudflare Access deployment for remote access. Local users,
processes, Docker administrators and all containers on this host are trusted.
Ordinary bridge-network containers may reach the management API through the
relay or Docker Desktop's host forwarding without administrative privileges.
Someone who can control Docker can additionally join namespaces or mount the
socket volume. An unreachable peer route on one engine/version does not narrow
this trust boundary. The loopback host binding must still be retained.

The shared volume uses the local driver's tmpfs mount options supported by Linux
and Docker Desktop; it is not a Windows-host bind mount or a per-container
`tmpfs:` mount. See [Docker volume options](https://docs.docker.com/reference/cli/docker/volume/create/).

## Restart, upgrade and disable

Keep the local override in subsequent deployment commands. When replacing the FA
container (including image updates), recreate both relays with it so the origin
joins the new network namespace:

```powershell
docker compose -f compose.yaml -f compose.local.yaml up -d --build --force-recreate frostagent local-panel-origin local-panel
```

Even if FA's container ID stays the same, Docker can replace its network
namespace when FA stops and starts. [`depends_on.restart`](https://docs.docker.com/reference/compose-file/services/#depends_on) assists explicit
Compose-managed restarts; it does not propagate Docker's automatic restarts or
an out-of-band `docker restart` to the relays. After those events, the panel may
remain unavailable until both relays are restarted against the running FA:

```powershell
docker compose -f compose.yaml -f compose.local.yaml restart local-panel-origin local-panel
```

This recovery command does not restart FA or remove its data. If FA was replaced
with a different container instead, use the full recreation command above.
The origin removes any stale socket pathname on startup. If the origin or FA is
unavailable, panel connections fail closed; no alternate upstream is used.

To close the local entry point without stopping FA or its adapters:

```powershell
docker compose -f compose.yaml -f compose.local.yaml stop local-panel local-panel-origin
```

To disable it permanently, remove **only those two stopped relay containers**
before omitting the override from future commands:

```powershell
docker compose -f compose.yaml -f compose.local.yaml rm -f local-panel local-panel-origin
```

Simply omitting the override does not remove already-running relay containers.
The extra socket volume contains no application data and may be left in place.
Never use `down -v` to disable the panel; it would remove the application volume.

## Verification and troubleshooting

```powershell
docker compose -f compose.yaml -f compose.local.yaml ps
curl.exe -i http://127.0.0.1:8088/api/instances
docker compose -f compose.yaml -f compose.local.yaml logs --tail 50 local-panel local-panel-origin
```

An empty response/connection reset usually means the Unix socket or target
listener is unavailable: check that all three containers are running, that the
target port matches SQL, and that both relays were recreated with FA. `401` is an
existing token requirement, not a reason to disable authentication. `403` is a
Host/Origin rejection; first use the exact local URL above.

Repository verification uses a separate random Compose project and synthetic
data, never a live deployment volume. The smoke requires the repository's standard
local named data-volume layout: it rejects application bind/extra mounts,
external volumes, and custom data-volume drivers/options before starting
containers. Run it from an unmodified repository checkout if your deployment
uses custom storage; that restriction applies to the smoke, not the local panel:

```powershell
python scripts/docker_local_panel_smoke.py --config-only
python scripts/docker_local_panel_smoke.py
```

The full smoke requires a running Linux-container engine and a built
`frostagent:local` image. Linux CI exercises real networking, APIs, adapter
handshakes, topology and lifecycle. Peer routes through the ingress container,
bridge gateway and host forwarding are recorded as reachable or unreachable;
both outcomes are allowed for these trusted peers. Once explicit local token
enforcement is enabled, every reachable peer route must reject missing/wrong
tokens and accept the correct token. The probe first sends no token: `401`
establishes reachability and protection, `200` fails as a token bypass, and only
transport status `0` is classified as unreachable. After `401`, a correct-token
request must return `200` and a wrong-token request must return `401`; transport
failure on either request fails the smoke instead of skipping the route.
Unreachable routes are reported as not exercised for authentication, not as
proof of isolation. Run the same smoke on
Windows Docker Desktop to validate that version's host forwarding, token policy
and lifecycle; Linux CI cannot replace that check. No public Tunnel or real IM
account is used.
