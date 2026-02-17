# devwrap

Run local app commands behind Caddy with friendly local hostnames.

## Install

Install latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/iterate/devwrap/main/install.sh | bash
```

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/iterate/devwrap/main/install.sh | bash -s -- -v 0.0.2
```

## Quick Start

```bash
devwrap --name myapp -- pnpm dev
```

Use a custom host when needed:

```bash
devwrap --name web --host web.dev.test -- pnpm dev
```

Use `@PORT` when your app expects a CLI flag instead of env vars:

```bash
devwrap --name dev-server -- vite dev --port @PORT
```

By default hosts are `<name>.localhost`.

`devwrap` also sets `PORT=<allocated port>`, `DEVWRAP_APP=<name>`, and `DEVWRAP_HOST=<https url>` for the child process.

## Proxy Modes

- `unmanaged caddy`: Caddy is already running on admin API `127.0.0.1:2019`
- `managed caddy`: started by `devwrap proxy start`

Start managed Caddy:

```bash
devwrap proxy start
```

Privileged start (bind 80/443 if available):

```bash
devwrap proxy start -p
```

Shortcut: `devwrap -p` starts managed proxy when no `--name` + command are provided.

## Common Commands

```bash
devwrap proxy status
devwrap proxy trust
devwrap proxy stop
devwrap ls
devwrap rm <name>
devwrap doctor
```

All commands support `--json` for scriptable output.

Examples:

```bash
devwrap proxy status --json
devwrap ls --json
devwrap --json --name api -- uvicorn app:app --port @PORT
```

## Trust

`devwrap proxy trust` fetches the local CA root from Caddy admin API and installs trust using the same truststore approach used by Caddy.

## Runtime Files

State is stored in:

- `$XDG_STATE_HOME/devwrap`
- fallback: `~/.local/state/devwrap`

Files:

- `state.json`
- `state.lock`
- `daemon.pid`
- `daemon.log`

## Development

Build + install from local source (dev flow):

```bash
./install-dev.sh
```

Source code lives in `cmd/devwrap`.

Build locally with:

```bash
/usr/local/go/bin/go build ./cmd/devwrap
```
