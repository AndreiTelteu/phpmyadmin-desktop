package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

const (
	ComponentFrankenPHP = "frankenphp"
	ComponentPHPMyAdmin = "phpmyadmin"

	maxArchiveDownloadBytes = 512 << 20
	maxMemberExtractBytes   = 512 << 20
	maxTotalExtractBytes    = 4 << 30

	InstallMarkerFile = "install.json"
	frankenphpExe     = "frankenphp.exe"
)

// InstallMarker records how an installed component was obtained. Unverified
// artifacts must keep ChecksumVerified=false so the limitation is visible
// instead of silently trusted.
type InstallMarker struct {
	Version          string `json:"version"`
	URL              string `json:"url"`
	SHA256           string `json:"sha256,omitempty"`
	ChecksumVerified bool   `json:"checksumVerified"`
	InstalledAt      string `json:"installedAt"`
}

// runtimeFS is the filesystem seam used by the runtime manager so that unit
// tests can exercise install/failure paths without a real disk layout.
type runtimeFS interface {
	MkdirAll(dir string, perm fs.FileMode) error
	RemoveAll(dir string) error
	Rename(oldpath, newpath string) error
	Create(name string) (io.WriteCloser, error)
	Open(name string) (io.ReadCloser, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
}

type osRuntimeFS struct{}

func (osRuntimeFS) MkdirAll(dir string, perm fs.FileMode) error { return os.MkdirAll(dir, perm) }
func (osRuntimeFS) RemoveAll(dir string) error                  { return os.RemoveAll(dir) }
func (osRuntimeFS) Rename(oldpath, newpath string) error        { return os.Rename(oldpath, newpath) }
func (osRuntimeFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (osRuntimeFS) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (osRuntimeFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }
func (osRuntimeFS) Create(name string) (io.WriteCloser, error) {
	return os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
}
func (osRuntimeFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) }

// Manager downloads, verifies and installs the FrankenPHP runtime and
// the phpMyAdmin application into an app-data cache keyed by version.
// It holds no mutable per-session state: progress reporting and session
// secrets are passed through call scopes instead so concurrent sessions
// sharing one Manager can never contaminate each other.
type Manager struct {
	root   string
	fsys   runtimeFS
	client *http.Client
	lookup func(ctx context.Context, component string) (*ComponentDownload, error)
	now    func() time.Time
}

func defaultRuntimeRoot() string {
	return filepath.Join(xdg.DataHome, "phpMyAdmin Desktop", "runtime")
}

func NewManager(root string) *Manager {
	return &Manager{
		root: root,
		fsys: osRuntimeFS{},
		client: &http.Client{
			Timeout: 15 * time.Minute,
		},
		lookup: defaultComponentLookup,
		now:    time.Now,
	}
}

func NewDefaultManager() *Manager {
	return NewManager(defaultRuntimeRoot())
}

func defaultComponentLookup(ctx context.Context, component string) (*ComponentDownload, error) {
	switch component {
	case ComponentFrankenPHP:
		info, err := LatestFrankenPHPDownload(ctx)
		if errors.Is(err, ErrNoOfficialChecksum) {
			// The artifact exists but upstream did not publish a checksum;
			// continue and record the limitation in the install marker.
			return info, nil
		}
		return info, err
	case ComponentPHPMyAdmin:
		return LatestPHPMyAdminDownload(ctx)
	case ComponentPMAThemeDarkwolf:
		return DarkwolfThemeDownload(ctx)
	default:
		return nil, fmt.Errorf("unsupported component: %s", component)
	}
}

func (m *Manager) componentDir(component, version string) string {
	return filepath.Join(m.root, component, version)
}

func (m *Manager) downloadsDir() string { return filepath.Join(m.root, "downloads") }
func (m *Manager) tmpDir() string       { return filepath.Join(m.root, "tmp") }

// Ensure returns the installed directory for the latest known version of a
// component, downloading and installing it when missing. The
// download/extract/publish sequence is guarded by an inter-process lock
// keyed on component+version so concurrent session processes do not truncate
// each other's partially downloaded archives or race the final publish.
// progress is a caller-scoped byte tracker; nil silences progress callbacks.
func (m *Manager) Ensure(ctx context.Context, component string, progress *progressTracker) (string, *InstallMarker, error) {
	info, err := m.lookup(ctx, component)
	if err != nil {
		return "", nil, fmt.Errorf("resolve latest %s release: %w", component, err)
	}
	if info.Version == "" || info.URL == "" {
		return "", nil, fmt.Errorf("resolve latest %s release: incomplete metadata", component)
	}

	finalDir := m.componentDir(component, info.Version)
	if marker, err := m.readMarker(finalDir); err == nil && marker.URL == info.URL {
		if progress != nil {
			progress.MarkDone(component, marker.Version)
		}
		return finalDir, marker, nil
	}

	lock := newComponentLock(m.root, component, info.Version)
	if err := lock.acquire(ctx); err != nil {
		return "", nil, err
	}
	defer lock.release()

	// Re-check after acquiring: another process may have finished the
	// install while we were waiting for the lock.
	if marker, err := m.readMarker(finalDir); err == nil && marker.URL == info.URL {
		if progress != nil {
			progress.MarkDone(component, marker.Version)
		}
		return finalDir, marker, nil
	}

	archiveExt := ".zip"
	if strings.HasSuffix(info.URL, ".tar.gz") {
		archiveExt = ".tar.gz"
	}
	if progress != nil {
		progress.setVersion(component, info.Version)
	}
	archivePath := filepath.Join(m.downloadsDir(), component+"-"+info.Version+archiveExt)
	if err := m.fsys.MkdirAll(m.downloadsDir(), 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare downloads directory: %w", err)
	}

	actualSHA, err := m.download(ctx, component, info.URL, archivePath, progress)
	if err != nil {
		return "", nil, fmt.Errorf("download %s %s: %w", component, info.Version, err)
	}
	verified := false
	if info.ChecksumSHA256 != "" {
		if !strings.EqualFold(actualSHA, info.ChecksumSHA256) {
			_ = m.fsys.RemoveAll(archivePath)
			return "", nil, fmt.Errorf("download %s %s: checksum mismatch with upstream hashes", component, info.Version)
		}
		verified = true
	}

	staging := filepath.Join(m.tmpDir(), fmt.Sprintf("%s.staging-%d-%d", component, os.Getpid(), m.now().UnixNano()))
	if err := m.fsys.RemoveAll(staging); err != nil {
		return "", nil, fmt.Errorf("clean staging directory: %w", err)
	}
	if err := m.fsys.MkdirAll(staging, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare staging directory: %w", err)
	}
	defer m.fsys.RemoveAll(staging)

	extractRoot := filepath.Join(staging, "extract")
	if err := m.fsys.MkdirAll(extractRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare extract directory: %w", err)
	}
	if archiveExt == ".zip" {
		err = extractZip(m.fsys, archivePath, extractRoot)
	} else {
		err = extractTarGz(m.fsys, archivePath, extractRoot)
	}
	if err != nil {
		return "", nil, fmt.Errorf("extract %s %s: %w", component, info.Version, err)
	}

	var content string
	if component == ComponentPMAThemeDarkwolf {
		// Keep the themes-master/ wrapper: phpMyAdmin needs the archive's
		// <root>/darkwolf tree verbatim under its themes/ directory.
		content = extractRoot
	} else {
		content, err = flattenSingleTopLevelDir(m.fsys, extractRoot, filepath.Join(staging, "tree"))
		if err != nil {
			return "", nil, fmt.Errorf("install %s %s: %w", component, info.Version, err)
		}
	}

	if err := m.fsys.RemoveAll(finalDir); err != nil {
		return "", nil, fmt.Errorf("replace previous %s install: %w", component, err)
	}
	if err := m.fsys.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return "", nil, fmt.Errorf("prepare install directory: %w", err)
	}
	if err := m.fsys.Rename(content, finalDir); err != nil {
		return "", nil, fmt.Errorf("publish %s install: %w", component, err)
	}

	marker := &InstallMarker{
		Version:          info.Version,
		URL:              info.URL,
		ChecksumVerified: verified,
		InstalledAt:      m.now().UTC().Format(time.RFC3339),
	}
	if verified {
		marker.SHA256 = actualSHA
	}
	if err := m.writeMarker(finalDir, marker); err != nil {
		return "", nil, fmt.Errorf("record %s install: %w", component, err)
	}
	if progress != nil {
		progress.MarkDone(component, marker.Version)
	}
	return finalDir, marker, nil
}

// EnsureAll installs (or reuses) every required component concurrently so a
// cold start downloads FrankenPHP, phpMyAdmin and the Darkwolf theme in
// parallel. results is keyed by component in the order of components; the
// first failure is returned after all goroutines finish so no download
// outlives the call. progress is the single caller-scoped tracker shared by
// every concurrent download of this call; nil silences progress callbacks.
func (m *Manager) EnsureAll(ctx context.Context, components []string, progress *progressTracker) (map[string]string, map[string]*InstallMarker, error) {
	type outcome struct {
		component string
		dir       string
		marker    *InstallMarker
		err       error
	}
	outcomes := make(chan outcome, len(components))
	for _, component := range components {
		component := component
		go func() {
			dir, marker, err := m.Ensure(ctx, component, progress)
			outcomes <- outcome{component, dir, marker, err}
		}()
	}
	dirs := make(map[string]string, len(components))
	markers := make(map[string]*InstallMarker, len(components))
	var firstErr error
	for range components {
		o := <-outcomes
		if o.err != nil && firstErr == nil {
			firstErr = o.err
		}
		dirs[o.component] = o.dir
		markers[o.component] = o.marker
	}
	return dirs, markers, firstErr
}

// download streams an artifact to disk, enforcing the download size limit,
// reporting byte-level progress into the caller-scoped tracker when set, and
// returning the archive's SHA-256.
func (m *Manager) download(ctx context.Context, component, url, destPath string, progress *progressTracker) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("User-Agent", githubAPIUserAgent)

	response, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", response.Status)
	}
	if progress != nil {
		progress.SetTotal(component, response.ContentLength)
	}

	out, err := m.fsys.Create(destPath)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	limited := io.LimitReader(response.Body, maxArchiveDownloadBytes+1)
	var src io.Reader = limited
	if progress != nil {
		src = &progressReader{r: limited, add: func(n int64) { progress.AddBytes(component, n) }}
	}
	written, copyErr := io.Copy(io.MultiWriter(out, h), src)
	closeErr := out.Close()
	if copyErr != nil {
		_ = m.fsys.RemoveAll(destPath)
		return "", copyErr
	}
	if closeErr != nil {
		_ = m.fsys.RemoveAll(destPath)
		return "", closeErr
	}
	if written > maxArchiveDownloadBytes {
		_ = m.fsys.RemoveAll(destPath)
		return "", fmt.Errorf("archive exceeds the %d MiB download limit", maxArchiveDownloadBytes>>20)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// progressReader pipes every byte read into add; it adds no overhead when no
// progress sink is wired because Manager.download skips it entirely.
type progressReader struct {
	r   io.Reader
	add func(n int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.add(int64(n))
	}
	return n, err
}

func (m *Manager) readMarker(dir string) (*InstallMarker, error) {
	data, err := m.fsys.ReadFile(filepath.Join(dir, InstallMarkerFile))
	if err != nil {
		return nil, err
	}
	var marker InstallMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

func (m *Manager) writeMarker(dir string, marker *InstallMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return m.fsys.WriteFile(filepath.Join(dir, InstallMarkerFile), data, 0o644)
}

// safeJoin resolves an archive member path beneath dest and rejects entries
// that are absolute, carry a volume/drive prefix, or escape via "..".
func safeJoin(dest, name string) (string, error) {
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." || cleaned == "" {
		return "", errors.New("empty archive path")
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("archive entry %q is absolute", name)
	}
	// Reject Windows volume prefixes explicitly: filepath.VolumeName is
	// platform-specific, and on Linux "C:/x" parses as a relative path.
	if len(cleaned) >= 2 && ((cleaned[0] >= 'A' && cleaned[0] <= 'Z') || (cleaned[0] >= 'a' && cleaned[0] <= 'z')) && cleaned[1] == ':' {
		return "", fmt.Errorf("archive entry %q carries a volume prefix", name)
	}
	if vol := filepath.VolumeName(cleaned); vol != "" {
		return "", fmt.Errorf("archive entry %q carries a volume prefix", name)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return filepath.Join(dest, filepath.FromSlash(cleaned)), nil
}

type countingWriter struct {
	w     io.Writer
	total *int64
	limit int64
	what  string
}

func (c *countingWriter) Write(p []byte) (int, error) {
	*c.total += int64(len(p))
	if *c.total > c.limit {
		return 0, fmt.Errorf("%s", c.what)
	}
	return c.w.Write(p)
}

// extractZip unpacks a ZIP archive with traversal guards, symlink skipping
// and per-member/total size caps.
func extractZip(fsys runtimeFS, archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat zip: %w", err)
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}

	var total int64
	for _, f := range zr.File {
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		mode := f.FileInfo().Mode()
		if mode&fs.ModeSymlink != 0 {
			continue
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := fsys.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			continue
		}
		if int64(f.UncompressedSize64) > maxMemberExtractBytes {
			return fmt.Errorf("archive member %q exceeds the %d MiB member limit", f.Name, maxMemberExtractBytes>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("read archive member %q: %w", f.Name, err)
		}
		err = writeExtracted(fsys, rc, target, f.Name, &total)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// extractTarGz unpacks a gzipped tar archive with the same guards as
// extractZip.
func extractTarGz(fsys runtimeFS, archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := fsys.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if hdr.Size > maxMemberExtractBytes {
				return fmt.Errorf("archive member %q exceeds the %d MiB member limit", hdr.Name, maxMemberExtractBytes>>20)
			}
			if err := writeExtracted(fsys, tr, target, hdr.Name, &total); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Skip links: the official phpMyAdmin tree does not need them and
			// accepting them would reopen traversal.
		default:
			continue
		}
	}
}

func writeExtracted(fsys runtimeFS, src io.Reader, target, name string, total *int64) error {
	if err := fsys.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := fsys.Create(target)
	if err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	cw := &countingWriter{
		w:     out,
		total: total,
		limit: maxTotalExtractBytes,
		what:  fmt.Sprintf("archive member %q exceeds the %d GiB total limit", name, maxTotalExtractBytes>>30),
	}
	limited := io.LimitReader(src, maxMemberExtractBytes+1)
	written, copyErr := io.Copy(cw, limited)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maxMemberExtractBytes {
		return fmt.Errorf("archive member %q exceeds the %d MiB member limit", name, maxMemberExtractBytes>>20)
	}
	return nil
}

// flattenSingleTopLevelDir moves the single top-level directory produced by
// GitHub tarballs (repo-tag/) into `target`, or the extracted tree itself
// when the archive has no wrapper directory.
func flattenSingleTopLevelDir(fsys runtimeFS, extractRoot, target string) (string, error) {
	entries, err := readDir(fsys, extractRoot)
	if err != nil {
		return "", fmt.Errorf("inspect extracted archive: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return extractRoot, nil
	}
	wrapper := filepath.Join(extractRoot, entries[0].Name())
	if err := fsys.Rename(wrapper, target); err != nil {
		return "", fmt.Errorf("flatten archive root: %w", err)
	}
	return target, nil
}

func readDir(fsys runtimeFS, dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(dir)
}
