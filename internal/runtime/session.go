package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	PhaseIdle       = "idle"
	PhaseInstalling = "installing"
	PhaseTunnel     = "tunnel"
	PhaseStarting   = "starting"
	PhaseReady      = "ready"
	PhaseFailed     = "failed"

	maxLogTailBytes = 8 << 10

	// keepFailedSessionDir keeps a failed attempt's generated session
	// directory on disk: its FrankenPHP logs are the only runtime diagnostics
	// available on readiness/startup failures, so deleting them would hide
	// the cause. Successful sessions delete their directory on Stop.
	keepFailedSessionDir = true
)

// sessionComponentOrder lists every component a cold start needs, in UI
// display order; all of them are downloaded concurrently by Manager.EnsureAll.
var sessionComponentOrder = []string{ComponentFrankenPHP, ComponentPHPMyAdmin, ComponentPMAThemeDarkwolf}

// readinessTimeout/readinessInterval are variables so tests can shorten the
// probe loop; production values keep a 30s readiness window.
var (
	readinessTimeout  = 30 * time.Second
	readinessInterval = 250 * time.Millisecond
)

// StatusSnapshot is the value returned to the frontend. Progress carries the
// real byte counts of every required component download so the UI can render
// a truthful aggregate instead of a fake timer animation.
type StatusSnapshot struct {
	Phase            string           `json:"phase"`
	Message          string           `json:"message"`
	URL              string           `json:"url,omitempty"`
	CurrentComponent string           `json:"currentComponent,omitempty"`
	Progress         ProgressSnapshot `json:"progress"`
}

// Session owns every runtime resource of a dedicated phpMyAdmin window:
// the SSH tunnel (optional), the FrankenPHP child process and the generated
// per-connection configuration. All of them share the Session context so
// closing the window tears everything down deterministically.
type Session struct {
	manager *Manager

	// lifecycleMu serializes Start/Stop so a retry can never interleave a
	// previous attempt's teardown with the new attempt's startup work.
	lifecycleMu sync.Mutex

	mu      sync.Mutex
	phase   string
	message string
	url     string

	tracker          *progressTracker
	compOrder        []string
	installComponent string

	tunnel  TunnelRunner
	cmd     *exec.Cmd
	ctx     context.Context
	cancel  context.CancelFunc
	started bool

	// sessionDir is the unique per-attempt filesystem root
	// (sessions/<serverID>/<token>) holding this attempt's cloned phpMyAdmin
	// tree, generated configs and logs. It is never shared with another
	// session or attempt, so concurrent windows of the same connection
	// cannot rebuild or delete each other's files.
	sessionDir string

	// sensitive values kept only for scrubbing diagnostics
	scrubValues []string

	// injectable seams for tests
	newTunnel   func() TunnelRunner
	spawn       func(cmd *exec.Cmd) error
	lookupCfg   func(serverID string) (*ServerConfig, error)
	sessionRoot string
	probeURL    func(port int) string
}

type TunnelRunner interface {
	Start(ctx context.Context) error
	Addr() string
	Close() error
}

type tunnelReconnecter interface {
	Reconnect(ctx context.Context) error
}

func NewSession(manager *Manager) *Session {
	return &Session{
		manager: manager,
		phase:   PhaseIdle,
		newTunnel: func() TunnelRunner {
			return NewSSHTunnel()
		},
		spawn: func(cmd *exec.Cmd) error { return cmd.Start() },
	}
}

// currentSessionDir returns the attempt's unique session directory, or
// empty before/after its ownership window. Tests use it to assert isolation
// and cleanup; it never appears in StatusSnapshot or error diagnostics.
func (s *Session) currentSessionDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionDir
}

// sessionToken returns a cryptographically random hex token (128 bits) that
// makes every session's filesystem root unique, so PID reuse or concurrent
// windows of the same connection can never collide.
func sessionToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Session) setPhase(phase, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phase = phase
	s.message = message
}

func (s *Session) setInstallComponent(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installComponent = name
}

// Snapshot returns the current session phase/message/URL plus the aggregate
// download progress for the frontend. The progress is computed from the
// shared tracker under its own lock, so polling never blocks a download.
func (s *Session) Snapshot() StatusSnapshot {
	s.mu.Lock()
	tracker := s.tracker
	order := s.compOrder
	phase := s.phase
	snap := StatusSnapshot{Phase: phase, Message: s.message, URL: s.url}
	s.mu.Unlock()

	if tracker == nil {
		snap.Progress = newRequiredTracker(nil).Snapshot(nil, "")
		return snap
	}
	snap.Progress = tracker.Snapshot(order, s.activeComponent(phase))
	return snap
}

// activeComponent names the component currently installing for the
// "installing" state; during downloads the per-component byte streams carry
// the state instead.
func (s *Session) activeComponent(phase string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if phase != PhaseInstalling {
		return ""
	}
	return s.installComponent
}

// SetConfigLoader wires the persisted connection catalogue into the session.
// The host process passes NewServerConfigLoader(configStore) here.
func (s *Session) SetConfigLoader(loader func(serverID string) (*ServerConfig, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupCfg = loader
}

// addScrubValues registers sensitive values that must never appear in errors
// or log tails returned to the UI.
func (s *Session) addScrubValues(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range values {
		if v != "" {
			s.scrubValues = append(s.scrubValues, v)
		}
	}
}

func (s *Session) scrub(text string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ScrubForDiagnostics(text, s.scrubValues...)
}

func (s *Session) fail(err error) error {
	clean := s.scrub(err.Error())
	s.setPhase(PhaseFailed, clean)
	return errors.New(clean)
}

// Start brings up the full Session and returns the loopback URL to navigate
// to. It is safe to call again after a failure; every previous attempt is
// fully torn down first so retries cannot orphan a tunnel or child process.
// Start and Stop are serialized by a lifecycle mutex, and cleanup never
// takes that mutex, so a concurrent Stop cannot interleave with startup.
func (s *Session) Start(ctx context.Context, serverID string) (string, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.cleanup()
	return s.startAttempt(serverID)
}

// startAttempt runs one startup sequence. On any failure it releases every
// resource acquired so far (tunnel and SSH client, FrankEnPHP child,
// session context) before returning, so partial starts cannot leak.
func (s *Session) startAttempt(serverID string) (string, error) {
	// The failure phase is recorded before cleanup so the teardown keeps the
	// attempt's session directory for its log-based diagnostics instead of
	// deleting the evidence of why startup failed.
	fail := func(err error) (string, error) {
		ferr := s.fail(err)
		s.cleanup()
		return "", ferr
	}

	if serverID == "" {
		return fail(errors.New("no connection selected for this window"))
	}
	s.mu.Lock()
	lookupCfg := s.lookupCfg
	s.mu.Unlock()
	if lookupCfg == nil {
		return fail(errors.New("Session configuration store unavailable"))
	}

	server, err := lookupCfg(serverID)
	if err != nil {
		return fail(fmt.Errorf("load connection: %w", err))
	}
	if server == nil {
		return fail(fmt.Errorf("connection %q no longer exists; close this window and reopen it from the launcher", serverID))
	}
	if !server.Tunnel.Enabled && server.Host == "" {
		return fail(errors.New("connection has no database host configured"))
	}
	s.addScrubValues(server.Password, server.Tunnel.Password, server.Tunnel.Passphrase, server.Tunnel.PrivateKey)

	sessionCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ctx = sessionCtx
	s.cancel = cancel
	s.mu.Unlock()

	// Fresh tracker per attempt: byte counts from a failed attempt must not
	// pollute a retry, and a cancelled download leaves no cleanup behind
	// because the tracker holds no resources.
	tracker := newRequiredTracker(sessionComponentOrder)
	s.mu.Lock()
	s.tracker = tracker
	s.compOrder = sessionComponentOrder
	s.mu.Unlock()

	s.setPhase(PhaseInstalling, "Installing the FrankenPHP runtime, phpMyAdmin and the Darkwolf theme (downloaded once per version)…")
	// Probe Content-Length values up-front (best-effort, unauthenticated
	// HEAD) so the aggregate bar has real denominators even before the first
	// bytes land; when a probe fails the component stays indeterminate.
	for _, component := range sessionComponentOrder {
		component := component
		go func() {
			info, err := s.manager.lookup(sessionCtx, component)
			if err == nil && info != nil {
				tracker.setVersion(component, info.Version)
				tracker.resolveExpectedTotal(sessionCtx, component, info.URL)
			}
		}()
	}
	dirs, markers, err := s.manager.EnsureAll(sessionCtx, sessionComponentOrder, tracker)
	if err != nil {
		return fail(err)
	}
	frankenDir := dirs[ComponentFrankenPHP]
	pmaInstallDir := dirs[ComponentPHPMyAdmin]
	themeComponentDir := dirs[ComponentPMAThemeDarkwolf]
	frankenMarker := markers[ComponentFrankenPHP]
	pmaMarker := markers[ComponentPHPMyAdmin]
	themeMarker := markers[ComponentPMAThemeDarkwolf]

	// Unique per-attempt filesystem root: server ID plus a cryptographically
	// random token (not a PID, which is reusable). Two windows opened on the
	// same connection therefore own disjoint trees and cannot rebuild or
	// delete each other's phpMyAdmin copy, generated config, Caddyfile,
	// php.ini or logs. The token appears only in local paths and never in
	// the status snapshot or failure messages.
	sessRoot := s.sessionRoot
	if sessRoot == "" {
		sessRoot = filepath.Join(s.manager.root, "sessions")
	}
	token, err := sessionToken()
	if err != nil {
		return fail(err)
	}
	sessDir := filepath.Join(sessRoot, SanitizePathSegment(serverID), token)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		return fail(fmt.Errorf("prepare Session directory: %w", err))
	}
	s.mu.Lock()
	s.sessionDir = sessDir
	s.mu.Unlock()

	s.setInstallComponent(ComponentPHPMyAdmin)
	pmaDir := filepath.Join(sessDir, "phpmyadmin")
	if err := cloneTree(pmaInstallDir, pmaDir); err != nil {
		return fail(fmt.Errorf("prepare Session phpMyAdmin copy: %w", err))
	}
	// The theme must be unpacked into this session's phpMyAdmin tree before
	// config.inc.php is written; if it fails we abort instead of writing a
	// ThemeDefault that would point at a missing theme.
	s.setInstallComponent(ComponentPMAThemeDarkwolf)
	if err := installDarkwolfTheme(themeComponentDir, pmaDir); err != nil {
		return fail(fmt.Errorf("install Darkwolf theme into the session tree: %w", err))
	}
	s.setInstallComponent("")

	var limitations []string
	if frankenMarker != nil && !frankenMarker.ChecksumVerified {
		limitations = append(limitations, "FrankenPHP archive could not be verified against an upstream checksum (none published for this asset)")
	}
	if pmaMarker != nil && !pmaMarker.ChecksumVerified {
		limitations = append(limitations, "phpMyAdmin archive has no official upstream checksum; recorded as unverified")
	}
	if themeMarker != nil && !themeMarker.ChecksumVerified {
		limitations = append(limitations, "Darkwolf theme tracks the unversioned phpMyAdmin/themes master branch; no official checksum exists, recorded as unverified")
	}

	dbHost := server.Host
	dbPort := server.Port
	if dbPort == 0 {
		dbPort = 3306
	}

	if server.Tunnel.Enabled {
		s.setPhase(PhaseTunnel, "Opening the SSH tunnel with strict host-key verification…")
		tunnel := s.newTunnel()
		if sshTun, ok := tunnel.(*SSHTunnel); ok {
			sshTun.Configure(TunnelParams{
				Host:       server.Tunnel.Host,
				Port:       server.Tunnel.Port,
				Username:   server.Tunnel.Username,
				Password:   server.Tunnel.Password,
				AuthMethod: server.Tunnel.AuthMethod,
				PrivateKey: server.Tunnel.PrivateKey,
				Passphrase: server.Tunnel.Passphrase,
				DBHost:     dbHost,
				DBPort:     dbPort,
			})
		}
		if err := tunnel.Start(sessionCtx); err != nil {
			return fail(fmt.Errorf("start SSH tunnel: %w", err))
		}
		s.mu.Lock()
		s.tunnel = tunnel
		s.mu.Unlock()
		localPort, err := splitPort(tunnel.Addr())
		if err != nil {
			return fail(fmt.Errorf("resolve tunnel endpoint: %w", err))
		}
		dbHost = "127.0.0.1"
		dbPort = localPort
	}

	s.setPhase(PhaseStarting, "Generating the phpMyAdmin Session configuration…")
	secret, err := BlowfishSecret()
	if err != nil {
		return fail(fmt.Errorf("generate Session secret: %w", err))
	}
	s.addScrubValues(secret)

	logsDir := filepath.Join(sessDir, "logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return fail(fmt.Errorf("prepare log directory: %w", err))
	}

	pmaConfig := ApplyServerToPMAConfig(BuildPMAConfig(secret, server.Username, server.Password), dbHost, dbPort)
	// ThemeDefault is only written after the Darkwolf theme was installed
	// into this session's tree above; a failed download/install aborted
	// startup earlier so this can never point at a missing theme.
	pmaConfig = ApplyThemeToPMAConfig(pmaConfig, ThemeDefaultName)
	if err := ContainsSSHSecret(pmaConfig, server); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(pmaDir, "config.inc.php"), []byte(pmaConfig), 0o600); err != nil {
		return fail(fmt.Errorf("write phpMyAdmin configuration: %w", err))
	}

	phpIniPath := filepath.Join(sessDir, "php.ini")
	ini, err := s.manager.BuildPHPIni(frankenDir)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(phpIniPath, []byte(ini), 0o600); err != nil {
		return fail(fmt.Errorf("write php.ini: %w", err))
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(fmt.Errorf("reserve local Session port: %w", err))
	}
	httpPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	caddyPath := filepath.Join(sessDir, "Caddyfile")
	caddy := BuildCaddyfile("127.0.0.1", httpPort, filepath.ToSlash(pmaDir))
	if err := os.WriteFile(caddyPath, []byte(caddy), 0o600); err != nil {
		return fail(fmt.Errorf("write Caddyfile: %w", err))
	}

	s.setPhase(PhaseStarting, "Starting the FrankenPHP runtime…")
	cmd := buildFrankenPHPCommand(frankenDir, caddyPath, phpIniPath, logsDir)
	if err := s.spawn(cmd); err != nil {
		closeCommandLogs(cmd)
		return fail(fmt.Errorf("start FrankenPHP: %w", err))
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	url := fmt.Sprintf("http://127.0.0.1:%d/", httpPort)
	probe := url + "index.php"
	if s.probeURL != nil {
		probe = s.probeURL(httpPort)
	}
	s.setPhase(PhaseStarting, "Waiting for phpMyAdmin to answer…")
	if err := WaitForReady(sessionCtx, probe, readinessTimeout); err != nil {
		tail := logTail(filepath.Join(logsDir, "frankenphp.stderr.log"), maxLogTailBytes)
		if tail != "" {
			return fail(fmt.Errorf("phpMyAdmin did not become ready: %w\nLast runtime output:\n%s", err, tail))
		}
		return fail(fmt.Errorf("phpMyAdmin did not become ready: %w", err))
	}

	s.mu.Lock()
	s.url = url
	s.started = true
	s.mu.Unlock()
	message := "phpMyAdmin is being served locally."
	if len(limitations) > 0 {
		message += "\n" + strings.Join(limitations, "\n")
	}
	s.setPhase(PhaseReady, message)
	return url, nil
}

// ReconnectTunnel replaces the SSH client and loopback listener while retaining
// the exact local port already configured in phpMyAdmin. It is only valid for
// a ready SSH-backed session and does not restart FrankenPHP or regenerate its
// config. The lifecycle mutex prevents Stop or a retry from racing the swap.
func (s *Session) ReconnectTunnel(ctx context.Context) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	s.mu.Lock()
	phase := s.phase
	tunnel := s.tunnel
	sessionCtx := s.ctx
	s.mu.Unlock()
	if phase != PhaseReady || tunnel == nil {
		return errors.New("SSH tunnel is not active for this session")
	}
	reconnecter, ok := tunnel.(tunnelReconnecter)
	if !ok {
		return errors.New("SSH tunnel reconnect is unavailable")
	}
	if sessionCtx == nil {
		sessionCtx = ctx
	}

	s.setPhase(PhaseTunnel, "Reconnecting the SSH tunnel on its existing local port…")
	if err := reconnecter.Reconnect(sessionCtx); err != nil {
		// A reconnect failure does not invalidate the running FrankenPHP session:
		// retain its ready lifecycle so the UI can offer another reconnect attempt
		// instead of trapping the user in a terminal failed state.
		clean := s.scrub(fmt.Sprintf("SSH reconnect failed: %v", err))
		s.setPhase(PhaseReady, clean)
		return errors.New(clean)
	}
	s.setPhase(PhaseReady, "SSH tunnel reconnected. phpMyAdmin remains available locally.")
	return nil
}

// Stop releases every Session resource plus the attempt's generated session
// directory. It never fails the caller; partial teardown is always attempted
// in reverse startup order (child/tunnel first, filesystem removal last, so
// a still-running FrankenPHP can never watch its document root disappear).
// It is idempotent and serialized with Start via the lifecycle mutex.
func (s *Session) Stop() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.cleanup()
	s.mu.Lock()
	s.started = false
	if s.phase != PhaseFailed {
		s.phase = PhaseIdle
		s.message = ""
	}
	s.mu.Unlock()
}

// cleanup tears down the session context, FrankenPHP child and SSH tunnel
// exactly once, whichever of Start/Stop triggered it, then removes the
// attempt's generated session directory — but only when this attempt owns it
// (s.mu tracks ownership) and the attempt is not in the failed phase. Failed
// attempts retain the directory by policy: the bounded FrankenPHP logs and
// the partially generated tree are the only startup diagnostics available
// (they contain no credentials; the per-session blowfish secret is scrubbed
// from any surfaced content). The tracker is not removed because it holds no
// resources.
func (s *Session) cleanup() {
	s.mu.Lock()
	cmd := s.cmd
	tunnel := s.tunnel
	cancel := s.cancel
	dir := s.sessionDir
	keep := s.phase == PhaseFailed && keepFailedSessionDir
	s.cmd = nil
	s.tunnel = nil
	s.ctx = nil
	s.cancel = nil
	s.sessionDir = ""
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if cmd != nil {
		closeCommandLogs(cmd)
	}
	if tunnel != nil {
		_ = tunnel.Close()
	}
	if dir != "" && !keep {
		_ = os.RemoveAll(dir)
	}
}

// buildFrankenPHPCommand constructs the classic-mode child process. PHP reads
// the per-session php.ini through PHPRC; ini_file_path is not a supported
// php_server subdirective in current FrankenPHP releases. Output is redirected
// to bounded session logs instead of a console, which Windows GUI-subsystem
// applications do not have. The files stay owned by the command and are
// explicitly closed after Wait/failed Start so Windows can remove the finished
// session directory.
func buildFrankenPHPCommand(frankenDir, caddyPath, phpIniPath, logsDir string) *exec.Cmd {
	exe := filepath.Join(frankenDir, frankenphpExe)
	cmd := exec.Command(exe, "run", "--config", caddyPath, "--adapter", "caddyfile")
	cmd.Dir = frankenDir
	cmd.Env = append(os.Environ(), "PHPRC="+filepath.Dir(phpIniPath))
	hideProcessWindow(cmd)
	cmd.Stdout = openLogTail(filepath.Join(logsDir, "frankenphp.stdout.log"))
	cmd.Stderr = openLogTail(filepath.Join(logsDir, "frankenphp.stderr.log"))
	return cmd
}

// closeCommandLogs releases the per-process log handles. exec.Cmd does not
// close caller-owned io.Writer values after Wait, and Windows disallows
// removing a directory while its log files are open.
func closeCommandLogs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	closed := make(map[io.Closer]struct{}, 2)
	for _, writer := range []io.Writer{cmd.Stdout, cmd.Stderr} {
		closer, ok := writer.(io.Closer)
		if !ok || closer == nil {
			continue
		}
		if _, seen := closed[closer]; seen {
			continue
		}
		_ = closer.Close()
		closed[closer] = struct{}{}
	}
}

// openLogTail truncates and opens a Session log file. Errors fall back to
// discarding output; readiness and stderr tails are only best-effort
// diagnostics.
func openLogTail(path string) io.Writer {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return io.Discard
	}
	return f
}

// logTail returns the last maxBytes of a Session log file, without loading
// unbounded amounts of runtime output into memory.
func logTail(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	offset := int64(0)
	if info.Size() > maxBytes {
		offset = info.Size() - maxBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WaitForReady polls the Session URL until phpMyAdmin answers with a real
// response. Only statuses below 400 count as ready: 2xx (a rendered page)
// and 3xx (phpMyAdmin's own redirect to its login page, followed by the
// client) are genuine application responses, while a 404/5xx comes from a
// server that is still booting or misconfigured and must not be accepted.
func WaitForReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no response from the local phpMyAdmin endpoint within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readinessInterval):
		}
	}
}

// ContainsSSHSecret asserts a generated artifact does not embed SSH credential
// material from the connection definition. Database credentials are intentionally
// written only to the private per-session phpMyAdmin config so config auth can
// sign in automatically.
func ContainsSSHSecret(artifact string, server *ServerConfig) error {
	for _, secret := range []string{
		server.Tunnel.Password, server.Tunnel.Passphrase, server.Tunnel.PrivateKey,
	} {
		if secret != "" && strings.Contains(artifact, secret) {
			return errors.New("internal error: generated configuration would embed an SSH credential; refusing to write it")
		}
	}
	return nil
}

func SanitizePathSegment(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func splitPort(addr string) (int, error) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(portStr)
}

const _ = runtime.GOOS // platform guard lives in defaultComponentLookup

// installDarkwolfTheme copies the installed darkwolf theme tree
// (themes-master/darkwolf from the official archive) into a session's
// phpMyAdmin themes/ directory. It mirrors cloneTree's traversal checks:
// refusing absolute/..-escaping paths, and skipping symlinks and other
// non-regular entries instead of materializing them.
func installDarkwolfTheme(themeComponentDir, sessionPMADir string) error {
	src := themeDir(themeComponentDir)
	if _, err := os.Stat(filepath.Join(src, "theme.json")); err != nil {
		return fmt.Errorf("installed theme is missing darkwolf/theme.json: %w", err)
	}
	dst := filepath.Join(sessionPMADir, "themes", ThemeDefaultName)
	if err := copyThemeTree(src, dst); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dst, "theme.json")); err != nil {
		return fmt.Errorf("theme.json missing after session install: %w", err)
	}
	return nil
}

// copyThemeTree copies regular files like cloneTree but additionally
// verifies every resolved target stays inside the destination, so a
// corrupted cache tree cannot write outside the session's themes directory.
func copyThemeTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	dstRoot := filepath.Clean(dst) + string(os.PathSeparator)
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		cleaned := filepath.Clean(target)
		if cleaned != filepath.Clean(dst) && !strings.HasPrefix(cleaned, dstRoot) {
			return fmt.Errorf("theme path %q escapes the themes directory", rel)
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case !info.Mode().IsRegular():
			// Skip symlinks/devices: the official theme only ships
			// regular files and accepting them would reopen traversal.
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	return nil
}

// cloneTree copies a directory tree; regular files only, symlinks skipped.
func cloneTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
