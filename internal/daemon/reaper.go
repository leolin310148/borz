package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// reaperTickInterval is how often the idle-tab reaper scans tabs.
const reaperTickInterval = 60 * time.Second

// idleTabCloser closes a tab via CDP. Abstracted for tests.
type idleTabCloser interface {
	BrowserCommand(method string, params interface{}) (json.RawMessage, error)
}

// runIdleTabReaper runs the reap loop until ctx is canceled. threshold is the
// idle duration that triggers a close; tickEvery controls scan cadence.
// activeTargetID is consulted at scan time so the currently-focused tab is
// never reaped.
func runIdleTabReaper(
	ctx context.Context,
	tm *TabStateManager,
	closer idleTabCloser,
	threshold time.Duration,
	tickEvery time.Duration,
	activeTargetID func() string,
	now func() time.Time,
) {
	if threshold <= 0 {
		return
	}
	if now == nil {
		now = time.Now
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reapOnce(tm, closer, threshold, activeTargetID(), now())
		}
	}
}

// reapOnce performs a single scan-and-close pass. Exported within the package
// so tests can drive it deterministically.
func reapOnce(
	tm *TabStateManager,
	closer idleTabCloser,
	threshold time.Duration,
	activeTargetID string,
	now time.Time,
) []string {
	var closed []string
	tabs := tm.AllTabs()
	activeTargetIsStale := false
	if activeTargetID != "" {
		activeTargetIsStale = true
		for _, tab := range tabs {
			if tab.TargetID == activeTargetID {
				activeTargetIsStale = false
				break
			}
		}
	}
	if activeTargetID == "" && len(tabs) > 0 {
		// If Chrome has not told us which page is foregrounded, keep the most
		// recently touched tab as a fallback so an idle scan cannot close every
		// page in the browser.
		var keep *TabState
		for _, tab := range tabs {
			if keep == nil || tab.IdleSince().After(keep.IdleSince()) {
				keep = tab
			}
		}
		activeTargetID = keep.TargetID
	}

	var candidates []*TabState
	for _, tab := range tabs {
		if tab.TargetID == activeTargetID {
			continue
		}
		if now.Sub(tab.IdleSince()) < threshold {
			continue
		}
		candidates = append(candidates, tab)
	}

	if len(candidates) == 0 {
		return closed
	}

	if activeTargetIsStale && len(candidates) == len(tabs) {
		// Chrome may close the browser window when its last page target is
		// closed. If our active pointer is stale and every known tab is idle,
		// create a blank page first so the old tabs can still be reaped.
		if _, err := closer.BrowserCommand("Target.createTarget", map[string]interface{}{
			"url": "about:blank",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "idle-tab reaper: create about:blank failed: %v\n", err)
			return closed
		}
	}

	for _, tab := range candidates {
		_, err := closer.BrowserCommand("Target.closeTarget", map[string]interface{}{
			"targetId": tab.TargetID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "idle-tab reaper: close %s failed: %v\n", tab.ShortID, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "idle-tab reaper: closed %s (idle %s)\n", tab.ShortID, now.Sub(tab.IdleSince()).Round(time.Second))
		closed = append(closed, tab.TargetID)
	}
	return closed
}
