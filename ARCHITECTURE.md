# devwrap Architecture

## Purpose

`devwrap` is a local development CLI that:

- Runs an app command with an assigned local app port.
- Registers a host route like `myapp.localhost` (or a custom `--host`) in Caddy.
- Uses Caddy as the reverse proxy and TLS terminator.
- Reuses any existing Caddy Admin API when available.
- Spawns its own embedded Caddy wrapper daemon only when needed.

Core user run shape:

```bash
devwrap --name myapp -- <command...>
devwrap --name myapp --host myapp.dev.test -- <command...>
```

Example:

```bash
devwrap --name opencode -- opencode serve --port @PORT
```

`@PORT` is templated to the assigned app port, and `PORT` env var is also set.

---

## High-Level Components

- `cmd/devwrap/main.go`: process entrypoint.
- `cmd/devwrap/cli.go`: command parsing and top-level flow dispatch.
- `cmd/devwrap/commands.go`: implementations for proxy/list/remove/run process management.
- `cmd/devwrap/client.go`: shared data structures and lease helper entry points.
- `cmd/devwrap/local_state.go`: file-based lease/state management and direct Caddy Admin sync.
- `cmd/devwrap/daemon.go`: thin managed-wrapper process lifecycle (starts/stops embedded Caddy).
- `cmd/devwrap/proxy_caddy.go`: embedded Caddy startup/shutdown helpers.
- `cmd/devwrap/proxy_external.go`: Caddy Admin API inspection and route update logic.
- `cmd/devwrap/runtime.go`: runtime paths, health probes, daemon reachability helpers.
- `cmd/devwrap/admin_client.go`: centralized Caddy Admin HTTP access + readiness backoff.
- `install.sh`: release installer (downloads latest or selected GitHub release).
- `install-dev.sh`: local build + install script for development.

---

## Runtime Storage (XDG)

All runtime artifacts are stored under:

- `$XDG_STATE_HOME/devwrap` if set
- otherwise `~/.local/state/devwrap`

Files:

- `state.json`: tracked app leases and proxy metadata.
- `daemon.pid`: PID of the devwrap daemon (when daemon mode is used).
- `daemon.log`: daemon stdout/stderr log.

---

## Execution Modes

`devwrap` supports two operating modes.

### 1) Unmanaged Caddy Mode (preferred when available)

If Caddy Admin is reachable at `http://127.0.0.1:2019`, `devwrap`:

- Does **not** start a daemon.
- Uses `state.json` directly for lease tracking.
- Applies routes directly to Caddy via Admin API.
- Treats Caddy as `caddy_source=unmanaged`.

### 2) Managed Caddy Mode

If no Caddy Admin exists, `devwrap proxy start` (or an implicit start from run path):

- Spawns `devwrap proxy daemon` in background.
- Daemon starts embedded Caddy (in-process, via Caddy Go modules).
- Embedded Caddy exposes Admin on `127.0.0.1:2019`.
- Treats Caddy as `caddy_source=managed`.

---

## Command Surface

### Run App

```bash
devwrap --name <app> -- <cmd...>
devwrap --name=<app> -- <cmd...>
```

Flow:

1. Parse/validate app name (`[a-z0-9-]`, not leading/trailing `-`).
2. Resolve host (`--host` or default `<name>.localhost`) and validate hostname format.
3. Ensure Caddy Admin is available (unmanaged or managed).
4. Acquire lease from file state and sync routes directly to Caddy Admin.
5. Print HTTPS/HTTP URLs.
6. Warn if Caddy local CA is not trusted.
7. Run child command with:
   - `PORT=<assigned-port>` in env
   - `DEVWRAP_APP=<name>` in env
   - `@PORT` token replacement in argv
8. Forward signals to child; release lease on exit.

### Proxy Commands

- `devwrap proxy start`
- `devwrap proxy stop`
- `devwrap proxy status`
- `devwrap proxy trust`
- `devwrap proxy logs`

Behavior details:

- `start`
  - If daemon already up: no-op message.
  - If unmanaged Caddy admin found: no daemon needed; no-op with message.
  - Else spawn daemon and wait for readiness.
- `stop`
  - Stops only managed devwrap wrapper process (by PID signal).
  - Does not stop externally-managed Caddy.
- `status`
  - Reads file state and current Caddy Admin ports.
  - Marks source as `managed` if daemon PID is alive, otherwise `unmanaged`.
- `trust`
  - Uses local trust installation flow after ensuring Caddy is available.
- `logs`
  - Prints daemon log file contents.

### Route Registry Helpers

- `devwrap ls`: list tracked apps with URLs and app ports.
- `devwrap rm <name>`: remove route + tracked lease entry.

---

## Port Strategy

### App Ports (passthrough process)

- Range: `11000-19999`
- Selection rules:
  - skip ports already present in `state.Apps`
  - bind-probe `127.0.0.1:<port>` to ensure no external process is using it

### Proxy Listener Ports (only when spawning embedded Caddy)

- If root and free: `80/443`
- Else if free: `8080/8443`
- Else fallback: `9080/9443`
- If no valid pair is free: startup error

When using existing Caddy, listener ports are read from Admin config instead of assumed.

---

## Caddy Integration

### Admin Endpoint

- Base: `http://127.0.0.1:2019`

### Server Discovery

`proxy_external.go` reads `/config/apps/http/servers` and determines:

- HTTP server name and port
- HTTPS server name and port

Special handling:

- If `devwrap-http`/`devwrap-https` exist, those are preferred.
- Otherwise picks first discovered HTTP and TLS-capable servers.

### Route Model

For each app, route created with:

- `@id: devwrap-<app-name>`
- host match: app host from state (`--host` override or `<app>.localhost`)
- handler: reverse proxy to `127.0.0.1:<app-port>`

Route update behavior:

1. Merge existing routes while removing prior `devwrap-*` routes.
2. Attempt `PATCH` on `/routes`.
3. If patch fails, fallback to delete+put to recreate `/routes` payload.

This preserves non-devwrap routes while replacing devwrap-managed entries.

---

## TLS + Trust

Embedded Caddy is configured with internal issuer for all subjects, so custom hosts still use devwrap's local CA.

For managed mode, embedded Caddy is configured with explicit file-system storage root so CA material is reusable:

- `DEVWRAP_CADDY_DATA_DIR` (if set)
- else `CADDY_DATA_DIR` (if set)
- else Caddy app data dir for the invoking user
- if started via `sudo`, uses `SUDO_USER`'s Caddy app data dir when available

Trust checks/install follow Caddy's approach:

- Fetch root cert from Caddy Admin API (`/pki/ca/local`)
- Verify trust with `x509.Verify`
- Install trust via `github.com/smallstep/truststore`

If untrusted at run time, CLI prints:

- `devwrap proxy trust`
- `sudo devwrap proxy trust`

---

## Daemon Wrapper

When started, `devwrap proxy daemon` is a thin wrapper around embedded Caddy:

- picks listener ports
- starts Caddy with Admin on `127.0.0.1:2019`
- waits for process signals
- stops embedded Caddy on shutdown

All lease and route management is still performed by regular CLI invocations through file state + Caddy Admin API.

---

## Process Lifecycle and Signal Behavior

Child app process is started with inherited stdio.

- Signals (`INT`, `TERM`, `HUP`, `QUIT`) are forwarded to child.
- After child exit, lease is released.
- If child exits non-zero, devwrap exits with child exit status.

---

## Installation

`install.sh`:

- installs from GitHub release assets (latest by default)
- accepts an optional version argument (with or without `v` prefix)
- auto-detects repo from `git remote origin` or `REPO=owner/repo`
- verifies asset checksum using `checksums.txt`
- installs binary to `/usr/local/bin/devwrap` by default
- supports `PREFIX` / `BIN_DIR` overrides
- uses `sudo` automatically when destination is not writable

`install-dev.sh`:

- builds with `/usr/local/go/bin/go` if available, else `go` from `PATH`
- installs binary to `/usr/local/bin/devwrap` by default
- supports `PREFIX` / `BIN_DIR` overrides
- uses `sudo` automatically when destination is not writable

---

## Current Guarantees and Caveats

### Guarantees

- Works with either unmanaged or managed Caddy admin API.
- Route ownership is explicit through `@id=devwrap-*`.
- Stale process entries are evicted and synced.
- App ports avoid collisions with both tracked and externally bound sockets.
- State file updates are atomic per write and guarded by an inter-process lock (`state.lock`).

### Caveats

- Trust installation depends on OS trust store permissions/policies and may require elevated privileges.
- Port reservation has a small TOCTOU window between probe and child bind.
