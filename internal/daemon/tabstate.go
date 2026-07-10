package daemon

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/leolin310148/borz/internal/protocol"
)

const (
	networkCapacity = 500
	consoleCapacity = 200
	errorsCapacity  = 100
)

// TabState holds per-tab event state.
type TabState struct {
	TargetID string
	ShortID  string

	mu sync.RWMutex

	NetworkRequests *RingBuffer[protocol.NetworkRequestInfo]
	ConsoleMessages *RingBuffer[protocol.ConsoleMessageInfo]
	JSErrors        *RingBuffer[protocol.JSErrorInfo]

	// Seq of the last user-initiated action on this tab.
	LastActionSeq int

	// Wall-clock time of tab registration. Stable after construction.
	CreatedAt time.Time

	// Unix nanos of the most recent RecordAction call, 0 if none.
	// Read concurrently by the idle reaper, so use atomic ops via the helpers.
	lastActionUnixNano atomic.Int64

	// Element refs from the most recent snapshot.
	Refs map[string]*protocol.RefInfo

	// PrevDiffSnapshot is the baseline used by `snapshot --diff`. It is
	// rewritten after every successful tree-mode snapshot so the next
	// `--diff` call has something to compare against. Cleared (left as
	// nil) on text-mode or when the tree build fails.
	PrevDiffSnapshot *DiffSnapshot

	// Active frame ID for iframe navigation, empty = main frame.
	ActiveFrameID string

	// Dialog auto-handler config.
	DialogHandler *DialogHandler

	nextSeq func() int
}

// DialogHandler configures automatic dialog handling.
type DialogHandler struct {
	Accept     bool
	PromptText string
}

func (ts *TabState) SetDialogHandler(handler *DialogHandler) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.DialogHandler = handler
}

func (ts *TabState) ConsumeDialogHandler() *DialogHandler {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	handler := ts.DialogHandler
	ts.DialogHandler = nil
	return handler
}

func newTabState(targetID, shortID string, nextSeq func() int) *TabState {
	return &TabState{
		TargetID:        targetID,
		ShortID:         shortID,
		NetworkRequests: NewRingBuffer[protocol.NetworkRequestInfo](networkCapacity),
		ConsoleMessages: NewRingBuffer[protocol.ConsoleMessageInfo](consoleCapacity),
		JSErrors:        NewRingBuffer[protocol.JSErrorInfo](errorsCapacity),
		Refs:            make(map[string]*protocol.RefInfo),
		nextSeq:         nextSeq,
		CreatedAt:       time.Now(),
	}
}

// RecordAction increments global seq and records it as this tab's last action.
func (ts *TabState) RecordAction() int {
	ts.mu.Lock()
	seq := ts.nextSeq()
	ts.LastActionSeq = seq
	ts.mu.Unlock()
	ts.lastActionUnixNano.Store(time.Now().UnixNano())
	return seq
}

// TouchActivity bumps the idle-reaper timestamp without touching LastActionSeq.
// Use for read-only handlers so any access to a tab extends the auto-close
// timer while preserving `since: "last_action"` query semantics.
func (ts *TabState) TouchActivity() {
	ts.lastActionUnixNano.Store(time.Now().UnixNano())
}

// IdleSince returns the time of the most recent action, or CreatedAt if none.
// Used by the idle-tab reaper.
func (ts *TabState) IdleSince() time.Time {
	if n := ts.lastActionUnixNano.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return ts.CreatedAt
}

// LastActionSequence returns the last user-initiated action sequence.
func (ts *TabState) LastActionSequence() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.LastActionSeq
}

// EventCounts returns counts for the per-tab event buffers.
func (ts *TabState) EventCounts() (networkRequests, consoleMessages, jsErrors int) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.NetworkRequests.Size(), ts.ConsoleMessages.Size(), ts.JSErrors.Size()
}

// AddNetworkRequest adds a new network request event.
func (ts *TabState) AddNetworkRequest(requestID string, info protocol.NetworkRequestInfo) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	seq := ts.nextSeq()
	info.RequestID = requestID
	info.Seq = seq
	ts.NetworkRequests.Push(info)
}

// UpdateNetworkResponse updates an in-flight request with response data.
func (ts *TabState) UpdateNetworkResponse(requestID string, status *int, statusText string, headers map[string]string, mimeType string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry := ts.findNetworkRequest(requestID)
	if entry == nil {
		return
	}
	if status != nil {
		entry.Status = status
	}
	if statusText != "" {
		entry.StatusText = statusText
	}
	if headers != nil {
		entry.ResponseHeaders = headers
	}
	if mimeType != "" {
		entry.MimeType = mimeType
	}
}

// UpdateNetworkFailure marks a request as failed.
func (ts *TabState) UpdateNetworkFailure(requestID, reason string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry := ts.findNetworkRequest(requestID)
	if entry == nil {
		return
	}
	entry.Failed = true
	entry.FailureReason = reason
}

func (ts *TabState) findNetworkRequest(requestID string) *protocol.NetworkRequestInfo {
	if requestID == "" {
		return nil
	}
	return ts.NetworkRequests.Find(func(info *protocol.NetworkRequestInfo) bool {
		return info.RequestID == requestID
	})
}

// AddConsoleMessage adds a console message event.
func (ts *TabState) AddConsoleMessage(info protocol.ConsoleMessageInfo) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	seq := ts.nextSeq()
	info.Seq = seq
	ts.ConsoleMessages.Push(info)
}

// AddJSError adds a JavaScript error event.
func (ts *TabState) AddJSError(info protocol.JSErrorInfo) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	seq := ts.nextSeq()
	info.Seq = seq
	ts.JSErrors.Push(info)
}

// QueryOptions for filtering event queries.
type QueryOptions struct {
	Since  interface{} // int or "last_action"
	Filter string
	Method string
	Status string
	Limit  int
}

// QueryResult holds filtered items and a cursor for incremental queries.
type QueryResult[T any] struct {
	Items  []T
	Cursor int
}

func (ts *TabState) sinceThreshold(since interface{}) int {
	if since == nil {
		return 0
	}
	switch v := since.(type) {
	case string:
		v = strings.TrimSpace(v)
		if strings.EqualFold(v, "last_action") {
			return ts.LastActionSeq
		}
		n, _ := strconv.Atoi(v)
		return n
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

// GetNetworkRequests returns filtered network requests.
func (ts *TabState) GetNetworkRequests(opts QueryOptions) QueryResult[protocol.NetworkRequestInfo] {
	ts.mu.RLock()
	items := ts.NetworkRequests.ToSlice()
	threshold := ts.sinceThreshold(opts.Since)
	ts.mu.RUnlock()
	filter := strings.TrimSpace(opts.Filter)
	method := strings.TrimSpace(opts.Method)
	status := strings.TrimSpace(opts.Status)

	return queryEvents(items, threshold, opts.Limit, func(item protocol.NetworkRequestInfo) int {
		return item.Seq
	}, func(item protocol.NetworkRequestInfo) bool {
		if filter != "" && !strings.Contains(item.URL, filter) {
			return false
		}
		if method != "" && !strings.EqualFold(item.Method, method) {
			return false
		}
		if status != "" {
			if !matchStatus(item.Status, status) {
				return false
			}
		}
		return true
	})
}

// GetConsoleMessages returns filtered console messages.
func (ts *TabState) GetConsoleMessages(opts QueryOptions) QueryResult[protocol.ConsoleMessageInfo] {
	ts.mu.RLock()
	items := ts.ConsoleMessages.ToSlice()
	threshold := ts.sinceThreshold(opts.Since)
	ts.mu.RUnlock()

	return queryEvents(items, threshold, opts.Limit, func(item protocol.ConsoleMessageInfo) int {
		return item.Seq
	}, func(item protocol.ConsoleMessageInfo) bool {
		if opts.Filter != "" && !strings.Contains(item.Text, opts.Filter) {
			return false
		}
		return true
	})
}

// GetJSErrors returns filtered JavaScript errors.
func (ts *TabState) GetJSErrors(opts QueryOptions) QueryResult[protocol.JSErrorInfo] {
	ts.mu.RLock()
	items := ts.JSErrors.ToSlice()
	threshold := ts.sinceThreshold(opts.Since)
	ts.mu.RUnlock()

	return queryEvents(items, threshold, opts.Limit, func(item protocol.JSErrorInfo) int {
		return item.Seq
	}, func(item protocol.JSErrorInfo) bool {
		if opts.Filter != "" && !strings.Contains(item.Message, opts.Filter) {
			if item.URL == "" || !strings.Contains(item.URL, opts.Filter) {
				return false
			}
		}
		return true
	})
}

func queryEvents[T any](items []T, threshold, limit int, seq func(T) int, match func(T) bool) QueryResult[T] {
	var filtered []T
	for _, item := range items {
		if threshold > 0 && seq(item) <= threshold {
			continue
		}
		if match != nil && !match(item) {
			continue
		}
		filtered = append(filtered, item)
	}

	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}

	cursor := threshold
	for _, item := range filtered {
		if itemSeq := seq(item); itemSeq > cursor {
			cursor = itemSeq
		}
	}
	return QueryResult[T]{Items: filtered, Cursor: cursor}
}

func (ts *TabState) ClearNetwork() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.NetworkRequests.Clear()
}

func (ts *TabState) ClearConsole() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ConsoleMessages.Clear()
}

func (ts *TabState) ClearErrors() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.JSErrors.Clear()
}

func matchStatus(status *int, pattern string) bool {
	if status == nil {
		return false
	}
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	s := *status
	if len(pattern) == 3 && pattern[1:] == "xx" && pattern[0] >= '1' && pattern[0] <= '5' {
		base := int(pattern[0]-'0') * 100
		return s >= base && s < base+100
	}
	code, err := strconv.Atoi(pattern)
	if err != nil {
		return false
	}
	return s == code
}

// TabStateManager manages all tabs and the global seq counter.
type TabStateManager struct {
	seq           atomic.Int64
	mu            sync.RWMutex
	tabs          map[string]*TabState // targetId -> TabState
	shortToTarget map[string]string    // shortId -> targetId
	targetToShort map[string]string    // targetId -> shortId
}

// NewTabStateManager creates a new manager.
func NewTabStateManager() *TabStateManager {
	return &TabStateManager{
		tabs:          make(map[string]*TabState),
		shortToTarget: make(map[string]string),
		targetToShort: make(map[string]string),
	}
}

func (m *TabStateManager) generateShortID(targetID string) string {
	lower := strings.ToLower(targetID)
	for length := 4; length <= len(lower); length++ {
		candidate := lower[len(lower)-length:]
		if _, exists := m.shortToTarget[candidate]; !exists {
			return candidate
		}
	}
	return lower
}

// NextSeq returns the next globally monotonic sequence number.
func (m *TabStateManager) NextSeq() int {
	return int(m.seq.Add(1))
}

// CurrentSeq returns the current seq without incrementing.
func (m *TabStateManager) CurrentSeq() int {
	return int(m.seq.Load())
}

// AddTab registers a new tab, returning the TabState.
func (m *TabStateManager) AddTab(targetID string) *TabState {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.tabs[targetID]; ok {
		return existing
	}

	shortID := m.generateShortID(targetID)
	tab := newTabState(targetID, shortID, m.NextSeq)
	m.tabs[targetID] = tab
	m.shortToTarget[shortID] = targetID
	m.targetToShort[targetID] = shortID
	return tab
}

// RemoveTab removes a tab.
func (m *TabStateManager) RemoveTab(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tab, ok := m.tabs[targetID]
	if !ok {
		return
	}
	delete(m.shortToTarget, tab.ShortID)
	delete(m.targetToShort, targetID)
	delete(m.tabs, targetID)
}

// GetTab returns a tab by targetId.
func (m *TabStateManager) GetTab(targetID string) *TabState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tabs[targetID]
}

// ResolveShortID resolves a short ID to a targetId.
func (m *TabStateManager) ResolveShortID(shortID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shortToTarget[shortID]
}

// GetShortID returns the short ID for a targetId.
func (m *TabStateManager) GetShortID(targetID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.targetToShort[targetID]
}

// AllTabs returns all active tab states in deterministic creation order.
func (m *TabStateManager) AllTabs() []*TabState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tabs := make([]*TabState, 0, len(m.tabs))
	for _, tab := range m.tabs {
		tabs = append(tabs, tab)
	}
	sort.Slice(tabs, func(i, j int) bool {
		if tabs[i].CreatedAt.Equal(tabs[j].CreatedAt) {
			return tabs[i].TargetID < tabs[j].TargetID
		}
		return tabs[i].CreatedAt.Before(tabs[j].CreatedAt)
	})
	return tabs
}
