package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	wailsconfigstore "github.com/AndreiTelteu/wails-configstore"
)

func testServerConfig() *ServerConfig {
	return &ServerConfig{
		ID:       "conn-1",
		Name:     "Production",
		Host:     "db.internal",
		Port:     3307,
		Username: "app",
		Password: "s3cret-sentinel",
		Tunnel: TunnelConfig{
			Enabled:    false,
			Host:       "",
			Port:       22,
			Username:   "ubuntu",
			Password:   "ssh-sentinel",
			AuthMethod: "password",
			PrivateKey: "C:\\keys\\id_rsa_sentinel",
			Passphrase: "pass-sentinel",
		},
	}
}

func TestPMAConfigDoesNotLeakSecrets(t *testing.T) {
	server := testServerConfig()
	cfg := ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), server.Host, server.Port)

	for _, sentinel := range []string{
		server.Password,
		server.Tunnel.Password,
		server.Tunnel.Passphrase,
		server.Tunnel.PrivateKey,
		"pass-sentinel", "s3cret-sentinel", "ssh-sentinel",
	} {
		if strings.Contains(cfg, sentinel) {
			t.Fatalf("generated config.inc.php leaks secret %q", sentinel)
		}
	}
	if !strings.Contains(cfg, "'auth_type'] = 'cookie'") {
		t.Fatal("config must use cookie auth so credentials stay out of the static file")
	}
	if !strings.Contains(cfg, "'host'] = 'db.internal'") {
		t.Fatal("config must point at the direct host")
	}
	if !strings.Contains(cfg, "'port'] = '3307'") {
		t.Fatal("non-default port must be written")
	}
	// Username/password have no place in the served config: cookie auth
	// accepts them through phpMyAdmin's login form at runtime.
	if strings.Contains(cfg, server.Username) || strings.Contains(cfg, "'user'") || strings.Contains(cfg, "'password'") {
		t.Fatal("config must not embed database username/password directives")
	}
}

func TestPMAConfigExplicitPort3306(t *testing.T) {
	cfg := ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), "db.internal", 3306)
	if !strings.Contains(cfg, "$cfg['Servers'][$i]['host'] = 'db.internal';") {
		t.Fatal("direct host must be injected")
	}
	if !strings.Contains(cfg, "$cfg['Servers'][$i]['port'] = '3306';") {
		t.Fatalf("port 3306 must be written explicitly, got:\n%s", cfg)
	}
	// Zero/absent port normalizes to 3306, never to an empty directive.
	cfg = ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), "db.internal", 0)
	if !strings.Contains(cfg, "$cfg['Servers'][$i]['port'] = '3306';") {
		t.Fatal("zero port must normalize to explicit 3306")
	}
}

func TestPMAConfigAppliesExactlyOnce(t *testing.T) {
	cfg := ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), "127.0.0.1", 3306)
	if strings.Count(cfg, "'port']") != 1 {
		t.Fatalf("port directive must appear exactly once, got:\n%s", cfg)
	}
	if strings.Count(cfg, "'host']") != 1 {
		t.Fatal("host directive must appear exactly once")
	}
}

func TestPMAConfigTunnelHostIsLoopback(t *testing.T) {
	server := testServerConfig()
	server.Tunnel.Enabled = true
	cfg := ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), "127.0.0.1", 43210)
	if !strings.Contains(cfg, "'host'] = '127.0.0.1'") || !strings.Contains(cfg, "'port'] = '43210'") {
		t.Fatalf("config must point at the local tunnel endpoint, got:\n%s", cfg)
	}
}

func TestBlowfishSecretLength(t *testing.T) {
	secret, err := BlowfishSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Fatalf("phpMyAdmin requires a 32-char blowfish secret, got %d", len(secret))
	}
	other, _ := BlowfishSecret()
	if other == secret {
		t.Fatal("blowfish secrets must be random")
	}
}

func TestScrubForDiagnostics(t *testing.T) {
	dirty := "failed: password s3cret-sentinel rejected; key C:\\keys\\id_rsa_sentinel unreadable"
	clean := ScrubForDiagnostics(dirty, "s3cret-sentinel", "C:\\keys\\id_rsa_sentinel")
	if strings.Contains(clean, "sentinel") {
		t.Fatalf("secrets must be redacted: %q", clean)
	}
	if !strings.Contains(clean, "[redacted]") {
		t.Fatalf("redaction marker expected: %q", clean)
	}
}

func TestCaddyfileBindsLoopbackAndClassicMode(t *testing.T) {
	caddy := BuildCaddyfile("127.0.0.1", 8123, "C:/data/pma", "C:/data/sessions/x/php.ini")
	if !strings.Contains(caddy, "http://127.0.0.1:8123") {
		t.Fatal("Caddyfile must bind the loopback port")
	}
	if !strings.Contains(caddy, "root * \"C:/data/pma\"") || !strings.Contains(caddy, "ini_file_path \"C:/data/sessions/x/php.ini\"") {
		t.Fatalf("Caddyfile paths must be quoted as one token, got:\n%s", caddy)
	}
	if !strings.Contains(caddy, "php_server") {
		t.Fatal("Caddyfile must use classic mode (php_server)")
	}
	if strings.Contains(caddy, "worker") {
		t.Fatal("worker mode is forbidden")
	}
	if !strings.Contains(caddy, "admin off") {
		t.Fatal("Caddy admin endpoint must be disabled")
	}
}

func TestCaddyfileQuotesWindowsPathsWithSpaces(t *testing.T) {
	pmaDir := `C:\Users\Andrei\AppData\Local\phpMyAdmin Desktop\runtime\sessions\connection	oken\phpmyadmin`
	phpIni := `C:\Users\Andrei\AppData\Local\phpMyAdmin Desktop\runtime\sessions\connection	oken\php.ini`
	caddy := BuildCaddyfile("127.0.0.1", 8123, pmaDir, phpIni)

	if !strings.Contains(caddy, "root * "+strconv.Quote(pmaDir)) {
		t.Fatalf("Caddyfile root must quote a Windows path containing spaces, got:\n%s", caddy)
	}
	if !strings.Contains(caddy, "ini_file_path "+strconv.Quote(phpIni)) {
		t.Fatalf("Caddyfile ini path must quote a Windows path containing spaces, got:\n%s", caddy)
	}
}

func TestContainsSecretGuard(t *testing.T) {
	server := testServerConfig()
	if err := ContainsSecret("plain text", server); err != nil {
		t.Fatalf("clean artifact rejected: %v", err)
	}
	if err := ContainsSecret("password=s3cret-sentinel", server); err == nil {
		t.Fatal("artifact embedding a credential must be rejected")
	}
}

func helperProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	// Simulate a long-running server process.
	select {}
}

// procExited reports whether the process has terminated, without reaping it
// a second time. It uses signal 0 so it works post-Kill+Wait.
func procExited(p *os.Process) error {
	if p == nil {
		return errors.New("no process")
	}
	err := p.Signal(syscall.Signal(0))
	if err == nil {
		return errors.New("process is still running")
	}
	return nil
}

// sessionDirOf resolves the unique per-attempt session directory created by
// a Start: exactly one token directory below sessions/<serverID>/.
func sessionDirOf(t *testing.T, root, serverID string) string {
	t.Helper()
	base := filepath.Join(root, "sessions", SanitizePathSegment(serverID))
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read session parent dir: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		t.Fatalf("expected exactly one session token dir under %s, got %v", base, dirs)
	}
	return filepath.Join(base, dirs[0])
}

// newSeededSessionRoot builds a runtime root with cached installs for both
// components so Session start skips network downloads.
func newSeededSessionRoot(t *testing.T) (string, *Manager) {
	t.Helper()
	root := t.TempDir()

	for _, dir := range []string{"phpmyadmin/x", "frankenphp/x", "pma-theme-darkwolf/master-snapshot/themes-master/darkwolf"} {
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "phpmyadmin", "x", "index.php"), []byte("<?php"), 0o600); err != nil {
		t.Fatal(err)
	}
	darkwolfDir := filepath.Join(root, "pma-theme-darkwolf", "master-snapshot", "themes-master", "darkwolf")
	if err := os.WriteFile(filepath.Join(darkwolfDir, "theme.json"), []byte(`{"name":"darkwolf","version":"1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		switch component {
		case ComponentFrankenPHP:
			return &ComponentDownload{Version: "x", URL: "https://example.test/f.zip"}, nil
		case ComponentPMAThemeDarkwolf:
			return &ComponentDownload{Version: themeVersionSnapshot, URL: "https://example.test/themes.zip"}, nil
		default:
			return &ComponentDownload{Version: "x", URL: "https://example.test/pma.tar.gz"}, nil
		}
	}
	seedMarkers := map[string]string{
		ComponentFrankenPHP:       "x",
		ComponentPHPMyAdmin:       "x",
		ComponentPMAThemeDarkwolf: themeVersionSnapshot,
	}
	for component, version := range seedMarkers {
		dir := filepath.Join(root, component, version)
		if err := m.writeMarker(dir, &InstallMarker{Version: version, ChecksumVerified: false}); err != nil {
			t.Fatal(err)
		}
	}
	return root, m
}

func TestSessionStartEndToEndWithStubs(t *testing.T) {
	root, m := newSeededSessionRoot(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	s := NewSession(m)
	s.sessionRoot = filepath.Join(root, "sessions")
	s.SetConfigLoader(func(id string) (*ServerConfig, error) {
		if id != "conn-1" {
			return nil, nil
		}
		return testServerConfig(), nil
	})
	spawned := make(chan *exec.Cmd, 1)
	s.spawn = func(cmd *exec.Cmd) error {
		spawned <- cmd
		return nil
	}

	if _, err := s.Start(context.Background(), "missing"); err == nil {
		t.Fatal("unknown server id must fail")
	}
	if s.Snapshot().Phase != PhaseFailed {
		t.Fatalf("expected failed phase, got %s", s.Snapshot().Phase)
	}

	s.probeURL = func(port int) string { return backend.URL + "/index.php" }
	url, err := s.Start(context.Background(), "conn-1")
	if err != nil {
		t.Fatalf("Session start: %v", err)
	}
	cmd := <-spawned
	if !strings.HasSuffix(cmd.Path, frankenphpExe) {
		t.Fatalf("unexpected executable %q", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "run") || !strings.Contains(joined, "--adapter") || !strings.Contains(joined, "caddyfile") {
		t.Fatalf("unexpected frankenphp args: %s", joined)
	}

	sessDir := sessionDirOf(t, root, "conn-1")
	if sessDir != s.currentSessionDir() {
		t.Fatalf("session must own its unique token dir %q, got %q", sessDir, s.currentSessionDir())
	}
	generated, err := os.ReadFile(filepath.Join(sessDir, "phpmyadmin", "config.inc.php"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"s3cret-sentinel", "ssh-sentinel", "pass-sentinel", "id_rsa_sentinel"} {
		if strings.Contains(string(generated), sentinel) {
			t.Fatalf("config leaks %q", sentinel)
		}
	}
	if !strings.Contains(string(generated), "'host'] = 'db.internal'") || !strings.Contains(string(generated), "'port'] = '3307'") {
		t.Fatalf("direct connection host/port must be injected, got:\n%s", generated)
	}
	if !strings.Contains(string(generated), "$cfg['ThemeDefault'] = 'darkwolf';") {
		t.Fatalf("Darkwolf must be the default theme once installed, got:\n%s", generated)
	}
	if _, err := os.Stat(filepath.Join(sessDir, "phpmyadmin", "themes", "darkwolf", "theme.json")); err != nil {
		t.Fatalf("Darkwolf theme must be present in the session tree: %v", err)
	}

	// The diagnostic status visible to the UI must never echo credentials.
	statusJSON := s.Snapshot()
	scrubbed, _ := json.Marshal(statusJSON)
	for _, sentinel := range []string{"s3cret-sentinel", "ssh-sentinel", "pass-sentinel", "id_rsa_sentinel"} {
		if strings.Contains(string(scrubbed), sentinel) {
			t.Fatalf("session status leaks %q: %s", sentinel, scrubbed)
		}
	}
	if strings.Contains(string(scrubbed), sessDir) {
		t.Fatal("session status must not carry internal session paths")
	}
	if _, err := os.Stat(filepath.Join(sessDir, "Caddyfile")); err != nil {
		t.Fatalf("per-Session Caddyfile missing: %v", err)
	}

	snap := s.Snapshot()
	if snap.Phase != PhaseReady {
		t.Fatalf("expected ready phase, got %s (%s)", snap.Phase, snap.Message)
	}
	if url == "" || !strings.Contains(url, "127.0.0.1") {
		t.Fatalf("expected loopback Session URL, got %q", url)
	}
	s.Stop()
	// After Stop the phase returns to idle and a later Start is safe.
	if s.Snapshot().Phase != PhaseIdle {
		t.Fatalf("expected idle phase after Stop, got %s", s.Snapshot().Phase)
	}
	// A successful session cleans up its unique session directory: the
	// child process and tunnel are already stopped, and no diagnostics
	// depend on it.
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("successful session dir must be removed on Stop, stat err %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "phpmyadmin", "x", "index.php")); err != nil {
		t.Fatalf("shared cached phpMyAdmin install must survive sessions: %v", err)
	}
}

func TestSessionStopKillsProcessAndTunnel(t *testing.T) {
	proc := helperProcess(t)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	s := NewSession(NewManager(t.TempDir()))
	s.sessionRoot = t.TempDir()
	sessDir := t.TempDir()

	tun := &fakeTunnel{}
	s.mu.Lock()
	s.cmd = proc
	s.tunnel = tun
	s.cancel = func() {}
	s.sessionDir = sessDir
	s.mu.Unlock()

	s.Stop()
	if !tun.closed {
		t.Fatal("tunnel must be closed by Session stop")
	}
	if err := procExited(proc.Process); err != nil {
		t.Fatalf("child process must be killed and reaped by Session stop: %v", err)
	}
	// The child and tunnel are stopped before the session directory is
	// removed; nothing keeps the attempt root deliberately here.
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("owned session dir must be removed on Stop, stat err %v", err)
	}
}

func TestSessionStopIsIdempotentAndConcurrentSafe(t *testing.T) {
	tun := &fakeTunnel{}
	s := NewSession(NewManager(t.TempDir()))
	sessDir := t.TempDir()
	s.mu.Lock()
	s.tunnel = tun
	s.cancel = func() {}
	s.sessionDir = sessDir
	s.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Stop()
		}()
	}
	wg.Wait()
	s.Stop()
	if got := atomic.LoadInt32(&tun.closeCalls); got != 1 {
		t.Fatalf("tunnel must be closed exactly once across concurrent Stops, got %d", got)
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("session dir must be removed exactly once across concurrent Stops, stat err %v", err)
	}
}

// TestConcurrentSessionsSameServerNeverShareFiles starts two sessions on the
// same server ID against one seeded runtime root: each must build its own
// phpMyAdmin tree beneath a unique token directory, and running or stopping
// one must never rebuild or remove the other's config, theme or tree.
func TestConcurrentSessionsSameServerNeverShareFiles(t *testing.T) {
	root, m := newSeededSessionRoot(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	newLiveSession := func() *Session {
		s := NewSession(m)
		s.sessionRoot = filepath.Join(root, "sessions")
		s.SetConfigLoader(func(id string) (*ServerConfig, error) { return testServerConfig(), nil })
		s.spawn = func(cmd *exec.Cmd) error { return nil }
		s.probeURL = func(port int) string { return backend.URL + "/index.php" }
		return s
	}
	s1, s2 := newLiveSession(), newLiveSession()

	type result struct {
		url string
		err error
	}
	start := func(s *Session, ch chan<- result) {
		url, err := s.Start(context.Background(), "conn-1")
		ch <- result{url, err}
	}
	ch1, ch2 := make(chan result, 1), make(chan result, 1)
	go start(s1, ch1)
	go start(s2, ch2)
	r1, r2 := <-ch1, <-ch2
	if r1.err != nil {
		t.Fatalf("first concurrent start: %v", r1.err)
	}
	if r2.err != nil {
		t.Fatalf("second concurrent start: %v", r2.err)
	}

	dir1, dir2 := s1.currentSessionDir(), s2.currentSessionDir()
	if dir1 == "" || dir2 == "" {
		t.Fatalf("both sessions must own a session dir, got %q and %q", dir1, dir2)
	}
	if dir1 == dir2 {
		t.Fatalf("same-server sessions must never share a session dir: %q", dir1)
	}
	parent := filepath.Join(root, "sessions", "conn-1")
	if filepath.Dir(dir1) != parent || filepath.Dir(dir2) != parent {
		t.Fatalf("session dirs must live under %s, got %q and %q", parent, dir1, dir2)
	}
	base1, base2 := filepath.Base(dir1), filepath.Base(dir2)
	tokenChar := func(c byte) bool {
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
	}
	for _, tok := range []string{base1, base2} {
		if len(tok) != 32 {
			t.Fatalf("session token must be a 128-bit hex string, got %q", tok)
		}
		for i := 0; i < len(tok); i++ {
			if !tokenChar(tok[i]) {
				t.Fatalf("session token must be hex, got %q", tok)
			}
		}
	}

	assertTree := func(sessDir string) string {
		t.Helper()
		cfg, err := os.ReadFile(filepath.Join(sessDir, "phpmyadmin", "config.inc.php"))
		if err != nil {
			t.Fatalf("read session config in %s: %v", sessDir, err)
		}
		if !strings.Contains(string(cfg), "'host'] = 'db.internal'") {
			t.Fatalf("session config in %s must be fully generated:\n%s", sessDir, cfg)
		}
		if _, err := os.Stat(filepath.Join(sessDir, "phpmyadmin", "themes", "darkwolf", "theme.json")); err != nil {
			t.Fatalf("session %s must own its Darkwolf theme files: %v", sessDir, err)
		}
		if _, err := os.Stat(filepath.Join(sessDir, "phpmyadmin", "index.php")); err != nil {
			t.Fatalf("session %s must own a complete phpMyAdmin tree: %v", sessDir, err)
		}
		return string(cfg)
	}
	cfg1, cfg2 := assertTree(dir1), assertTree(dir2)
	if cfg1 == cfg2 {
		t.Fatal("each session must generate its own blowfish secret; configs must differ")
	}

	// Stopping one session cleans only its own attempt root; the other
	// session's running tree is left untouched.
	s1.Stop()
	if _, err := os.Stat(dir1); !os.IsNotExist(err) {
		t.Fatalf("stopped session's dir must be removed, stat err %v", err)
	}
	assertTree(dir2)
	if got := assertTree(dir2); got != cfg2 {
		t.Fatal("the surviving session's config must be byte-identical after the other session stopped")
	}

	// And the shared version-keyed cache install was never sessionized.
	cacheCfg := filepath.Join(root, "phpmyadmin", "x", "config.inc.php")
	if _, err := os.Stat(cacheCfg); !os.IsNotExist(err) {
		t.Fatal("sessions must never write config.inc.php into the shared cached phpMyAdmin install")
	}
	if _, err := os.Stat(filepath.Join(root, "phpmyadmin", "x", "index.php")); err != nil {
		t.Fatalf("shared cached phpMyAdmin tree must stay intact: %v", err)
	}

	s2.Stop()
	if _, err := os.Stat(dir2); !os.IsNotExist(err) {
		t.Fatalf("second session's dir must be removed on Stop, stat err %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("session parent dir must remain readable: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("session parent dir must be empty once both attempts released their unique roots, found %d entries", len(entries))
	}
}

// TestConcurrentStartStopSerialized exercises Start and Stop racing each
// other: the lifecycle mutex must serialize them without deadlock, and the
// session must end fully stopped exactly once.
func TestConcurrentStartStopSerialized(t *testing.T) {
	root, m := newSeededSessionRoot(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	s := NewSession(m)
	s.sessionRoot = filepath.Join(root, "sessions")
	s.SetConfigLoader(func(id string) (*ServerConfig, error) { return testServerConfig(), nil })
	s.spawn = func(cmd *exec.Cmd) error { return nil }
	s.probeURL = func(port int) string { return backend.URL + "/index.php" }

	done := make(chan struct{})
	go func() {
		// Multiple interleaved Stop calls must not deadlock with an
		// in-flight Start, and cleanup must keep working once.
		for i := 0; i < 10; i++ {
			s.Stop()
		}
		close(done)
	}()
	_, err := s.Start(context.Background(), "conn-1")
	<-done
	s.Stop()

	// After everything serialized, no session state may be cached and the
	// last Stop left a clean, stopped session.
	if s.currentSessionDir() != "" {
		t.Fatalf("session dir reference must be released after serialization, got %q", s.currentSessionDir())
	}
	if s.started {
		t.Fatal("concurrent Start/Stop serialization must end with the session stopped")
	}
	if err == nil && s.Snapshot().Phase != PhaseIdle {
		t.Fatalf("final Stop must reset a previously started session to idle, got %s", s.Snapshot().Phase)
	}
}

type fakeTunnel struct {
	addr       string
	closed     bool
	closeCalls int32
}

func (f *fakeTunnel) Start(ctx context.Context) error { return nil }
func (f *fakeTunnel) Addr() string                    { return f.addr }
func (f *fakeTunnel) Close() error {
	f.closed = true
	atomic.AddInt32(&f.closeCalls, 1)
	return nil
}

func TestSessionTunnelFlowUsesLoopbackEndpoint(t *testing.T) {
	root, m := newSeededSessionRoot(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	s := NewSession(m)
	s.sessionRoot = filepath.Join(root, "sessions")
	server := testServerConfig()
	server.Tunnel.Enabled = true
	s.SetConfigLoader(func(id string) (*ServerConfig, error) { return server, nil })
	s.newTunnel = func() TunnelRunner { return &fakeTunnel{addr: "127.0.0.1:41881"} }
	s.spawn = func(cmd *exec.Cmd) error { return nil }
	s.probeURL = func(port int) string { return backend.URL + "/index.php" }

	if _, err := s.Start(context.Background(), "conn-1"); err != nil {
		t.Fatalf("tunnel Session start: %v", err)
	}
	sessDir := sessionDirOf(t, root, "conn-1")
	generated, err := os.ReadFile(filepath.Join(sessDir, "phpmyadmin", "config.inc.php"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "'host'] = '127.0.0.1'") {
		t.Fatalf("config must target the tunnel endpoint, got:\n%s", generated)
	}
	if !strings.Contains(string(generated), "'port'] = '41881'") {
		t.Fatalf("config must use the tunnel port, got:\n%s", generated)
	}
	s.Stop()
}

// startFailTunnel tracks Close calls so tests can assert that a tunnel which
// started successfully is always released when the rest of startup fails.
type startFailTunnel struct {
	fakeTunnel
	startErr error
}

func (f *startFailTunnel) Start(ctx context.Context) error { return f.startErr }

// readinessBlockedSession returns a Session whose probe target never answers
// successfully, forcing the readiness-failure cleanup path.
func readinessBlockedSession(t *testing.T, m *Manager) *Session {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)

	s := NewSession(m)
	s.sessionRoot = t.TempDir()
	server := testServerConfig()
	server.Tunnel.Enabled = true
	s.SetConfigLoader(func(id string) (*ServerConfig, error) { return server, nil })
	s.spawn = func(cmd *exec.Cmd) error { return nil }
	s.probeURL = func(port int) string { return backend.URL + "/index.php" }
	return s
}

func TestSessionReadinessFailureCleansUpTunnelAndProcess(t *testing.T) {
	old := readinessTimeout
	readinessTimeout = 600 * time.Millisecond
	t.Cleanup(func() { readinessTimeout = old })

	root, m := newSeededSessionRoot(t)
	s := readinessBlockedSession(t, m)
	s.sessionRoot = filepath.Join(root, "sessions")

	tun := &startFailTunnel{}
	tun.addr = "127.0.0.1:41881"
	s.newTunnel = func() TunnelRunner { return tun }

	// Run a real long-lived child so kill/reap can be asserted.
	spawnedCmd := make(chan *exec.Cmd, 1)
	s.spawn = func(cmd *exec.Cmd) error {
		real := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		real.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := real.Start(); err != nil {
			return err
		}
		cmd.Process = real.Process
		spawnedCmd <- cmd
		return nil
	}

	if _, err := s.Start(context.Background(), "conn-1"); err == nil {
		t.Fatal("readiness failure must surface an error")
	}
	if !tun.closed {
		t.Fatal("readiness failure must close a successfully started tunnel")
	}
	cmd := <-spawnedCmd
	if cmd.Process == nil {
		t.Fatal("spawned child process was not tracked")
	}
	// Start must not return before cleanup terminated and reaped the child.
	if err := procExited(cmd.Process); err != nil {
		t.Fatalf("readiness failure must terminate and reap the child process: %v", err)
	}
	if s.Snapshot().Phase != PhaseFailed {
		t.Fatalf("expected failed phase, got %s", s.Snapshot().Phase)
	}

	// Failed-attempt sessions retain their unique session directory
	// deliberately: newSeededSessionRoot seeds no runtime on the managed
	// root, and the bounded FrankenPHP logs under the session dir are the
	// only startup diagnostics. The directory contains no credentials
	// (blowfish secret is inert on disk; scrubbing applies to any surfaced
	// content).
	sessDir := sessionDirOf(t, root, "conn-1")
	if _, err := os.Stat(sessDir); err != nil {
		t.Fatalf("failed session dir must be retained for diagnostics: %v", err)
	}
	// The scrubbed failure message exposed to the UI must not carry the
	// internal session token path.
	if strings.Contains(s.Snapshot().Message, sessDir) {
		t.Fatal("failure message must not leak the internal session directory")
	}
}

func TestSessionRetryAfterFailureLeavesNoOrphans(t *testing.T) {
	root, m := newSeededSessionRoot(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	s := NewSession(m)
	s.sessionRoot = filepath.Join(root, "sessions")
	server := testServerConfig()
	server.Tunnel.Enabled = true
	s.SetConfigLoader(func(id string) (*ServerConfig, error) { return server, nil })

	var tunnels []*startFailTunnel
	s.newTunnel = func() TunnelRunner {
		tun := &startFailTunnel{}
		tun.addr = "127.0.0.1:41881"
		tunnels = append(tunnels, tun)
		return tun
	}

	// First attempt fails at spawn; the retry must not inherit any partial
	// state (tunnel, context) from the failed attempt.
	spawnErr := errors.New("simulated spawn failure")
	attempt := 0
	s.spawn = func(cmd *exec.Cmd) error {
		attempt++
		if attempt == 1 {
			return spawnErr
		}
		return nil
	}
	s.probeURL = func(port int) string { return backend.URL + "/index.php" }

	if _, err := s.Start(context.Background(), "conn-1"); err == nil {
		t.Fatal("first attempt must fail")
	}
	if len(tunnels) != 1 || !tunnels[0].closed || atomic.LoadInt32(&tunnels[0].closeCalls) != 1 {
		t.Fatalf("failed attempt must close its tunnel exactly once, tunnels=%d", len(tunnels))
	}
	// The failed attempt retains its session directory for diagnostics by
	// policy: the phase was recorded before cleanup ran, so teardown keeps
	// the tree/logs instead of destroying evidence of the failure.
	failedDir := sessionDirOf(t, root, "conn-1")

	if _, err := s.Start(context.Background(), "conn-1"); err != nil {
		t.Fatalf("retry after failure must succeed: %v", err)
	}
	retryDir := s.currentSessionDir()
	if !strings.HasPrefix(retryDir, filepath.Join(root, "sessions", "conn-1")) {
		t.Fatalf("retry dir must live under the connection's session root, got %q", retryDir)
	}
	if retryDir == failedDir {
		t.Fatal("retry must create a fresh unique session dir, not reuse the failed attempt's")
	}
	if entries, err := os.ReadDir(filepath.Dir(retryDir)); err != nil || len(entries) != 2 {
		t.Fatalf("retry's fresh dir must sit alongside the retained failed dir, entries=%v err=%v", entries, err)
	}
	if len(tunnels) != 2 {
		t.Fatalf("retry must create a fresh tunnel, got %d", len(tunnels))
	}
	if atomic.LoadInt32(&tunnels[0].closeCalls) != 1 {
		t.Fatal("retry must not double-close the previous tunnel")
	}
	s.Stop()
	if atomic.LoadInt32(&tunnels[1].closeCalls) != 1 {
		t.Fatal("Stop must close the second tunnel exactly once")
	}
	if _, err := os.Stat(retryDir); !os.IsNotExist(err) {
		t.Fatalf("successful retry session dir must be removed on Stop, stat err %v", err)
	}
	// The failed first attempt's directory is retained by policy, so it
	// survives its successor's lifecycle; each attempt owns exactly its own
	// root and cleanup never crosses between attempts.
	if _, err := os.Stat(failedDir); err != nil {
		t.Fatalf("retained failed-attempt dir must survive Stop of the retry: %v", err)
	}
}

func TestConfigLoaderFindsServer(t *testing.T) {
	store := &memConfigStore{data: `{"list":[{"id":"a","name":"A"},{"id":"b","name":"B"}]}`}
	loader := NewServerConfigLoader(store)
	found, err := loader("b")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil || found.Name != "B" {
		t.Fatalf("unexpected result %+v", found)
	}
	missing, _ := loader("zzz")
	if missing != nil {
		t.Fatal("unknown id must resolve to nil")
	}
}

func TestGetServersConfigParsesCatalogue(t *testing.T) {
	store := &memConfigStore{data: `{"list":[{"id":"a","name":"A"}]}`}
	cfg, err := GetServersConfig(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.List) != 1 || cfg.List[0].Name != "A" {
		t.Fatalf("unexpected config %+v", cfg)
	}
	bad := &memConfigStore{data: `{invalid`}
	if _, err := GetServersConfig(bad); err == nil {
		t.Fatal("malformed JSON must error")
	}
}

type memConfigStore struct {
	data string
}

func (m *memConfigStore) Get(filename string, defaultValue string) (wailsconfigstore.Config, error) {
	return wailsconfigstore.Config(m.data), nil
}

var _ = fmt.Sprintf
