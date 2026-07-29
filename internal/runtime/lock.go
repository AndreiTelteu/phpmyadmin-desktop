package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// lockStaleAge bounds how long a held inter-process install lock may
	// remain before another process may reclaim it. It comfortably exceeds
	// the worst-case download/extract duration on slow links.
	lockStaleAge = 60 * time.Minute
	// lockRetryInterval is the polling cadence while waiting for another
	// process to finish installing the same component version.
	lockRetryInterval = 500 * time.Millisecond
)

var errLockHeld = errors.New("install lock held by another process")

// componentLock coordinates concurrent Manager.Ensure calls for the same
// component/version across processes. It uses atomic lock-directory
// creation (portable: works on Windows and Linux), stale-age reclaim, and
// a best-effort owner hint for observability.
type componentLock struct {
	path string
}

func newComponentLock(root, component, version string) *componentLock {
	name := fmt.Sprintf("%s-%s.lock", SanitizePathSegment(component), SanitizePathSegment(version))
	return &componentLock{path: filepath.Join(root, "locks", name)}
}

// acquire blocks until the lock is held or ctx is done.
func (l *componentLock) acquire(ctx context.Context) error {
	for {
		err := l.tryLock()
		if err == nil {
			return nil
		}
		if !errors.Is(err, errLockHeld) {
			return err
		}
		if err := l.reclaimStale(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for install lock %s: %w", filepath.Base(l.path), ctx.Err())
		case <-time.After(lockRetryInterval):
		}
	}
}

func (l *componentLock) tryLock() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return fmt.Errorf("prepare lock directory: %w", err)
	}
	err := os.Mkdir(l.path, 0o755)
	if err == nil {
		// Best-effort owner hint; the lock itself is the directory.
		_ = os.WriteFile(filepath.Join(l.path, "owner"), []byte(fmt.Sprint(os.Getpid())), 0o600)
		return nil
	}
	if os.IsExist(err) {
		return errLockHeld
	}
	return fmt.Errorf("acquire install lock: %w", err)
}

// reclaimStale removes a lock directory whose owner has disappeared (mtime
// older than lockStaleAge). It is only called after tryLock reported the
// lock held; between reclaim and the next tryLock another process may have
// acquired the lock, which is handled by the retry loop.
func (l *componentLock) reclaimStale() error {
	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect install lock: %w", err)
	}
	if time.Since(info.ModTime()) < lockStaleAge {
		return nil // still fresh; keep waiting
	}
	if err := os.RemoveAll(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reclaim stale install lock: %w", err)
	}
	return nil
}

func (l *componentLock) release() {
	// Remove the owner hint first, then the lock directory.
	_ = os.Remove(filepath.Join(l.path, "owner"))
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		// Fall back to RemoveAll in case another writer added entries.
		_ = os.RemoveAll(l.path)
	}
}
