# phpMyAdmin Desktop

A local desktop launcher for **phpMyAdmin** connections, with saved database profiles and optional **SSH tunnels** for remote MySQL/MariaDB hosts.

The intended workflow is deliberately simple:

1. Keep phpMyAdmin and its runtime on your workstation.
2. Save the database environments you work with.
3. For a private database, describe an SSH bastion rather than opening MySQL to the internet.
4. Launch a dedicated local phpMyAdmin session for that environment.

This is an early prototype. The connection catalogue, local persistence model, SSH-key picker, Wails shell, and component-version checks exist. Reliable PHP/phpMyAdmin installation, local HTTP serving, SSH-tunnel lifecycle management, secure credential storage, and opening a working phpMyAdmin session are still under construction.

## Why this exists

phpMyAdmin is useful, but its usual deployment model creates friction for people who manage several remote databases:

- each host may have a different database endpoint and credential set;
- the database should not need a public port;
- SSH port-forwarding is safer but repetitive to configure manually;
- a browser tab does not give a clean boundary between unrelated client/staging/production environments.

phpMyAdmin Desktop is meant to be the small local control plane around that workflow. It is **not** trying to replace phpMyAdmin, MySQL, or SSH. It should make the safe path—local phpMyAdmin plus a per-connection tunnel—the convenient path.

## Intended connection model

### Direct local connection

For local development, phpMyAdmin connects to MySQL/MariaDB directly:

```text
phpMyAdmin Desktop → local phpMyAdmin → 127.0.0.1:3306
```

### Remote connection through an SSH bastion

For a remote environment, the desktop app should create a local-only forward and point phpMyAdmin at it:

```text
phpMyAdmin Desktop → local phpMyAdmin → 127.0.0.1:<ephemeral-port>
                                              │
                                              └─ SSH bastion → database-host:3306
```

The tunnel must bind to loopback only, use a free local port, propagate errors clearly, and shut down with its phpMyAdmin session. The database server remains private; the workstation is the only machine that talks to the tunnel.

## Current state

| Area | Status |
| --- | --- |
| Solid/Wails desktop shell | Migrated to Wails v3 alpha2.119 |
| Saved connection catalogue | Implemented locally as `servers.json` via XDG config storage |
| SSH profile fields and private-key picker | Prototype implemented |
| Dedicated app process for a selected profile | Implemented, but not yet a completed phpMyAdmin session |
| Version lookup for app/PHP/phpMyAdmin | Prototype scraper; needs hardening |
| PHP/phpMyAdmin installation and lifecycle | Not implemented |
| Start/stop SSH tunnel with readiness and cleanup | Not implemented |
| Secure credential storage | Not implemented—do not use this prototype with production secrets |
| Browser/webview phpMyAdmin session | Not implemented |

## Technology

- Go 1.25+ and [Wails v3 alpha](https://v3.wails.io/)
- SolidJS, TypeScript, Vite
- Kobalte + `solid-styled-components`
- `github.com/rgzr/sshtun` for the planned SSH forwarding layer
- `github.com/AndreiTelteu/wails-configstore` for the current local configuration store

Wails v3 is still pre-release software. The project intentionally pins the tested alpha version in `go.mod`.

### Runtime for phpMyAdmin

The planned Windows runtime is the **official FrankenPHP Windows archive**, used in [classic mode](https://frankenphp.dev/docs/classic/) only. No `worker` directive is used: each phpMyAdmin request runs with the normal request lifecycle, which is the safe compatibility choice for an application that was designed around PHP's per-request state.

This means the runtime manager will download and checksum-pin a release archive, unpack it inside the app data directory, generate a minimal `php.ini` that enables phpMyAdmin's required extensions, and start `frankenphp.exe` with a generated Caddyfile containing `php_server`. It must retain and terminate that process as part of the selected connection session.

Do **not** compile FrankenPHP on an end-user machine. The upstream project publishes a Windows x86_64 archive containing `frankenphp.exe`, the compatible PHP runtime, and its required DLLs. This repository's Windows CI downloads the latest official archive and smoke-tests classic-mode PHP serving; the desktop app build is separately compiled on `windows-latest`.

RoadRunner has official Windows binaries, but it requires an application PHP worker process plus its worker protocol. That lifecycle is unnecessary for phpMyAdmin and does not eliminate bundling PHP/extensions. It is not the chosen runtime for this product.

## Development setup

### Requirements

- Go **1.25+**
- Node.js + npm
- Wails v3 CLI matching `go.mod`
- Native Wails build dependencies for the OS you are building on

Linux builds require `pkg-config`, GTK development headers, and WebKitGTK development headers. Install the packages appropriate to your distribution before running Go/Wails builds. Windows and macOS need their standard Wails platform prerequisites.

Install the pinned CLI locally:

```bash
CGO_ENABLED=0 GOBIN="$HOME/.local/bin" go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha2.119
export PATH="$HOME/.local/bin:$PATH"
```

### Build the frontend and generate bindings

```bash
git clone git@github.com:AndreiTelteu/phpmyadmin-desktop.git
cd phpmyadmin-desktop

cd frontend
npm install
npm run build
cd ..

wails3 generate bindings -clean -b -names -ts -d frontend/bindings
gofmt -w *.go
go test ./...
```

`frontend/dist` is embedded into the Go binary. The Wails v3 bindings in `frontend/bindings/` are generated; do not edit them by hand.

### Run/build

Once native prerequisites are installed, build with the included Wails v3 task:

```bash
wails3 task build
```

List the available task targets or inspect the installed CLI with:

```bash
wails3 task --list
wails3 --help
```

## Security notes

This repository is not ready for production database credentials. The current persistence implementation writes the connection catalogue—including fields for passwords and SSH passphrases—to a local configuration file. Until that is replaced with OS keychain/credential-store integration:

- use only disposable test credentials;
- never commit `servers.json` or screenshots/logs containing connection data;
- do not expose database ports publicly as a workaround;
- prefer SSH key authentication and strict host-key verification in the eventual tunnel implementation.

## Roadmap

The next meaningful implementation milestones are:

1. Build a validated connection editor with stable IDs and safe defaults.
2. Add a runtime manager for PHP and phpMyAdmin: download/verify, install, start, health-check, stop.
3. Implement a tunnel manager with loopback binding, a free-port allocator, SSH host-key verification, connection-state UI, retry policy, and deterministic cleanup.
4. Launch a phpMyAdmin session scoped to the selected connection—ideally with clear environment labeling so production mistakes are harder to make.
5. Move secrets out of `servers.json` into native secure storage.
6. Add integration tests using a disposable MySQL/MariaDB server and SSH endpoint.

## Contributing

Read [AGENTS.md](AGENTS.md) before changing code. It documents the intended product boundary, generated-file rules, build order, security expectations, and the known prototype gaps.
