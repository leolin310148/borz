package daemon

import (
	"context"
	"fmt"
	"os"
	"time"
)

// runTabLimitEnforcer periodically retries the cap after transient CDP close
// failures. New target registration also enforces immediately.
func runTabLimitEnforcer(ctx context.Context, tickEvery time.Duration, enforce func()) {
	if enforce == nil || tickEvery <= 0 {
		return
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			enforce()
		}
	}
}

// closeTabsOverLimit closes the oldest non-current tabs until the number of
// retained page tabs is at or below maxTabs. Tabs with an in-flight close are
// excluded from both the count and the candidate list.
func closeTabsOverLimit(
	tm *TabStateManager,
	closer idleTabCloser,
	maxTabs int,
	currentTargetID string,
	isClosing func(string) bool,
	markClosing func(string, bool),
) []string {
	if maxTabs <= 0 {
		return nil
	}
	if isClosing == nil {
		isClosing = func(string) bool { return false }
	}
	if markClosing == nil {
		markClosing = func(string, bool) {}
	}

	tabs := tm.AllTabs()
	retained := 0
	for _, tab := range tabs {
		if !isClosing(tab.TargetID) {
			retained++
		}
	}
	excess := retained - maxTabs
	if excess <= 0 {
		return nil
	}

	closed := make([]string, 0, excess)
	for _, tab := range tabs {
		if len(closed) >= excess {
			break
		}
		if tab.TargetID == currentTargetID || isClosing(tab.TargetID) {
			continue
		}
		markClosing(tab.TargetID, true)
		if _, err := closer.BrowserCommand("Target.closeTarget", map[string]interface{}{
			"targetId": tab.TargetID,
		}); err != nil {
			markClosing(tab.TargetID, false)
			fmt.Fprintf(os.Stderr, "tab limit: close %s failed: %v\n", tab.ShortID, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "tab limit: closed %s (oldest retained tab; maxTabs=%d)\n", tab.ShortID, maxTabs)
		closed = append(closed, tab.TargetID)
	}
	return closed
}
