package runtime

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Progress states for a single required component download.
const (
	ProgressPending     = "pending"
	ProgressDownloading = "downloading"
	ProgressInstalling  = "installing"
	ProgressDone        = "done"
)

// ComponentProgress is the per-component slice of the install progress sent
// to the frontend. Total < 0 means the server did not send a Content-Length:
// the transfer is real but its percentage must be rendered indeterminate.
type ComponentProgress struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Bytes   int64  `json:"bytes"`
	Total   int64  `json:"total"`
	Version string `json:"version,omitempty"`
}

// ProgressSnapshot is the aggregate view of every required component
// download. Percent is only authoritative when AggregateKnown is true;
// unknown totals yield a bounded estimate so the UI shows an approximate bar
// instead of a fake timer-based animation.
type ProgressSnapshot struct {
	Components     []ComponentProgress `json:"components"`
	AggregateBytes int64               `json:"aggregateBytes"`
	AggregateTotal int64               `json:"aggregateTotal"`
	AggregateKnown bool                `json:"aggregateKnown"`
	Percent        int                 `json:"percent"`
}

type componentProgress struct {
	downloadBytes int64
	downloadTotal int64 // -1 = indeterminate (no Content-Length), 0 = unknown yet, >0 = real size
	estimate      int64 // resolved via HEAD probe; only an aggregate denominator fallback
	done          bool
	version       string
}

// progressTracker receives byte-level callbacks from Manager downloads and
// Content-Length probes, aggregating them across every component a cold
// start needs. It is safe for concurrent use and holds no resources, so
// context cancellation and retries need no cleanup beyond replacing it.
type progressTracker struct {
	mu   sync.Mutex
	comp map[string]*componentProgress
}

func newRequiredTracker(names []string) *progressTracker {
	t := &progressTracker{comp: make(map[string]*componentProgress, len(names))}
	for _, name := range names {
		t.comp[name] = &componentProgress{}
	}
	return t
}

func (t *progressTracker) get(name string) *componentProgress {
	c, ok := t.comp[name]
	if !ok {
		c = &componentProgress{}
		t.comp[name] = c
	}
	return c
}

// SetTotal records the transfer size reported by the actual download
// response. total <= 0 marks the transfer indeterminate; the aggregate then
// falls back to the HEAD-probed estimate for a bounded denominator.
func (t *progressTracker) SetTotal(name string, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.get(name)
	if total > 0 {
		c.downloadTotal = total
	} else if c.downloadTotal == 0 {
		c.downloadTotal = -1
	}
	if c.done && c.downloadTotal > 0 {
		c.downloadBytes = c.downloadTotal
	}
}

// AddBytes accumulates downloaded bytes; the Manager calls it on every chunk
// read from the network.
func (t *progressTracker) AddBytes(name string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.get(name).downloadBytes += n
}

// MarkDone marks a component fully available (fresh install or cache hit).
func (t *progressTracker) MarkDone(name, version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.get(name)
	c.done = true
	c.version = version
	if c.downloadTotal > 0 {
		c.downloadBytes = c.downloadTotal
	}
}

func (t *progressTracker) setEstimate(name string, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.get(name)
	if c.downloadTotal <= 0 {
		c.estimate = total
	}
}

func (t *progressTracker) setVersion(name, version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.get(name).version = version
}

// resolveExpectedTotal queries the artifact's Content-Length with an
// unauthenticated HEAD request so the aggregate has a real denominator even
// before the download's first byte arrives. Failure is non-fatal: the
// component aggregate falls back to a coarse size estimate.
func (t *progressTracker) resolveExpectedTotal(ctx context.Context, name, url string) {
	if url == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", githubAPIUserAgent)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 {
		t.setEstimate(name, resp.ContentLength)
	}
}

// roughSizeEstimate is the last-resort aggregate denominator used when
// neither a HEAD probe nor a Content-Length is available. It keeps the
// aggregate bounded; AggregateKnown stays false so the UI renders it as
// approximate rather than a fake exact percentage.
func roughSizeEstimate(component string) int64 {
	switch component {
	case ComponentFrankenPHP:
		return 45 << 20
	case ComponentPHPMyAdmin:
		return 80 << 20
	case ComponentPMAThemeDarkwolf:
		return 2 << 20
	default:
		return 0
	}
}

// Snapshot returns the aggregate progress. The aggregate denominator uses
// real Content-Length values where known, else HEAD-probed sizes, else a
// coarse estimate; a transferring download is capped just below its
// unconfirmed total so the bar never claims 100% before completion.
func (t *progressTracker) Snapshot(order []string, activeComponent string) ProgressSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	var (
		aggBytes int64
		aggTotal int64
		allKnown = true
		pending  int
	)
	byName := make(map[string]ComponentProgress, len(t.comp))
	for name, c := range t.comp {
		state := ProgressPending
		switch {
		case c.done:
			state = ProgressDone
		case c.downloadBytes > 0 || c.downloadTotal != 0:
			state = ProgressDownloading
		case name == activeComponent:
			state = ProgressInstalling
		}
		byName[name] = ComponentProgress{
			Name:    name,
			State:   state,
			Bytes:   c.downloadBytes,
			Total:   c.downloadTotal,
			Version: c.version,
		}

		denom := c.downloadTotal
		if denom <= 0 {
			denom = c.estimate
		}
		if denom <= 0 {
			denom = roughSizeEstimate(name)
		}
		bytesDone := c.downloadBytes
		if c.done {
			bytesDone = denom
		} else {
			pending++
			if c.downloadTotal <= 0 {
				// No confirmed size: aggregate is an estimate.
				allKnown = false
			}
			if bytesDone >= denom {
				bytesDone = denom - 1
			}
			if bytesDone < 0 {
				bytesDone = 0
			}
		}
		if c.done && c.downloadTotal <= 0 && c.estimate <= 0 {
			allKnown = false
		}
		aggBytes += bytesDone
		aggTotal += denom
	}

	out := make([]ComponentProgress, 0, len(byName))
	seen := make(map[string]bool, len(byName))
	for _, name := range order {
		if cp, ok := byName[name]; ok {
			out = append(out, cp)
			seen[name] = true
		}
	}
	var rest []string
	for name := range byName {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		out = append(out, byName[name])
	}

	percent := 0
	if aggTotal > 0 {
		percent = int(aggBytes * 100 / aggTotal)
	}
	switch {
	case pending == 0 && aggTotal > 0:
		percent = 100
	case percent > 99:
		percent = 99
	case percent < 0:
		percent = 0
	}
	if pending > 0 {
		allKnown = false
	}
	return ProgressSnapshot{
		Components:     out,
		AggregateBytes: aggBytes,
		AggregateTotal: aggTotal,
		AggregateKnown: allKnown,
		Percent:        percent,
	}
}
