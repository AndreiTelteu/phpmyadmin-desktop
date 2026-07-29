package runtime

import (
	"context"
	"fmt"
	"path/filepath"
)

const (
	// ComponentPMAThemeDarkwolf is the cache key of the bundled session theme.
	ComponentPMAThemeDarkwolf = "pma-theme-darkwolf"
	// ThemeDefaultName is the phpMyAdmin theme directory and ThemeDefault
	// value written into generated configs once the theme is installed.
	ThemeDefaultName = "darkwolf"

	themesMasterArchiveURL = "https://github.com/phpmyadmin/themes/archive/refs/heads/master.zip"
	// themeVersionSnapshot identifies the moving master-branch snapshot. The
	// upstream themes repository publishes no releases and no checksums for
	// the branch archive, so the cache key is a rolling snapshot and the
	// install marker records the limitation instead of inventing a hash.
	themeVersionSnapshot = "master-snapshot"
)

// DarkwolfThemeDownload resolves the official phpMyAdmin themes repository
// master-branch archive. There is no upstream checksum for this artifact by
// design; callers must record ChecksumVerified=false in the install marker.
func DarkwolfThemeDownload(ctx context.Context) (*ComponentDownload, error) {
	return &ComponentDownload{
		Version: themeVersionSnapshot,
		URL:     themesMasterArchiveURL,
	}, nil
}

// themeDir returns the installed darkwolf theme directory (containing its
// theme.json) inside an installed component tree. The archive root
// (themes-master) is preserved so upgrades of the wrapper stay obvious.
func themeDir(componentRoot string) string {
	return filepath.Join(componentRoot, "themes-master", "darkwolf")
}

// EnsureTheme downloads and installs the Darkwolf theme with the same
// discipline as the other components: bounded streamed download, traversal
// guards, staged publish, inter-process lock, and an install marker that
// records the snapshot as checksum-unverified. progress is caller-scoped;
// nil silences progress callbacks.
func (m *Manager) EnsureTheme(ctx context.Context, progress *progressTracker) (string, *InstallMarker, error) {
	dir, marker, err := m.Ensure(ctx, ComponentPMAThemeDarkwolf, progress)
	if err != nil {
		return "", nil, err
	}
	if _, err := m.fsys.Stat(filepath.Join(themeDir(dir), "theme.json")); err != nil {
		return "", nil, fmt.Errorf("darkwolf theme %s is missing themes-master/darkwolf/theme.json after install: %w", themeVersionSnapshot, err)
	}
	return dir, marker, nil
}
