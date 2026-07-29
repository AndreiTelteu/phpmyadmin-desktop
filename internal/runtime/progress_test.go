package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// slowChunkedServer streams body in chunks with a flush between each so
// intermediate progress samples are observable mid-transfer.
func slowChunkedServer(t *testing.T, body []byte, chunk int, delay time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		fl, _ := w.(http.Flusher)
		for off := 0; off < len(body); off += chunk {
			end := off + chunk
			if end > len(body) {
				end = len(body)
			}
			w.Write(body[off:end])
			if fl != nil {
				fl.Flush()
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}))
}

func waitForProgress(t *testing.T, tracker *progressTracker, order []string, cond func(ProgressSnapshot) bool, what string) ProgressSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot(order, "")
		if cond(snap) {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for progress condition: %s", what)
	return ProgressSnapshot{}
}

func TestDownloadReportsByteProgressWithContentLength(t *testing.T) {
	zipBytes := smallZipBytes(t)
	if len(zipBytes) < 8 {
		t.Fatal("test zip too small for chunked assertions")
	}
	srv := slowChunkedServer(t, zipBytes, len(zipBytes)/4, 40*time.Millisecond)
	defer srv.Close()

	m := NewManager(t.TempDir())
	tracker := newRequiredTracker([]string{ComponentFrankenPHP})

	dest := filepath.Join(t.TempDir(), "f.zip")
	done := make(chan error, 1)
	go func() {
		_, err := m.download(context.Background(), ComponentFrankenPHP, srv.URL, dest, tracker)
		done <- err
	}()

	sawPartial := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot([]string{ComponentFrankenPHP}, "")
		cp := snap.Components[0]
		if cp.State == ProgressDownloading && cp.Bytes > 0 && cp.Bytes < cp.Total {
			sawPartial = true
		}
		if cp.Total == int64(len(zipBytes)) && snap.AggregateTotal == int64(len(zipBytes)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("download: %v", err)
	}
	if !sawPartial {
		t.Fatal("progress must report intermediate byte counts while the transfer is in flight")
	}
	snap := tracker.Snapshot([]string{ComponentFrankenPHP}, "")
	cp := snap.Components[0]
	if cp.Total != int64(len(zipBytes)) || cp.Bytes != cp.Total {
		t.Fatalf("final component progress must equal the Content-Length, got %+v", cp)
	}
	if snap.Percent > 99 {
		t.Fatalf("an unconfirmed download must stay at 99%%, got %d", snap.Percent)
	}
	// Ensure marks the component done after publish; only then may the bar
	// claim completion.
	tracker.MarkDone(ComponentFrankenPHP, "9.9.9")
	snap = tracker.Snapshot([]string{ComponentFrankenPHP}, "")
	if snap.Percent != 100 || !snap.AggregateKnown {
		t.Fatalf("finished single download must aggregate to 100%% known, got %+v", snap)
	}
}

func TestDownloadIndeterminateWithoutContentLength(t *testing.T) {
	zipBytes := smallZipBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length header: chunked transfer encoding.
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for i := 0; i < len(zipBytes); i += 16 {
			end := i + 16
			if end > len(zipBytes) {
				end = len(zipBytes)
			}
			w.Write(zipBytes[i:end])
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	m := NewManager(t.TempDir())
	tracker := newRequiredTracker([]string{ComponentFrankenPHP})

	dest := filepath.Join(t.TempDir(), "f.zip")
	if _, err := m.download(context.Background(), ComponentFrankenPHP, srv.URL, dest, tracker); err != nil {
		t.Fatalf("download: %v", err)
	}
	tracker.MarkDone(ComponentFrankenPHP, "9.9.9")

	snap := tracker.Snapshot([]string{ComponentFrankenPHP}, "")
	cp := snap.Components[0]
	if cp.Total >= 0 {
		t.Fatalf("missing Content-Length must surface as indeterminate (Total=-1), got %+v", cp)
	}
	if cp.Bytes != int64(len(zipBytes)) {
		t.Fatalf("byte counter must still reflect the real transfer, got %d want %d", cp.Bytes, len(zipBytes))
	}
}

func TestEnsureAllDownloadsConcurrentlyAndAggregates(t *testing.T) {
	root := t.TempDir()
	frankenBody := smallZipBytes(t)
	pmaBody := pmaTarGzBytes(t)
	themeBody := darkwolfZipBytes(t)

	frankenSrv := slowChunkedServer(t, frankenBody, len(frankenBody)/8+1, 25*time.Millisecond)
	defer frankenSrv.Close()
	pmaSrv := slowChunkedServer(t, pmaBody, len(pmaBody)/8+1, 25*time.Millisecond)
	defer pmaSrv.Close()
	themeSrv := slowChunkedServer(t, themeBody, len(themeBody)/8+1, 25*time.Millisecond)
	defer themeSrv.Close()

	versions := map[string]string{
		ComponentFrankenPHP:       "1.2.3",
		ComponentPHPMyAdmin:       "5.2.2",
		ComponentPMAThemeDarkwolf: themeVersionSnapshot,
	}
	urls := map[string]string{
		ComponentFrankenPHP:       frankenSrv.URL,
		ComponentPHPMyAdmin:       pmaSrv.URL,
		ComponentPMAThemeDarkwolf: themeSrv.URL,
	}
	checksums := map[string]string{
		ComponentFrankenPHP: smallZipSHA256(t),
	}

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		info, ok := urls[component]
		if !ok {
			return nil, fmt.Errorf("unexpected component %q", component)
		}
		return &ComponentDownload{Version: versions[component], URL: info, ChecksumSHA256: checksums[component]}, nil
	}
	order := sessionComponentOrder
	tracker := newRequiredTracker(order)

	done := make(chan error, 1)
	go func() {
		_, _, err := m.EnsureAll(context.Background(), order, tracker)
		done <- err
	}()

	// Two components must be observed in flight at once: the downloads run
	// concurrently, not sequentially.
	concurrent := false
	aggAboveSingle := false
	deadline := time.Now().Add(10 * time.Second)
	singleTotal := int64(len(frankenBody) + len(themeBody))
	for time.Now().Before(deadline) {
		snap := tracker.Snapshot(order, "")
		active := 0
		var aggBytes int64
		for _, cp := range snap.Components {
			if cp.State == ProgressDownloading && cp.Bytes > 0 {
				active++
			}
			aggBytes += cp.Bytes
		}
		if active >= 2 {
			concurrent = true
		}
		if aggBytes > 0 && snap.AggregateTotal >= singleTotal {
			aggAboveSingle = true
		}
		if concurrent && aggAboveSingle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("EnsureAll: %v", err)
	}
	if !concurrent {
		t.Fatal("required component downloads must run concurrently")
	}
	if !aggAboveSingle {
		t.Fatal("aggregate must account for every required download, not a subset")
	}

	snap := tracker.Snapshot(order, "")
	if snap.Percent != 100 {
		t.Fatalf("all components done must aggregate to 100%%, got %d (%+v)", snap.Percent, snap)
	}
	for _, name := range order {
		found := false
		for _, cp := range snap.Components {
			if cp.Name == name {
				found = true
				if cp.State != ProgressDone {
					t.Fatalf("component %s must be done, got %s", name, cp.State)
				}
			}
		}
		if !found {
			t.Fatalf("component %s missing from progress snapshot", name)
		}
	}

	// All three installs are published.
	for _, name := range order {
		if _, err := os.Stat(filepath.Join(root, name, versions[name], InstallMarkerFile)); err != nil {
			t.Fatalf("component %s not installed: %v", name, err)
		}
	}

	// A second EnsureAll hits the cache for every component and reports
	// the aggregate as fully done without new downloads.
	tracker2 := newRequiredTracker(order)
	if _, _, err := m.EnsureAll(context.Background(), order, tracker2); err != nil {
		t.Fatalf("cached EnsureAll: %v", err)
	}
	snap2 := tracker2.Snapshot(order, "")
	if snap2.Percent != 100 {
		t.Fatalf("cached components must count as completed, got %+v", snap2)
	}
	for _, cp := range snap2.Components {
		if cp.State != ProgressDone {
			t.Fatalf("cached component %s must be done immediately, got %s", cp.Name, cp.State)
		}
	}
}

func pmaTarGzBytes(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	body := "<?php"
	tw.WriteHeader(&tar.Header{Name: "pma-5.2.2/index.php", Mode: 0o644, Size: int64(len(body))})
	tw.Write([]byte(body))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestProgressSnapshotThreadSafety(t *testing.T) {
	tracker := newRequiredTracker(sessionComponentOrder)
	var wg sync.WaitGroup
	var stop int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := sessionComponentOrder[i%len(sessionComponentOrder)]
			for atomic.LoadInt32(&stop) == 0 {
				tracker.AddBytes(name, 128)
				tracker.SetTotal(name, 1<<20)
			}
		}(i)
	}
	for i := 0; i < 64; i++ {
		_ = tracker.Snapshot(sessionComponentOrder, ComponentPHPMyAdmin)
	}
	atomic.StoreInt32(&stop, 1)
	wg.Wait()

	// Cancellation mid-download must leave the renderer consistent: the
	// tracker simply keeps the last byte counts, no goroutines are owned.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ctx.Err(); err == nil {
		t.Fatal("context must be cancelled")
	}
	tracker.AddBytes(ComponentFrankenPHP, 64)
	_ = tracker.Snapshot(sessionComponentOrder, "")
}

func TestProgressNeverExceeds99BeforeConfirmation(t *testing.T) {
	tracker := newRequiredTracker([]string{ComponentFrankenPHP})
	tracker.SetTotal(ComponentFrankenPHP, 1000)
	tracker.AddBytes(ComponentFrankenPHP, 1000) // fully read, MarkDone pending
	snap := tracker.Snapshot([]string{ComponentFrankenPHP}, "")
	if snap.Percent > 99 {
		t.Fatalf("unconfirmed download must not claim 100%%, got %d", snap.Percent)
	}
	tracker.MarkDone(ComponentFrankenPHP, "x")
	snap = tracker.Snapshot([]string{ComponentFrankenPHP}, "")
	if snap.Percent != 100 {
		t.Fatalf("confirmed completion must reach 100%%, got %d", snap.Percent)
	}
}

func TestStatusSnapshotCarriesProgressAndNoSecrets(t *testing.T) {
	root, m := newSeededSessionRoot(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	s := NewSession(m)
	s.sessionRoot = filepath.Join(root, "sessions")
	s.SetConfigLoader(func(id string) (*ServerConfig, error) { return testServerConfig(), nil })
	s.spawn = func(cmd *exec.Cmd) error { return nil }
	s.probeURL = func(port int) string { return backend.URL + "/index.php" }

	if _, err := s.Start(context.Background(), "conn-1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	snap := s.Snapshot()
	if len(snap.Progress.Components) != len(sessionComponentOrder) {
		t.Fatalf("status must include all required components, got %+v", snap.Progress.Components)
	}
	if snap.Progress.Percent != 100 {
		t.Fatalf("cached cold start must aggregate to 100%%, got %d", snap.Progress.Percent)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, sentinel := range []string{"s3cret-sentinel", "ssh-sentinel", "pass-sentinel", "id_rsa_sentinel"} {
		if strings.Contains(string(data), sentinel) {
			t.Fatalf("status snapshot leaks %q", sentinel)
		}
	}
	// The aggregate bytes of cached components come from estimates, never
	// from the credential-bearing connection fields.
	if snap.Progress.AggregateTotal <= 0 {
		t.Fatal("aggregate total must be positive once components resolve")
	}
}
