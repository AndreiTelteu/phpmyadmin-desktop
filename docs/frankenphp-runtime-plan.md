# FrankenPHP + phpMyAdmin runtime implementation plan

This document describes the architecture implemented on the
`feature/frankenphp-phpmyadmin-runtime` branch. It turns the connection
**Open** button into a real, dedicated phpMyAdmin session served by the
official FrankenPHP Windows runtime in classic mode (`php_server` only).

## Goals and non-goals

- Windows x86_64 is the primary runtime target. The launcher still compiles on
  other platforms, but the managed FrankenPHP runtime fails fast with a clear
  error when the platform has no official archive.
- Classic Caddy mode only: a generated Caddyfile with `php_server`. No
  FrankenPHP worker mode, no RoadRunner.
- No authentication against GitHub: all downloads use the public release
  endpoints already used by `CheckLatestVersion.go`.
- No new browser: phpMyAdmin renders inside the Wails WebView2 window by
  navigating that window to the loopback session URL.
- No telemetry, cloud sync, or remote credential APIs.

## Process model and ownership

One app process owns one session, unambiguously:

```text
launcher process (compact window, 540x600)
   └─ Open(serverId) → spawns second executable with -serverId <id>

session process (full window, 1024x768, title "PMA <connection name>")
   ├─ runtime.Manager   : downloads/verifies/installs FrankenPHP + phpMyAdmin
   │                      + the Darkwolf theme concurrently under the app-data
   │                      cache (shared, keyed by version)
   ├─ SSHTunnel         : optional loopback forward to the database via the
   │                      configured bastion (owned by this process)
   ├─ Caddy/FrankenPHP  : child `frankenphp.exe run` process (owned by this
   │                      process, killed on exit)
   └─ WebviewWindow     : navigated to http://127.0.0.1:<port>/ once ready
```

Teardown order is the reverse of startup: on window close or app shutdown the
session kills the FrankenPHP child process (process kill + `Wait` to reap)
and closes the SSH tunnel listener; there is no detached `frankenphp stop`
invocation anywhere in the lifecycle because the child was spawned classic
via `run` and is owned directly. The shared version-keyed runtime cache is
*not* deleted; per-attempt generated state (the cloned phpMyAdmin tree with
`config.inc.php`, the Darkwolf theme files inside it, `Caddyfile`, `php.ini`,
logs) lives under `sessions/<serverID>/<token>/`, where `<token>` is a fresh
128-bit hex value generated per attempt from `crypto/rand` — never a PID, so
two windows of the same connection and PID reuse cannot collide. A
successful session removes its unique directory on `Stop`; a failed attempt
retains it deliberately because its bounded FrankenPHP logs are the only
startup diagnostics, and no credentials are ever stored there (the per-
session blowfish secret is inert config material and is scrubbed from any
surfaced diagnostic).

## File layout (app data)

The runtime root is `<xdg.DataHome>/phpMyAdmin Desktop/runtime/`
(`%LOCALAPPDATA%\phpMyAdmin Desktop\runtime` on Windows):

```text
runtime/
  frankenphp/<version>/frankenphp.exe, php.exe, ext/, ...   (installed archive)
  phpmyadmin/<version>/                                     (installed archive; never sessionized,
                                                            never carries config.inc.php)
  pma-theme-darkwolf/master-snapshot/
    themes-master/darkwolf/theme.json, css/, ...            (installed theme archive)
  tmp/                                                      (staging, same volume)
  downloads/                                                (downloaded archives)
  sessions/<serverID>/<128-bit-token>/      (unique per attempt; removes guessable PID reuse)
    phpmyadmin/          (cloned tree with generated config.inc.php + themes/darkwolf)
    Caddyfile            (generated, no secrets)
    php.ini              (generated, no secrets)
    logs/frankenphp.stdout.log / frankenphp.stderr.log (bounded tails, scrubbed)
```

The `sessions/<serverID>/<token>/` root is unique per attempt: the token is
generated from `crypto/rand` at startup so concurrent same-server windows
always build disjoint trees. Cleanup removes the unique directory only after
the FrankenPHP child and tunnel are stopped; a failed attempt keeps
its directory for diagnostics instead of destroying the log tails.

Installs are serialized per component+version across processes: an atomic
lock directory under `locks/<component>-<version>.lock` guards the whole
download/extract/publish sequence (`lock.go`), waiters re-check the install
marker after acquiring the lock (so the artifact is downloaded exactly once),
and locks left behind by a crashed installer (older than 60 minutes) are
reclaimed as stale. Within the lock the archive is streamed to `downloads/`,
extracted into `tmp/<component>.staging-<pid>-<n>`, the successor tree is
prepared next to the final path, and a final directory rename publishes it. A
`nilfs` seam (`runtimeFS` interface) wraps the filesystem calls that need
injection in unit tests on Linux.

The phpMyAdmin and FrankenPHP archives are flattened to their single
top-level directory; the Darkwolf theme archive keeps its `themes-master/`
wrapper intact because the session copies `themes-master/darkwolf` verbatim
into phpMyAdmin's `themes/darkwolf` (`session.go` `installDarkwolfTheme`,
which applies the same traversal/symlink checks as the archive extractors).

## Download integrity

- `releaseInfo` (extended from `CheckLatestVersion.go`) resolves the latest
  FrankenPHP release asset `frankenphp-windows-x86_64.zip` *and* the upstream
  `hashes.json` asset published by php/frankenphp releases.
- Downloads are bounded (response body is limited to 512 MiB) and streamed to
  disk with a 15-minute client timeout.
- When the upstream `hashes.json` contains a SHA-256 for the selected asset,
  the archive is verified before extraction and the checked hash is stored in
  the install marker.
- When no official checksum is published for an asset (this is the case for
  the phpMyAdmin official all-languages distribution ZIP and for the
  phpMyAdmin/themes `master` branch archive the Darkwolf theme comes from;
  branch archives move and upstream publishes no checksum), the installer records
  `checksumVerified: false` in the `install.json` marker and surfaces that
  limitation in session status diagnostics instead of inventing a hash.
- Extraction is traversal-guarded for both ZIP and `.tar.gz`: absolute paths,
  drive-letter/prefixed paths, and any entry that would escape the destination
  via `..` are rejected; symlinks and other non-regular entries are skipped;
  individual members are capped at 512 MiB and the whole archive at 4 GiB
  decompressed.

## Generated configuration (session-scoped database credentials)

- `php.ini`: production php.ini from the archive plus
  `extension_dir = "<runtime>/ext"` and the extensions phpMyAdmin needs
  (`curl`, `mbstring`, `mysqli`, `openssl`, `pdo_mysql`, `zip`,
  `sessions`-friendly defaults).
- `Caddyfile`: single HTTP site bound to `127.0.0.1:<freePort>`, document root
  pointing at the installed phpMyAdmin directory, `php_server`, `admin off`
  and FrankePHP ini override pointing at the generated `php.ini`. No logs to
  the terminal: Caddy writes to files; process stdout/stderr are redirected to
  bounded log tails.
- `config.inc.php` (per session phpMyAdmin tree, regenerated per connection):
  `blowfish_secret` is generated with `crypto/rand` (phpMyAdmin requires 32
  chars), `auth_type = 'config'`, and a single server entry whose **host and
  port are always written explicitly** — including port `3306`, so the served
  config documents its real endpoint instead of relying on phpMyAdmin's
  implicit default. The host is either the configured direct host or
  `127.0.0.1` with the tunnel's ephemeral port when the tunnel is enabled.
  **The database username/password from `servers.json` are written only to this
  private per-session config** so phpMyAdmin opens already authenticated. SSH
  credentials are never injected into `config.inc.php`; generated session
  directories are removed when a successful session closes. Guards reject SSH
  secret values in generated artifacts, error
  message, or diagnostic status, and unit tests scan generated output and the
  session status snapshot for known sentinel secrets.
- `ThemeDefault = 'darkwolf'` is written only after the Darkwolf theme was
  actually installed into the session tree; if the theme download or install
  fails, session startup fails explicitly instead of emitting a broken
  directive.

## SSH tunnel (`tunnel.go`, `tunnel_dial.go`)

- Implementation: `golang.org/x/crypto/ssh` plus
  `github.com/skeema/knownhosts` for strict `known_hosts` verification.
- Host-key policy is *strict*: the callback is built from the user's
  `~/.ssh/known_hosts` (and `known_hosts2`). Unknown or changed host keys are
  rejected; there is no auto-accept and no `InsecureIgnoreHostKey` path. If no
  known-hosts file exists the tunnel refuses to start with an actionable
  error pointing at `ssh-keyscan`/manual provisioning.
- Auth: `publicKey` (private key file + optional passphrase) or `password`.
  Key content/paths are never logged.
- The listener binds `127.0.0.1:0` (OS-assigned port). Accept loop dials the
  requested `dbHost:dbPort` through the SSH client per connection and proxies
  both directions; context cancellation closes the listener and the SSH
  client. Startup failures (auth, unreachable bastion, unknown host key)
  propagate synchronously from `Start` so the UI can show them.

## Session lifecycle (`session.go`, `session_state.go`)

`SessionStart` (bound method, called by the PMA frontend) performs:

1. Load `servers.json` and resolve the selected server by ID. Unknown IDs are
   an immediate error surfaced to the UI.
2. Ensure the runtime: resolve the latest FrankenPHP + phpMyAdmin releases
   and the Darkwolf theme snapshot, then download/verify/install the missing
   version directories **concurrently** (`Manager.EnsureAll`), reporting real
   byte progress (Content-Length when the server sends one, indeterminate per
   component otherwise, plus a weighted aggregate across all three — cached
   components count as completed). Concurrent session processes still
   serialize on the per-component install lock.
3. Generate/repair the session config files for this server ID.
4. If `tunnel.enabled`, start the SSH tunnel first and point phpMyAdmin at
   the loopback endpoint; regenerate `config.inc.php` accordingly.
5. Allocate a free loopback port, write the final Caddyfile with it, spawn
   `frankenphp.exe run --config <Caddyfile> --adapter caddyfile` with
   stdout/stderr captured to bounded log files.
6. Readiness probe: GET `http://127.0.0.1:<port>/index.php` every 250 ms up
   to 30 s; only statuses below 400 count as ready, so a live `phpMyAdmin`
   page (200) or a genuine phpMyAdmin-driven redirect (3xx) passes, while a
   server that is still booting, misconfigured, or returning 404/5xx does
   not. On timeout the bounded stderr tail is attached to the error, already
   scrubbed of any secret values.
7. Return the session URL to the frontend, which navigates the window
   (`window.location.assign(url)`), rendering phpMyAdmin inside the Wails
   WebView2 — the approach supported by Wails v3 for arbitrary localhost
   URLs; no fake browser UI is involved.

`SessionStop`/`shutdown()` release the session resources exactly once, in
reverse startup order: cancel the session context, `Process.Kill` + reap the
FrankenPHP child, then close the tunnel (which closes the local listener and
the SSH client). The same cleanup runs on any mid-start failure and at the
entry of a retried `Start`, so a partial or failed start cannot leak a tunnel
or child process. Window close quits the process, which triggers the same
shutdown handler, so orphan children cannot survive the session window. A
lifecycle mutex serializes `Start`/`Stop` so concurrent calls can never
interleave destructive work; cleanup itself never takes that mutex, so a
Stop racing a Start cannot deadlock. The unique `sessions/<serverID>/<token>/`
directory is deleted only after the child and tunnel are stopped: a
successful session removes it on `Stop`, while a failed attempt retains it
deliberately because its bounded FrankenPHP stdout/stderr logs are the only
startup diagnostics available.

`SessionStatus` returns the current phase (`idle`, `installing`, `starting`,
`tunnel`, `ready`, `failed`), a scrubbed human-readable message, and a
structured `progress` snapshot: per-component name/state/bytes/total
(`total < 0` = server gave no Content-Length, rendered indeterminate) and a
weighted aggregate (`aggregateBytes`, `aggregateTotal`, `aggregateKnown`,
`percent`) computed from real transfers plus HEAD-probed Content-Length
denominators. The aggregate never claims 100% before a component is fully
published, and never fabricates a percentage where no size signal exists.
The frontend polls this every 500 ms; status updates are therefore bounded
by the existing poll cadence and do not flood the bindings.

## Frontend (`frontend/src/PMA.tsx`)

- On mount: `SessionStart()`. Phases render a progress panel; while the
  runtime downloads, an aggregate progress bar plus per-component rows show
  the real transferred bytes/percent (or an indeterminate/"N received" state
  when upstream sends no Content-Length and the aggregate is marked as an
  estimate — never a fake timer). `failed` shows the scrubbed error plus a
  **Retry** button that calls `SessionStart()` again and a note when the
  tunnel requires a known-hosts entry.
- On success the window navigates to the loopback session URL.
- The window is created by Go with the final title/size at startup; the
  frontend additionally sets `document.title` as a fallback. The old
  "planned, not implemented" placeholder copy is removed.

## Window integration (`main.go`, `app.go`)

- `-serverId ""` → compact launcher window (unchanged).
- `-serverId <id>` → window title `PMA <connection name>` (name resolved in
  Go before window creation, falling back to a generic title), 1024x768,
  `BackdropType: application.None` (no Mica), URL `/` — the frontend router
  still picks `PMA` from `GetServerID()`.

## Tests

Pure/infrastructure logic, runnable on Linux without a Windows runtime:

- `CheckLatestVersion_test.go`: asset selection, missing-asset errors,
  checksum selection from `hashes.json`, phpMyAdmin tag parsing.
- `runtime_test.go`: ZIP/tar.gz traversal rejection (`../`, absolute paths,
  prefixed paths), symlink skipping, atomic publish layout, `php.ini` and
  Caddyfile generation through the `nilfs` seam, install marker records
  unverified checksums explicitly, and install-lock behaviour (concurrent
  Ensures of the same version serialize and download exactly once; stale
  locks are reclaimed; released locks are removed).
- `theme_test.go`: Darkwolf archive extraction with the official
  `themes-master/darkwolf` target layout preserved, traversal rejection in
  the theme archive, checksum-unverified install marker, session-tree
  placement under `themes/darkwolf` with symlink skipping, failure of session
  startup when the theme tree is missing, and the generated
  `ThemeDefault = 'darkwolf'` directive.
- `progress_test.go`: byte-level progress against slow/chunked
  `httptest` servers (with and without Content-Length), indeterminate
  rendering data, the 99% cap until a download is confirmed published,
  concurrent `EnsureAll` of all three required components with the weighted
  aggregate, cache-hit completion counting, tracker thread safety, and the
  status snapshot carrying progress without leaking sentinel credentials.
- `session_test.go`: `config.inc.php` generation (no sentinel secrets,
  blowfish length, direct vs tunnel host), command construction for the
  FrankenPHP child, port allocation returns loopback listeners, readiness
  probe against a disposable `httptest.Server`, full `SessionStop` cleanup
  using a stubbed process, lifecycle cleanup invariants (a tunnel that
  started is closed exactly once when spawn or readiness fails afterwards,
  retry after a failed start leaves no orphan tunnel/process, `Stop` is
  idempotent and concurrent-safe).
- `tunnel_test.go`: known-hosts verification against an in-process SSH server
  (accepted key succeeds, unknown key fails closed), listener binds loopback.

## Windows CI

The existing `frankenphp-classic-smoke` GitHub Actions job verifies the public official Windows archive, the required PHP extensions, and a classic-mode PHP request. The `build` job builds the Windows desktop executable and runs the Go suite. The repository runtime's installer/session flow still needs a live Windows acceptance run against the resulting desktop application.

 ## Known limitations (documented, intentional)

- phpMyAdmin comes from the official all-languages distribution ZIP, which
  includes Composer runtime dependencies such as `vendor/autoload.php` and
  therefore needs no local Composer install. No official release-bound checksum
  is published for this download URL, so installs record
  `checksumVerified: false`; FrankenPHP ZIPs are verified against upstream
  `hashes.json` when present.
- The Darkwolf theme tracks the unversioned `master` branch of
  `phpmyadmin/themes` (`master-snapshot` cache key): branch archives move
  and no official checksum exists, so it is recorded
  `checksumVerified: false` like the phpMyAdmin tarball, and a cached theme
  may lag upstream until the cache is refreshed.
- Cookie auth requires the user to log in with the saved DB credentials in
  phpMyAdmin's form; credentials still live in `servers.json` locally
  (documented in README) and are cleared from scope by not embedding them in
  the served config.
- FrankenPHP's Caddy admin API is disabled (`admin off`); privilege-dropped
  operation is default behaviour on Windows.
- Linux/macOS can compile and unit-test the runtime logic but the managed
  FrankenPHP download is Windows-only by upstream artifact availability.
- The session process resolves the server config at window start; edits in the
  launcher while a session is open do not retro-apply.
