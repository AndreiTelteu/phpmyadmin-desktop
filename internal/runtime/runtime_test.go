package runtime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafeJoinRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	bad := []string{
		"../evil.php",
		"..\\evil.php",
		"foo/../../evil.php",
		"/abs/path/evil.php",
		"C:\\windows\\evil.php",
		"../../../etc/passwd",
		"..",
		"",
	}
	for _, name := range bad {
		if _, err := safeJoin(dest, name); err == nil {
			t.Fatalf("safeJoin accepted traversal entry %q", name)
		}
	}
}

func TestSafeJoinAcceptsNormalEntries(t *testing.T) {
	dest := t.TempDir()
	good := map[string]string{
		"index.php":                    filepath.Join(dest, "index.php"),
		"phpmyadmin-5.2.2/index.php":   filepath.Join(dest, "phpmyadmin-5.2.2", "index.php"),
		"./libraries/classes/Util.php": filepath.Join(dest, "libraries", "classes", "Util.php"),
	}
	for name, want := range good {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Fatalf("safeJoin rejected %q: %v", name, err)
		}
		if got != want {
			t.Fatalf("safeJoin(%q) = %q, want %q", name, got, want)
		}
	}
}

func writeTestZip(t *testing.T, entries map[string]string, extraHeaders [][2]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range entries {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range extraHeaders {
		hdr := &zip.FileHeader{Name: h[0], Method: zip.Deflate}
		fw, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(h[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractZipNormal(t *testing.T) {
	zipPath := writeTestZip(t, map[string]string{
		"frankenphp.exe":     "MZ",
		"ext/php_mysqli.dll": "DLL",
	}, nil)
	dest := t.TempDir()
	if err := extractZip(osRuntimeFS{}, zipPath, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "frankenphp.exe"))
	if err != nil || string(data) != "MZ" {
		t.Fatalf("expected frankenphp.exe extracted, got %q err %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "ext", "php_mysqli.dll")); err != nil {
		t.Fatalf("expected ext dir content: %v", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	zipPath := writeTestZip(t, nil, [][2]string{{"../evil.php", "evil"}})
	dest := t.TempDir()
	if err := extractZip(osRuntimeFS{}, zipPath, dest); err == nil {
		t.Fatal("extractZip accepted traversal entry")
	}
	if _, err := os.Stat(filepath.Join(dest, "..", "evil.php")); err == nil {
		t.Fatal("traversal entry was written")
	}
}

func TestExtractTarGzNormalAndTraversal(t *testing.T) {
	write := func(entries []tarTestEntry) string {
		path := filepath.Join(t.TempDir(), "test.tar.gz")
		f, _ := os.Create(path)
		defer f.Close()
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		for _, e := range entries {
			hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
			if e.typeflag != 0 {
				hdr.Typeflag = e.typeflag
				hdr.Size = 0
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatal(err)
			}
			if hdr.Typeflag == tar.TypeReg {
				if _, err := tw.Write([]byte(e.body)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}

	dest := t.TempDir()
	good := write([]tarTestEntry{{name: "pma-5.2.2/index.php", body: "<?php"}})
	if err := extractTarGz(osRuntimeFS{}, good, dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "pma-5.2.2", "index.php")); err != nil {
		t.Fatalf("expected extracted file: %v", err)
	}

	symlinks := write([]tarTestEntry{{name: "link", body: "/etc/passwd", typeflag: tar.TypeSymlink}})
	if err := extractTarGz(osRuntimeFS{}, symlinks, dest); err != nil {
		t.Fatalf("symlink must be skipped without error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink entry must not be materialized, stat err %v", err)
	}

	evil := write([]tarTestEntry{{name: "../evil.php", body: "evil"}})
	if err := extractTarGz(osRuntimeFS{}, evil, t.TempDir()); err == nil {
		t.Fatal("extractTarGz accepted traversal entry")
	}
}

type tarTestEntry struct {
	name     string
	body     string
	typeflag byte
}

func smallZipBytes(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fw, _ := zw.Create("frankenphp.exe")
	fw.Write([]byte("MZ"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func smallZipSHA256(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256(smallZipBytes(t))
	return hex.EncodeToString(sum[:])
}

func TestEnsureInstallsAndVerifies(t *testing.T) {
	root := t.TempDir()
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(smallZipBytes(t))
	}))
	defer dl.Close()

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		return &ComponentDownload{Version: "1.2.3", URL: dl.URL, ChecksumSHA256: smallZipSHA256(t)}, nil
	}
	dir, marker, err := m.Ensure(context.Background(), ComponentFrankenPHP, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("frankenphp", "1.2.3")) {
		t.Fatalf("unexpected install dir %q", dir)
	}
	if marker == nil {
		t.Fatal("expected install marker")
	}
	if _, err := os.Stat(filepath.Join(dir, "frankenphp.exe")); err != nil {
		t.Fatalf("binary missing after install: %v", err)
	}

	// an up-to-date cached lookup metadata plus an installed marker must be
	// reused without downloading again (lookup still runs to resolve the
	// latest version).
	if _, _, err := m.Ensure(context.Background(), ComponentFrankenPHP, nil); err != nil {
		t.Fatalf("ensure cached: %v", err)
	}
}

func TestEnsureChecksumMismatchRejected(t *testing.T) {
	root := t.TempDir()
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		zw := zip.NewWriter(buf)
		fw, _ := zw.Create("frankenphp.exe")
		fw.Write([]byte("MZ"))
		zw.Close()
		w.Write(buf.Bytes())
	}))
	defer dl.Close()

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		return &ComponentDownload{
			Version:        "1.2.3",
			URL:            dl.URL,
			ChecksumSHA256: strings.Repeat("0", 64), // definitely wrong
		}, nil
	}
	if _, _, err := m.Ensure(context.Background(), ComponentFrankenPHP, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "frankenphp", "1.2.3")); !os.IsNotExist(err) {
		t.Fatal("mismatched install must not be published")
	}
}

func TestEnsurePhpMyAdminDistributionIncludesComposerDependencies(t *testing.T) {
	root := t.TempDir()
	archive := writeTestZip(t, map[string]string{
		"phpMyAdmin-5.2.2-all-languages/index.php":           "<?php",
		"phpMyAdmin-5.2.2-all-languages/vendor/autoload.php": "<?php return [];",
	}, nil)
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(data)
	}))
	defer dl.Close()

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		return &ComponentDownload{Version: "5.2.2", URL: dl.URL + "/phpMyAdmin-5.2.2-all-languages.zip"}, nil
	}
	dir, marker, err := m.Ensure(context.Background(), ComponentPHPMyAdmin, nil)
	if err != nil {
		t.Fatalf("ensure pma: %v", err)
	}
	if marker.ChecksumVerified {
		t.Fatal("phpMyAdmin distribution without an official checksum must be recorded as unverified")
	}
	for _, required := range []string{"index.php", filepath.Join("vendor", "autoload.php")} {
		if _, err := os.Stat(filepath.Join(dir, required)); err != nil {
			t.Fatalf("official phpMyAdmin distribution must install %s: %v", required, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, InstallMarkerFile))
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	var decoded InstallMarker
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("marker decode: %v", err)
	}
	if decoded.ChecksumVerified || decoded.SHA256 != "" {
		t.Fatalf("unverified checksum must be explicit, got %+v", decoded)
	}
}

func TestBuildPHPIniAppendsExtensions(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "frankenphp", "9.9.9")
	os.MkdirAll(runtimeDir, 0o755)
	os.WriteFile(filepath.Join(runtimeDir, "php.ini-production"), []byte("[PHP]\ndisplay_errors = Off\n"), 0o644)

	m := NewManager(root)
	ini, err := m.BuildPHPIni(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"extension=mysqli", "extension=pdo_mysql", "extension=mbstring", "extension_dir =",
		"upload_max_filesize = 10G", "post_max_size = 10241M",
	} {
		if !strings.Contains(ini, want) {
			t.Fatalf("generated php.ini missing %q", want)
		}
	}
	if !strings.Contains(ini, "display_errors = Off") {
		t.Fatal("base template lines must be preserved")
	}
}

func TestReadinessProbeSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.php" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/index.php?route=/", http.StatusFound)
	}))
	defer server.Close()
	if err := WaitForReady(context.Background(), server.URL, 3*time.Second); err != nil {
		t.Fatalf("readiness must pass: %v", err)
	}
}

func TestReadinessProbeTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	err := WaitForReady(context.Background(), server.URL, 1200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("expected readiness timeout, got %v", err)
	}
}

func TestReadinessProbeRejects404(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	err := WaitForReady(context.Background(), server.URL, 1200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("a 404 must never count as phpMyAdmin readiness, got %v", err)
	}
	if got := atomic.LoadInt32(&requests); got < 2 {
		t.Fatalf("a 404 must keep the probe polling until the timeout, got %d request(s)", got)
	}
}

func TestDownloadLimitEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("download limit test allocates a large body")
	}
	writer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 1<<20)
		for i := 0; i < (maxArchiveDownloadBytes>>20)+2; i++ {
			w.Write(chunk)
		}
	}))
	defer writer.Close()

	m := NewManager(t.TempDir())
	dest := filepath.Join(t.TempDir(), "big.bin")
	if _, err := m.download(context.Background(), ComponentFrankenPHP, writer.URL, dest, nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected download limit, got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("oversized download must be removed")
	}
}

// TestEnsureConcurrentSerializeInstalls runs two Ensures on the same
// component/version through a slow download. The second caller must block on
// the inter-process lock and then reuse the first install instead of
// downloading or publishing again.
func TestEnsureConcurrentSerializeInstalls(t *testing.T) {
	root := t.TempDir()
	releaseDownload := make(chan struct{})
	var downloads int32
	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&downloads, 1)
		<-releaseDownload
		w.Write(smallZipBytes(t))
	}))
	defer dl.Close()

	newSlowManager := func() *Manager {
		m := NewManager(root)
		m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
			return &ComponentDownload{Version: "9.9.9", URL: dl.URL, ChecksumSHA256: smallZipSHA256(t)}, nil
		}
		return m
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dir1, _, err := newSlowManager().Ensure(context.Background(), ComponentFrankenPHP, nil)
		if err == nil {
			if _, statErr := os.Stat(filepath.Join(dir1, "frankenphp.exe")); statErr != nil {
				err = statErr
			}
		}
		results <- err
	}()

	// Let the first caller start the download while holding the lock.
	time.Sleep(150 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dir2, _, err := newSlowManager().Ensure(context.Background(), ComponentFrankenPHP, nil)
		if err == nil {
			if _, statErr := os.Stat(filepath.Join(dir2, "frankenphp.exe")); statErr != nil {
				err = statErr
			}
		}
		results <- err
	}()

	close(releaseDownload)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Ensure: %v", err)
		}
	}
	if got := atomic.LoadInt32(&downloads); got != 1 {
		t.Fatalf("shared artifact must be downloaded exactly once, got %d", got)
	}
	// Lock must be released afterwards.
	lockDir := filepath.Join(root, "locks", ComponentFrankenPHP+"-9.9.9.lock")
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("lock directory must be removed after release, stat err %v", err)
	}
}

func TestComponentLockAcquireReleaseAndStaleReclaim(t *testing.T) {
	root := t.TempDir()
	lock := newComponentLock(root, ComponentFrankenPHP, "1.2.3")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.acquire(ctx); err != nil {
		t.Fatalf("acquire: %v", err)
	}

	// Second acquire on the held lock must respect ctx cancellation.
	blocked := newComponentLock(root, ComponentFrankenPHP, "1.2.3")
	short, shortCancel := context.WithTimeout(context.Background(), 2*lockRetryInterval+100*time.Millisecond)
	defer shortCancel()
	if err := blocked.acquire(short); err == nil {
		t.Fatal("held lock must not be acquired")
	}

	// A stale lock must be reclaimed.
	stale := newComponentLock(root, "stale", "0.0.1")
	if err := os.MkdirAll(stale.path, 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStaleAge)
	if err := os.Chtimes(stale.path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := stale.acquire(ctx); err != nil {
		t.Fatalf("stale lock must be reclaimed: %v", err)
	}
	stale.release()

	lock.release()
	if _, err := os.Stat(lock.path); !os.IsNotExist(err) {
		t.Fatalf("release must remove the lock directory, stat err %v", err)
	}
}

func TestLogTailKeepsOnlyEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString(strings.Repeat("x", 100) + "\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o600)
	tail := logTail(path, 512)
	if len(tail) > 512 {
		t.Fatalf("tail too long: %d", len(tail))
	}
}

var _ = io.Discard
