package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon/extbridge"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/recorder"
)

// RecordingInfo is returned by daemon REST recording endpoints.
type RecordingInfo struct {
	ID         string                  `json:"id"`
	Path       string                  `json:"path"`
	Mode       string                  `json:"mode"`
	URL        string                  `json:"url,omitempty"`
	Tab        string                  `json:"tab,omitempty"`
	Status     string                  `json:"status"`
	StartedAt  time.Time               `json:"started_at"`
	FinishedAt *time.Time              `json:"finished_at,omitempty"`
	FrameCount int                     `json:"frame_count"`
	EventCount int                     `json:"event_count"`
	DurationNS int64                   `json:"duration_ns"`
	Options    recorder.CaptureOptions `json:"options"`
	Error      string                  `json:"error,omitempty"`
	Manifest   *recorder.Manifest      `json:"manifest,omitempty"`
}

type recordingManager struct {
	mu        sync.Mutex
	cdp       *CdpConnection
	extHub    *extbridge.Hub
	active    map[string]*activeRecording
	completed map[string]RecordingInfo
}

type activeRecording struct {
	info      RecordingInfo
	writer    *recorder.Writer
	cancel    context.CancelFunc
	done      chan struct{}
	targetID  string
	paused    bool
	lastErr   string
	extCursor uint64
	mu        sync.Mutex
}

func newRecordingManager(cdp *CdpConnection, extHub *extbridge.Hub) *recordingManager {
	return &recordingManager{
		cdp:       cdp,
		extHub:    extHub,
		active:    map[string]*activeRecording{},
		completed: map[string]RecordingInfo{},
	}
}

func (m *recordingManager) Start(opts recorder.CaptureOptions) (RecordingInfo, error) {
	if opts.ID == "" {
		opts.ID = recorder.NewID()
	}
	if opts.Mode == "" {
		opts.Mode = "cdp"
	}
	if opts.FPS <= 0 {
		opts.FPS = 10
	}
	if opts.Out == "" {
		opts.Out = filepath.Join(config.HomeDir(), "recordings", opts.ID+".borzrec")
	}
	if !strings.HasSuffix(opts.Out, ".borzrec") {
		opts.Out += ".borzrec"
	}
	if err := ensureParent(opts.Out); err != nil {
		return RecordingInfo{}, err
	}
	if opts.Mode != "cdp" && opts.Mode != "client" {
		return RecordingInfo{}, fmt.Errorf("unknown recording mode: %s", opts.Mode)
	}
	if opts.Mode == "client" && m.extHub.Connected() == 0 {
		return RecordingInfo{}, fmt.Errorf("client recording requires a connected borz extension")
	}

	var target *CdpTargetInfo
	if opts.Mode == "cdp" {
		if opts.URL != "" && opts.Tab == "" {
			resp := DispatchRequest(m.cdp, &protocol.Request{ID: "record-open", Action: protocol.ActionOpen, URL: opts.URL, New: true})
			if !resp.Success {
				return RecordingInfo{}, fmt.Errorf("open recording URL: %s", resp.Error)
			}
			if resp.Data != nil && resp.Data.TabID != nil {
				opts.Tab = fmt.Sprintf("%v", resp.Data.TabID)
			}
		}
		t, err := m.cdp.EnsurePageTarget(opts.Tab)
		if err != nil {
			return RecordingInfo{}, err
		}
		target = t
		if opts.URL == "" {
			opts.URL = target.URL
		}
	}

	w, err := recorder.Create(opts.Out, opts)
	if err != nil {
		return RecordingInfo{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	ar := &activeRecording{
		info: RecordingInfo{
			ID: opts.ID, Path: opts.Out, Mode: opts.Mode, URL: opts.URL, Tab: opts.Tab,
			Status: "recording", StartedAt: time.Now().UTC(), Options: opts,
		},
		writer: w, cancel: cancel, done: make(chan struct{}),
	}
	if target != nil {
		ar.targetID = target.ID
	}

	m.mu.Lock()
	if _, exists := m.active[opts.ID]; exists {
		m.mu.Unlock()
		_ = w.Close()
		cancel()
		return RecordingInfo{}, fmt.Errorf("recording id already exists: %s", opts.ID)
	}
	m.active[opts.ID] = ar
	m.mu.Unlock()

	if opts.Mode == "client" {
		ar.extCursor = m.extHub.LatestSeq()
		go m.runClient(ctx, ar)
	} else {
		go m.runCDP(ctx, ar)
	}
	return ar.snapshotInfo(), nil
}

func (m *recordingManager) Stop(id string, recoverPartial bool) (RecordingInfo, error) {
	ar, err := m.pickActive(id)
	if err != nil {
		if recoverPartial && id != "" {
			return m.recoverBundle(id)
		}
		return RecordingInfo{}, err
	}
	ar.cancel()
	<-ar.done
	if ar.info.Mode == "client" && m.extHub.Connected() > 0 {
		_, _ = m.extHub.Request("recording.stop", nil, 3*time.Second)
	}
	if err := ar.writer.Finish(); err != nil {
		return RecordingInfo{}, err
	}
	now := time.Now().UTC()
	ar.mu.Lock()
	ar.info.Status = "stopped"
	ar.info.FinishedAt = &now
	ar.info.DurationNS = now.Sub(ar.info.StartedAt).Nanoseconds()
	if b, err := recorder.Verify(ar.info.Path); err == nil {
		ar.info.FrameCount = b.Manifest.FrameCount
		ar.info.EventCount = b.Manifest.EventCount
		ar.info.DurationNS = b.Manifest.DurationNS
		ar.info.Manifest = &b.Manifest
	}
	info := ar.info
	ar.mu.Unlock()

	m.mu.Lock()
	delete(m.active, info.ID)
	m.completed[info.ID] = info
	m.mu.Unlock()
	return info, nil
}

func (m *recordingManager) Pause(id string) (RecordingInfo, error) {
	ar, err := m.pickActive(id)
	if err != nil {
		return RecordingInfo{}, err
	}
	ar.mu.Lock()
	ar.paused = true
	ar.info.Status = "paused"
	info := ar.info
	ar.mu.Unlock()
	_ = ar.writer.AddEvent(recorder.Event{Timestamp: time.Since(ar.info.StartedAt).Nanoseconds(), Type: "pause"})
	return info, nil
}

func (m *recordingManager) Resume(id string) (RecordingInfo, error) {
	ar, err := m.pickActive(id)
	if err != nil {
		return RecordingInfo{}, err
	}
	ar.mu.Lock()
	ar.paused = false
	ar.info.Status = "recording"
	info := ar.info
	ar.mu.Unlock()
	_ = ar.writer.AddEvent(recorder.Event{Timestamp: time.Since(ar.info.StartedAt).Nanoseconds(), Type: "resume"})
	return info, nil
}

func (m *recordingManager) List() []RecordingInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordingInfo, 0, len(m.active)+len(m.completed))
	for _, ar := range m.active {
		out = append(out, ar.snapshotInfo())
	}
	for _, info := range m.completed {
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (m *recordingManager) Info(idOrPath string) (RecordingInfo, error) {
	m.mu.Lock()
	if ar, ok := m.active[idOrPath]; ok {
		m.mu.Unlock()
		return ar.snapshotInfo(), nil
	}
	if info, ok := m.completed[idOrPath]; ok {
		m.mu.Unlock()
		return info, nil
	}
	m.mu.Unlock()
	path := idOrPath
	if !strings.Contains(path, string(filepath.Separator)) && !strings.HasSuffix(path, ".borzrec") {
		path = filepath.Join(config.HomeDir(), "recordings", idOrPath+".borzrec")
	}
	b, err := recorder.Verify(path)
	if err != nil {
		return RecordingInfo{}, err
	}
	info := RecordingInfo{
		ID: b.Manifest.ID, Path: path, Mode: b.Manifest.CaptureMode, URL: b.Manifest.Options.URL,
		Tab: b.Manifest.Options.Tab, Status: "stopped", StartedAt: b.Manifest.CreatedAt,
		FinishedAt: b.Manifest.FinalizedAt, FrameCount: b.Manifest.FrameCount,
		EventCount: b.Manifest.EventCount, DurationNS: b.Manifest.DurationNS,
		Options: b.Manifest.Options, Manifest: &b.Manifest,
	}
	return info, nil
}

func (m *recordingManager) recoverBundle(idOrPath string) (RecordingInfo, error) {
	path := idOrPath
	if !strings.Contains(path, string(filepath.Separator)) && !strings.HasSuffix(path, ".borzrec") {
		path = filepath.Join(config.HomeDir(), "recordings", idOrPath+".borzrec")
	}
	b, err := recorder.Verify(path)
	if err != nil {
		return RecordingInfo{}, err
	}
	now := time.Now().UTC()
	b.Manifest.Partial = false
	b.Manifest.FinalizedAt = &now
	data, _ := json.MarshalIndent(b.Manifest, "", "  ")
	if err := osWriteFile(filepath.Join(path, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return RecordingInfo{}, err
	}
	return m.Info(path)
}

func (m *recordingManager) pickActive(id string) (*activeRecording, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != "" {
		if ar, ok := m.active[id]; ok {
			return ar, nil
		}
		return nil, fmt.Errorf("recording not active: %s", id)
	}
	if len(m.active) != 1 {
		return nil, fmt.Errorf("recording id is required when %d recordings are active", len(m.active))
	}
	for _, ar := range m.active {
		return ar, nil
	}
	return nil, fmt.Errorf("no active recording")
}

func (m *recordingManager) runCDP(ctx context.Context, ar *activeRecording) {
	defer close(ar.done)
	interval := time.Second / time.Duration(maxInt(ar.info.Options.FPS, 1))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	_ = m.captureCDP(ar)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ar.isPaused() {
				continue
			}
			if err := m.captureCDP(ar); err != nil {
				ar.setError(err)
			}
		}
	}
}

func (m *recordingManager) captureCDP(ar *activeRecording) error {
	events, vp, url, title, scrollX, scrollY := m.drainPageEvents(ar)
	ts := time.Since(ar.info.StartedAt).Nanoseconds()
	for _, ev := range events {
		if ev.Timestamp == 0 {
			ev.Timestamp = ts
		}
		_ = ar.writer.AddEvent(ev)
	}
	format := "jpeg"
	params := map[string]interface{}{"format": format, "quality": 80, "fromSurface": true}
	if ar.info.Options.Lossless {
		format = "png"
		params = map[string]interface{}{"format": "png", "fromSurface": true}
	}
	if len(ar.info.Options.MaskSelectors) > 0 || ar.info.Options.MaskByDefault {
		_, _ = m.cdp.Evaluate(ar.targetID, maskScript(ar.info.Options.MaskSelectors, ar.info.Options.MaskByDefault), false)
	}
	raw, err := m.cdp.SessionCommandWithTimeout(ar.targetID, "Page.captureScreenshot", params, 10*time.Second)
	if err != nil {
		return err
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &shot); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return err
	}
	ext := "jpg"
	if format == "png" {
		ext = "png"
	}
	rec, err := ar.writer.AddFrame(ts, data, ext, vp, url, title, scrollX, scrollY)
	ar.updateCounts(rec.Seq, 0, ts)
	return err
}

func (m *recordingManager) drainPageEvents(ar *activeRecording) ([]recorder.Event, recorder.Viewport, string, string, float64, float64) {
	vp := recorder.Viewport{DPR: 1}
	script := eventTapScript(ar.info.Options.MaskSelectors)
	raw, err := m.cdp.EvaluateWithTimeout(ar.targetID, script, true, 2*time.Second)
	if err != nil {
		ar.setError(err)
		return nil, vp, ar.info.URL, "", 0, 0
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return nil, vp, ar.info.URL, "", 0, 0
	}
	var payload struct {
		Events  []pageEvent `json:"events"`
		Width   int         `json:"width"`
		Height  int         `json:"height"`
		DPR     float64     `json:"dpr"`
		URL     string      `json:"url"`
		Title   string      `json:"title"`
		ScrollX float64     `json:"scrollX"`
		ScrollY float64     `json:"scrollY"`
	}
	if json.Unmarshal([]byte(encoded), &payload) != nil {
		return nil, vp, ar.info.URL, "", 0, 0
	}
	vp = recorder.Viewport{Width: payload.Width, Height: payload.Height, DPR: payload.DPR}
	if vp.DPR == 0 {
		vp.DPR = 1
	}
	events := make([]recorder.Event, 0, len(payload.Events))
	for _, pe := range payload.Events {
		events = append(events, pe.toRecorder(ar.info.StartedAt, payload.URL))
	}
	if payload.URL != "" {
		ar.mu.Lock()
		ar.info.URL = payload.URL
		ar.mu.Unlock()
	}
	return events, vp, payload.URL, payload.Title, payload.ScrollX, payload.ScrollY
}

func (m *recordingManager) runClient(ctx context.Context, ar *activeRecording) {
	defer close(ar.done)
	_, _ = m.extHub.Request("recording.start", map[string]any{"maskSelectors": ar.info.Options.MaskSelectors}, 10*time.Second)
	interval := time.Second / time.Duration(maxInt(ar.info.Options.FPS, 1))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	_ = m.captureClient(ar)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ar.isPaused() {
				continue
			}
			if err := m.captureClient(ar); err != nil {
				ar.setError(err)
			}
		}
	}
}

func (m *recordingManager) captureClient(ar *activeRecording) error {
	for _, ev := range m.extHub.Events(ar.extCursor) {
		if ev.Seq > ar.extCursor {
			ar.extCursor = ev.Seq
		}
		if ev.Name == "recording.input" {
			var pe pageEvent
			if json.Unmarshal(ev.Data, &pe) == nil {
				_ = ar.writer.AddEvent(pe.toRecorder(ar.info.StartedAt, ""))
				ar.updateCounts(0, 1, time.Since(ar.info.StartedAt).Nanoseconds())
			}
		}
	}
	raw, err := m.extHub.Request("recording.captureVisible", nil, 10*time.Second)
	if err != nil {
		return err
	}
	var payload struct {
		DataURL string  `json:"dataUrl"`
		Width   int     `json:"width"`
		Height  int     `json:"height"`
		DPR     float64 `json:"dpr"`
		URL     string  `json:"url"`
		Title   string  `json:"title"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	data, ext, err := recorder.DecodeDataURL(payload.DataURL)
	if err != nil {
		return err
	}
	ts := time.Since(ar.info.StartedAt).Nanoseconds()
	rec, err := ar.writer.AddFrame(ts, data, ext, recorder.Viewport{Width: payload.Width, Height: payload.Height, DPR: payload.DPR}, payload.URL, payload.Title, 0, 0)
	ar.updateCounts(rec.Seq, 0, ts)
	return err
}

type pageEvent struct {
	Type      string          `json:"type"`
	Timestamp float64         `json:"timestamp"`
	X         *float64        `json:"x,omitempty"`
	Y         *float64        `json:"y,omitempty"`
	Button    string          `json:"button,omitempty"`
	Key       string          `json:"key,omitempty"`
	Text      string          `json:"text,omitempty"`
	Redacted  bool            `json:"redacted,omitempty"`
	Selector  string          `json:"selector,omitempty"`
	Cursor    string          `json:"cursor,omitempty"`
	FocusRect *recorder.Rect  `json:"focusRect,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (p pageEvent) toRecorder(start time.Time, url string) recorder.Event {
	ts := time.Since(start).Nanoseconds()
	if p.Timestamp > 0 {
		eventTime := time.UnixMilli(int64(p.Timestamp)).UTC()
		if eventTime.After(start) {
			ts = eventTime.Sub(start).Nanoseconds()
		}
	}
	return recorder.Event{
		Timestamp: ts, Type: p.Type, X: p.X, Y: p.Y, Button: p.Button, Key: p.Key,
		Text: p.Text, Redacted: p.Redacted, URL: url, Selector: p.Selector,
		Cursor: p.Cursor, FocusRect: p.FocusRect, Data: p.Data,
	}
}

func (ar *activeRecording) snapshotInfo() RecordingInfo {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	info := ar.info
	info.Error = ar.lastErr
	return info
}

func (ar *activeRecording) isPaused() bool {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	return ar.paused
}

func (ar *activeRecording) setError(err error) {
	if err == nil {
		return
	}
	ar.mu.Lock()
	ar.lastErr = err.Error()
	ar.info.Error = ar.lastErr
	ar.mu.Unlock()
}

func (ar *activeRecording) updateCounts(frameSeq int, eventDelta int, duration int64) {
	ar.mu.Lock()
	defer ar.mu.Unlock()
	if frameSeq > ar.info.FrameCount {
		ar.info.FrameCount = frameSeq
	}
	ar.info.EventCount += eventDelta
	if duration > ar.info.DurationNS {
		ar.info.DurationNS = duration
	}
}

func eventTapScript(maskSelectors []string) string {
	selectors, _ := json.Marshal(maskSelectors)
	return fmt.Sprintf(`(() => {
  const selectors = %s;
  const sensitiveSelector = 'input[type=password],[autocomplete*=one-time-code],[autocomplete*=cc-],[data-borz-mask]' + (selectors.length ? ',' + selectors.join(',') : '');
  if (!globalThis.__borzRecorderInstalled) {
    globalThis.__borzRecorderInstalled = true;
    globalThis.__borzRecorderEvents = [];
    const push = (ev) => {
      let target = ev.target;
      let redacted = false;
      let selector = '';
      let rect = null;
      let cursor = '';
      try {
        if (target && target.matches && target.matches(sensitiveSelector)) redacted = true;
        if (target && target.tagName) selector = target.tagName.toLowerCase() + (target.id ? '#' + target.id : '');
        if (target && target.getBoundingClientRect) {
          const r = target.getBoundingClientRect();
          rect = {x:r.x, y:r.y, w:r.width, h:r.height};
        }
        cursor = target ? getComputedStyle(target).cursor : '';
      } catch (_) {}
      const item = {type: ev.type, timestamp: Date.now(), selector, cursor, redacted};
      if ('clientX' in ev) { item.x = ev.clientX; item.y = ev.clientY; }
      if ('button' in ev) item.button = String(ev.button);
      if (ev.type.startsWith('key')) {
        item.key = redacted ? '<redacted>' : ev.key;
        item.text = redacted ? '<redacted>' : (ev.key && ev.key.length === 1 ? ev.key : '');
      }
      if (ev.type === 'focus') item.focusRect = rect;
      globalThis.__borzRecorderEvents.push(item);
    };
    ['pointermove','pointerdown','pointerup','click','mousedown','mouseup','keydown','keyup','focus','blur','scroll','wheel'].forEach(t => addEventListener(t, push, true));
  }
  const events = (globalThis.__borzRecorderEvents || []).splice(0);
  return JSON.stringify({events, width: innerWidth, height: innerHeight, dpr: devicePixelRatio || 1, url: location.href, title: document.title, scrollX, scrollY});
})()`, string(selectors))
}

func maskScript(maskSelectors []string, maskByDefault bool) string {
	selectors := append([]string{"[data-borz-mask]"}, maskSelectors...)
	if maskByDefault {
		selectors = append(selectors, "input", "textarea", "[contenteditable=true]")
	}
	data, _ := json.Marshal(selectors)
	return fmt.Sprintf(`(() => {
  const id = 'borz-recorder-mask-style';
  const selectors = %s.filter(Boolean).join(',');
  if (!selectors) return;
  let s = document.getElementById(id);
  if (!s) { s = document.createElement('style'); s.id = id; document.documentElement.appendChild(s); }
  s.textContent = selectors + '{background:#000!important;color:transparent!important;text-shadow:none!important;caret-color:transparent!important;}';
})()`, string(data))
}

func ensureParent(path string) error {
	return osMkdirAll(filepath.Dir(path), 0o755)
}

var osMkdirAll = func(path string, perm uint32) error { return os.MkdirAll(path, os.FileMode(perm)) }
var osWriteFile = func(path string, data []byte, perm uint32) error { return os.WriteFile(path, data, os.FileMode(perm)) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
