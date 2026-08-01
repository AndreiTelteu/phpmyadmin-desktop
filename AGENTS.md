# AGENTS.md

## Product intent

`phpmyadmin-desktop` is intended to be a local desktop control plane for phpMyAdmin access—not a new database client and not a hosted service. The user saves a catalogue of database connections, optionally describes an SSH bastion for each one, and launches a dedicated phpMyAdmin session for the selected environment.

The important security/product boundary is: database access stays local to the machine running the app. A remote MySQL/MariaDB endpoint should normally be reached through an SSH tunnel instead of being exposed publicly. The app holds connection metadata locally; it must never add telemetry, cloud sync, a remote credential API, or a public database proxy unless the product direction explicitly changes.

This is a **pre-release** application. The connection catalogue, local persistence model, SSH-key picker, Wails shell, public component-version checks, FrankenPHP/phpMyAdmin runtime, and strict SSH-tunnel lifecycle are implemented. Secure credential storage and Windows end-to-end acceptance validation remain incomplete; do not describe the runtime as production-ready.

## Stack and layout

- **Go** backend, module: `github.com/andreitelteu/phpmyadmin-desktop`.
- **Wails v3 alpha** application shell, pinned in `go.mod`.
- **SolidJS + TypeScript + Vite** frontend in `frontend/`.
- **Kobalte** and `solid-styled-components` for the current UI primitives.
- Saved connection definitions are persisted as `servers.json` in the local XDG config directory by `github.com/AndreiTelteu/wails-configstore`.
- **PHP/phpMyAdmin runtime:** use the official FrankenPHP Windows archive in **classic mode** (`php_server` only; do not enable FrankenPHP worker mode). The native app owns the downloaded runtime, generated `php.ini`/Caddyfile, process, readiness probe, and teardown. The implementation is in `internal/runtime/`; it uses per-component/version install locks, archive traversal guards, loopback-only listeners, strict SSH `known_hosts` verification, and per-session cleanup. Do not compile FrankenPHP during an end-user install. RoadRunner is not the chosen runtime: it requires a PHP worker/protocol lifecycle while adding no compatibility advantage for phpMyAdmin. The session theme is Darkwolf, installed from the checksum-unverified `phpmyadmin/themes` master snapshot; generated per-session configs use config auth with the saved database credentials (for automatic login), explicit host/port including 3306, and `ThemeDefault = 'darkwolf'` only after theme installation. SSH credentials are never embedded in generated configs.

| Path | Responsibility |
| --- | --- |
| `main.go` | Wails application construction, bound services, embedded frontend, main window. |
| `app.go` | Bound host APIs: configuration access, private-key picker, and dedicated-process launch. |
| `internal/runtime/` | Windows FrankenPHP/phpMyAdmin/Darkwolf-theme installer/cache with concurrent byte-level download progress, generated per-session config (`config` auth using saved DB credentials for automatic login; explicit host/port incl. 3306; `ThemeDefault = 'darkwolf'` only after theme install), strict SSH forwarding, session lifecycle, and runtime tests. |
| `CheckLatestVersion.go` | Public GitHub release/tag discovery used by the component UI. |
| `frontend/src/` | Solid UI and local store. |
| `frontend/bindings/` | Generated Wails v3 TypeScript bindings; do not edit manually. |
| `frontend/dist/` | Generated production frontend embedded into the Go binary. |
| `build/` | App icons and platform package metadata. |

## Build and verification

The `Taskfile.yml` encodes the Wails v3 build order. Run:

```bash
wails3 task test
# or
wails3 task build
```

For direct commands, the equivalent validation path is:

```bash
cd frontend && npm run build
cd .. && wails3 generate bindings -clean -b -names -ts -d frontend/bindings
gofmt -w *.go
go test ./...
```

## Change rules

1. **Do not hand-edit generated output.** Regenerate `frontend/bindings/` after changing any exported `App` method. Rebuild `frontend/dist/` after frontend changes.
2. **Treat connection fields as sensitive.** Never log, print, commit, or expose database passwords, SSH passwords, private-key paths, passphrases, or the saved `servers.json` file.
3. **Keep SSH tunnels loopback-only.** Pick a free local port, bind it to `127.0.0.1`, propagate startup failures to the UI, and stop the tunnel when the corresponding phpMyAdmin process/window exits. Do not disable host-key verification in production-quality tunnel code.
4. **Keep runtime and UI lifecycle explicit.** A connection launcher needs ownership of phpMyAdmin/PHP processes, tunnel cancellation, readiness checks, port allocation, and cleanup. Avoid detached orphan processes.
5. **Preserve platform behavior.** The root window is a compact connection manager; an opened connection is expected to be a full-size separate session. Test targeted changes on the affected OS rather than assuming Linux behavior matches Windows/macOS.
6. **Do not overclaim prototype features.** If a runtime installer, phpMyAdmin HTTP server, browser view, or tunnel management is not implemented and verified, label it planned or incomplete in documentation and UI.

## Verification checklist

For code changes, run the task (or its direct equivalent):

```bash
wails3 task test
```

If invoking direct commands, regenerate bindings after changes to exported `App` methods, then run `gofmt`, `go test ./...`, `git diff --check`, and `git status --short`.

## Known technical debt

- The historical `wails.json`, old `frontend/wailsjs/` bindings, and shell scripts were Wails v2 artifacts. Do not revive them; use the Wails v3 application/bindings flow.
- The version discovery paths use unauthenticated public GitHub APIs. Public rate limits apply and phpMyAdmin's official all-languages distribution ZIP has no release-bound upstream checksum; the installer records that limitation. The distribution includes `vendor/autoload.php` and needs no Composer on the end-user machine. Secure credential storage and full Windows end-to-end runtime validation remain outstanding.
