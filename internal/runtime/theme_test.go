package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darkwolfZipBytes builds the exact shape of the official archive: a
// themes-master wrapper containing the darkwolf theme directory.
func darkwolfZipBytes(t *testing.T) []byte {
	t.Helper()
	path := writeTestZip(t, map[string]string{
		"themes-master/darkwolf/theme.json":    `{"name":"Darkwolf","version":"5.2"}`,
		"themes-master/darkwolf/info.inc.php":  "<?php",
		"themes-master/darkwolf/css/theme.css": "body{}",
		"themes-master/fallbg/theme.json":      `{"name":"Fall BG"}`,
	}, nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func serveBytes(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(body)
	}))
}

func TestEnsureDarkwolfThemeLayoutAndUnverifiedMarker(t *testing.T) {
	root := t.TempDir()
	dl := serveBytes(t, darkwolfZipBytes(t))
	defer dl.Close()

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		if component != ComponentPMAThemeDarkwolf {
			t.Fatalf("unexpected component %q", component)
		}
		return &ComponentDownload{Version: themeVersionSnapshot, URL: dl.URL}, nil
	}

	dir, marker, err := m.EnsureTheme(context.Background(), nil)
	if err != nil {
		t.Fatalf("EnsureTheme: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join(ComponentPMAThemeDarkwolf, themeVersionSnapshot)) {
		t.Fatalf("unexpected install dir %q", dir)
	}
	if marker.ChecksumVerified || marker.SHA256 != "" {
		t.Fatalf("themes master snapshot must be recorded checksum-unverified, got %+v", marker)
	}

	// Expected target layout: archive root preserved, darkwolf beneath it.
	for _, rel := range []string{
		filepath.Join("themes-master", "darkwolf", "theme.json"),
		filepath.Join("themes-master", "darkwolf", "css", "theme.css"),
		filepath.Join("themes-master", "fallbg", "theme.json"),
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("expected %s in installed theme tree: %v", rel, err)
		}
	}
}

func TestEnsureDarkwolfThemeTraversalRejected(t *testing.T) {
	root := t.TempDir()
	zipPath := writeTestZip(t, map[string]string{
		"themes-master/darkwolf/theme.json": `{}`,
	}, [][2]string{{"themes-master/../../evil.php", "evil"}})
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	dl := serveBytes(t, data)
	defer dl.Close()

	m := NewManager(root)
	m.lookup = func(ctx context.Context, component string) (*ComponentDownload, error) {
		return &ComponentDownload{Version: themeVersionSnapshot, URL: dl.URL}, nil
	}
	if _, _, err := m.EnsureTheme(context.Background(), nil); err == nil {
		t.Fatal("traversal entry in the theme archive must abort the install")
	}
	testdata := filepath.Join(root, ComponentPMAThemeDarkwolf, themeVersionSnapshot)
	if _, err := os.Stat(testdata); !os.IsNotExist(err) {
		t.Fatal("failed theme install must not be published")
	}
}

func TestDarkwolfConfigValueApplied(t *testing.T) {
	cfg := ApplyServerToPMAConfig(BuildPMAConfig("0123456789abcdef0123456789abcdef"), "db.internal", 3307)
	cfg = ApplyThemeToPMAConfig(cfg, ThemeDefaultName)
	if !strings.Contains(cfg, "$cfg['ThemeDefault'] = 'darkwolf';") {
		t.Fatalf("ThemeDefault must select darkwolf, got:\n%s", cfg)
	}
	// Theme config must not disturb the server directives.
	if !strings.Contains(cfg, "'host'] = 'db.internal'") || !strings.Contains(cfg, "'port'] = '3307'") {
		t.Fatalf("theme application broke server config:\n%s", cfg)
	}
}

func TestInstallDarkwolfThemeIntoSessionTree(t *testing.T) {
	root := t.TempDir()
	component := filepath.Join(root, ComponentPMAThemeDarkwolf, themeVersionSnapshot)
	src := filepath.Join(component, "themes-master", "darkwolf")
	os.MkdirAll(filepath.Join(src, "css"), 0o755)
	os.WriteFile(filepath.Join(src, "theme.json"), []byte(`{"name":"Darkwolf"}`), 0o600)
	os.WriteFile(filepath.Join(src, "css", "theme.css"), []byte("body{}"), 0o600)
	// A symlink in the cache tree must be skipped, not followed.
	os.Symlink("/etc/passwd", filepath.Join(src, "evil.php"))

	pma := filepath.Join(root, "sessions", "conn-9", "phpmyadmin")
	os.MkdirAll(pma, 0o755)
	if err := installDarkwolfTheme(component, pma); err != nil {
		t.Fatalf("installDarkwolfTheme: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pma, "themes", "darkwolf", "theme.json")); err != nil {
		t.Fatalf("theme must land in themes/darkwolf: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pma, "themes", "darkwolf", "css", "theme.css")); err != nil {
		t.Fatalf("theme css must be copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(pma, "themes", "darkwolf", "evil.php")); !os.IsNotExist(err) {
		t.Fatal("symlink in the cached theme must not be materialized")
	}
}

func TestInstallDarkwolfThemeMissingFailsSession(t *testing.T) {
	root := t.TempDir()
	// Component dir exists but the darkwolf tree (with theme.json) does not.
	component := filepath.Join(root, ComponentPMAThemeDarkwolf, themeVersionSnapshot)
	os.MkdirAll(component, 0o755)
	pma := filepath.Join(root, "pma")
	os.MkdirAll(pma, 0o755)
	if err := installDarkwolfTheme(component, pma); err == nil {
		t.Fatal("missing theme tree must fail instead of writing a broken ThemeDefault")
	}
}

func TestThemeArchiveLayoutMatchesOfficialRepo(t *testing.T) {
	// Guard the mapping the user supplied: archive root themes-master,
	// theme directory themes-master/darkwolf, phpMyAdmin target
	// themes/darkwolf.
	got := themeDir(filepath.Join("root", "x"))
	want := filepath.Join("root", "x", "themes-master", "darkwolf")
	if got != want {
		t.Fatalf("themeDir = %q, want %q", got, want)
	}
}

func TestDarkwolfDefaultResolverTracksOfficialMasterArchive(t *testing.T) {
	info, err := DarkwolfThemeDownload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.URL != "https://github.com/phpmyadmin/themes/archive/refs/heads/master.zip" {
		t.Fatalf("Darkwolf must come from the supplied official themes master archive, got %q", info.URL)
	}
	if info.Version != themeVersionSnapshot {
		t.Fatalf("unexpected snapshot version %q", info.Version)
	}
	if info.ChecksumSHA256 != "" {
		t.Fatal("the master-branch archive has no official checksum; none may be invented")
	}
}
