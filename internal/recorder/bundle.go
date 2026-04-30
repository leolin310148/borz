// Package recorder implements the on-disk borz recording bundle and renderer.
package recorder

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	SchemaVersion = "1.0"
	framesDir     = "frames"
)

// CaptureOptions configures a recording bundle and active capture loop.
type CaptureOptions struct {
	ID            string   `json:"id,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	URL           string   `json:"url,omitempty"`
	Tab           string   `json:"tab,omitempty"`
	Out           string   `json:"out,omitempty"`
	FPS           int      `json:"fps,omitempty"`
	Lossless      bool     `json:"lossless,omitempty"`
	Audio         []string `json:"audio,omitempty"`
	MaskSelectors []string `json:"mask_selectors,omitempty"`
	MaskByDefault bool     `json:"mask_by_default,omitempty"`
	MaxSizeBytes  int64    `json:"max_size_bytes,omitempty"`
	Viewport      string   `json:"viewport,omitempty"`
	DPR           float64  `json:"dpr,omitempty"`
}

// Manifest describes a .borzrec bundle.
type Manifest struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	CaptureMode   string            `json:"capture_mode"`
	CreatedAt     time.Time         `json:"created_at"`
	FinalizedAt   *time.Time        `json:"finalized_at,omitempty"`
	DurationNS    int64             `json:"duration_ns"`
	FrameCount    int               `json:"frame_count"`
	EventCount    int               `json:"event_count"`
	Viewport      Viewport          `json:"viewport"`
	Scenes        []Scene           `json:"scenes"`
	Options       CaptureOptions    `json:"options"`
	Files         map[string]string `json:"files"`
	FFmpeg        string            `json:"ffmpeg,omitempty"`
	Partial       bool              `json:"partial"`
}

// Viewport stores capture dimensions and scale.
type Viewport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	DPR    float64 `json:"dpr"`
}

// Scene is a contiguous segment with stable visual context.
type Scene struct {
	ID        int      `json:"id"`
	StartNS   int64    `json:"start_ns"`
	EndNS     int64    `json:"end_ns,omitempty"`
	URL       string   `json:"url,omitempty"`
	Title     string   `json:"title,omitempty"`
	Viewport  Viewport `json:"viewport"`
	FrameFrom int      `json:"frame_from"`
	FrameTo   int      `json:"frame_to,omitempty"`
}

// FrameRecord indexes one frame file.
type FrameRecord struct {
	Seq       int      `json:"seq"`
	Timestamp int64    `json:"ts_ns"`
	SceneID   int      `json:"scene_id"`
	Path      string   `json:"path"`
	Width     int      `json:"w"`
	Height    int      `json:"h"`
	DPR       float64  `json:"dpr"`
	URL       string   `json:"url,omitempty"`
	Title     string   `json:"title,omitempty"`
	SHA256    string   `json:"sha256"`
	ScrollX   float64  `json:"scroll_x,omitempty"`
	ScrollY   float64  `json:"scroll_y,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// Event records input, lifecycle, redaction, network, or renderer metadata.
type Event struct {
	Seq       int             `json:"seq"`
	Timestamp int64           `json:"ts_ns"`
	Type      string          `json:"type"`
	X         *float64        `json:"x,omitempty"`
	Y         *float64        `json:"y,omitempty"`
	Button    string          `json:"button,omitempty"`
	Key       string          `json:"key,omitempty"`
	Text      string          `json:"text,omitempty"`
	Redacted  bool            `json:"redacted,omitempty"`
	URL       string          `json:"url,omitempty"`
	Selector  string          `json:"selector,omitempty"`
	Cursor    string          `json:"cursor,omitempty"`
	FocusRect *Rect           `json:"focus_rect,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Rect is a CSS-pixel rectangle.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Metadata stores user-editable bundle metadata and renderer markers.
type Metadata struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Author      string            `json:"author,omitempty"`
	SourceURLs  []string          `json:"source_urls,omitempty"`
	Markers     []Marker          `json:"markers,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// Marker stores renderer-only edits such as chapters, trims, and speed ramps.
type Marker struct {
	Type    string  `json:"type"`
	StartNS int64   `json:"start_ns"`
	EndNS   int64   `json:"end_ns,omitempty"`
	Label   string  `json:"label,omitempty"`
	Speed   float64 `json:"speed,omitempty"`
}

// Redactions stores render-time masks.
type Redactions struct {
	Selectors []string        `json:"selectors,omitempty"`
	Masks     []RedactionMask `json:"masks,omitempty"`
}

// RedactionMask is a time-scoped rectangle.
type RedactionMask struct {
	SceneID int     `json:"scene_id,omitempty"`
	StartNS int64   `json:"start_ns,omitempty"`
	EndNS   int64   `json:"end_ns,omitempty"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	Label   string  `json:"label,omitempty"`
	Baked   bool    `json:"baked,omitempty"`
}

// Writer appends frames and events to a bundle.
type Writer struct {
	mu        sync.Mutex
	dir       string
	manifest  Manifest
	metadata  Metadata
	redact    Redactions
	frames    *os.File
	events    *os.File
	start     time.Time
	lastScene sceneKey
	closed    bool
}

type sceneKey struct {
	w, h  int
	dpr   float64
	url   string
	title string
}

// Create initializes a partial .borzrec bundle.
func Create(dir string, opts CaptureOptions) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("bundle path is required")
	}
	if opts.ID == "" {
		opts.ID = NewID()
	}
	if opts.Mode == "" {
		opts.Mode = "cdp"
	}
	if opts.FPS <= 0 {
		opts.FPS = 10
	}
	if err := os.MkdirAll(filepath.Join(dir, framesDir), 0o755); err != nil {
		return nil, err
	}
	for _, sub := range []string{"audio", "thumbnails", "signatures"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	w := &Writer{
		dir:   dir,
		start: now,
		manifest: Manifest{
			SchemaVersion: SchemaVersion,
			ID:            opts.ID,
			CaptureMode:   opts.Mode,
			CreatedAt:     now,
			Options:       opts,
			Files:         map[string]string{},
			Partial:       true,
		},
		metadata: Metadata{SourceURLs: compactStrings([]string{opts.URL})},
		redact:   Redactions{Selectors: append([]string{}, opts.MaskSelectors...)},
	}
	var err error
	w.frames, err = os.OpenFile(filepath.Join(dir, "frames.cbor"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	w.events, err = os.OpenFile(filepath.Join(dir, "events.cbor"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		w.frames.Close()
		return nil, err
	}
	if err := w.writeJSONFiles(); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// NewID returns a sortable recording id.
func NewID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return "rec-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(sum[:])[:8]
}

// AddFrame writes image bytes and appends a frame index record.
func (w *Writer) AddFrame(tsNS int64, data []byte, ext string, vp Viewport, url, title string, scrollX, scrollY float64) (FrameRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return FrameRecord{}, errors.New("recording bundle is closed")
	}
	if len(data) == 0 {
		return FrameRecord{}, errors.New("frame data is empty")
	}
	if ext == "" {
		ext = "png"
	}
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if ext != "png" && ext != "jpg" && ext != "jpeg" {
		return FrameRecord{}, fmt.Errorf("unsupported frame extension: %s", ext)
	}
	if ext == "jpeg" {
		ext = "jpg"
	}
	w.manifest.FrameCount++
	seq := w.manifest.FrameCount
	name := fmt.Sprintf("%06d.%s", seq, ext)
	rel := filepath.Join(framesDir, name)
	if vp.Width == 0 || vp.Height == 0 {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			vp.Width, vp.Height = cfg.Width, cfg.Height
		}
	}
	if vp.DPR == 0 {
		vp.DPR = 1
	}
	if err := os.WriteFile(filepath.Join(w.dir, rel), data, 0o644); err != nil {
		return FrameRecord{}, err
	}
	sceneID := w.sceneFor(tsNS, seq, vp, url, title)
	sum := sha256.Sum256(data)
	rec := FrameRecord{
		Seq: seq, Timestamp: tsNS, SceneID: sceneID, Path: filepath.ToSlash(rel),
		Width: vp.Width, Height: vp.Height, DPR: vp.DPR, URL: url, Title: title,
		SHA256: hex.EncodeToString(sum[:]), ScrollX: scrollX, ScrollY: scrollY,
	}
	if err := appendJSONLine(w.frames, rec); err != nil {
		return FrameRecord{}, err
	}
	w.manifest.DurationNS = max64(w.manifest.DurationNS, tsNS)
	if seq == 1 {
		w.manifest.Viewport = vp
	}
	return rec, nil
}

// AddEvent appends an event after applying built-in privacy redaction.
func (w *Writer) AddEvent(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("recording bundle is closed")
	}
	w.manifest.EventCount++
	ev.Seq = w.manifest.EventCount
	ev = RedactEvent(ev)
	if err := appendJSONLine(w.events, ev); err != nil {
		return err
	}
	w.manifest.DurationNS = max64(w.manifest.DurationNS, ev.Timestamp)
	return nil
}

// Finish finalizes manifest and closes writers.
func (w *Writer) Finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finishLocked(true)
}

// Close closes a writer without marking the bundle finalized.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finishLocked(false)
}

func (w *Writer) finishLocked(final bool) error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.manifest.Scenes) > 0 {
		last := &w.manifest.Scenes[len(w.manifest.Scenes)-1]
		last.EndNS = w.manifest.DurationNS
		last.FrameTo = w.manifest.FrameCount
	}
	if final {
		now := time.Now().UTC()
		w.manifest.FinalizedAt = &now
		w.manifest.Partial = false
	}
	if w.frames != nil {
		w.frames.Close()
	}
	if w.events != nil {
		w.events.Close()
	}
	return w.writeJSONFiles()
}

func (w *Writer) sceneFor(tsNS int64, seq int, vp Viewport, url, title string) int {
	key := sceneKey{w: vp.Width, h: vp.Height, dpr: vp.DPR, url: url, title: title}
	if len(w.manifest.Scenes) == 0 || key != w.lastScene {
		if len(w.manifest.Scenes) > 0 {
			prev := &w.manifest.Scenes[len(w.manifest.Scenes)-1]
			prev.EndNS = tsNS
			prev.FrameTo = seq - 1
		}
		id := len(w.manifest.Scenes) + 1
		w.manifest.Scenes = append(w.manifest.Scenes, Scene{
			ID: id, StartNS: tsNS, URL: url, Title: title, Viewport: vp, FrameFrom: seq,
		})
		w.lastScene = key
		return id
	}
	return w.manifest.Scenes[len(w.manifest.Scenes)-1].ID
}

func (w *Writer) writeJSONFiles() error {
	if err := writePretty(filepath.Join(w.dir, "manifest.json"), w.manifest); err != nil {
		return err
	}
	if err := writePretty(filepath.Join(w.dir, "metadata.json"), w.metadata); err != nil {
		return err
	}
	if err := writePretty(filepath.Join(w.dir, "redactions.json"), w.redact); err != nil {
		return err
	}
	return nil
}

// Bundle is a loaded recording.
type Bundle struct {
	Dir       string
	Manifest  Manifest
	Frames    []FrameRecord
	Events    []Event
	Metadata  Metadata
	Redaction Redactions
}

// Open reads a .borzrec bundle.
func Open(dir string) (*Bundle, error) {
	var b Bundle
	b.Dir = dir
	if err := readJSON(filepath.Join(dir, "manifest.json"), &b.Manifest); err != nil {
		return nil, err
	}
	if err := checkSchema(b.Manifest.SchemaVersion); err != nil {
		return nil, err
	}
	if err := readJSONLines(filepath.Join(dir, "frames.cbor"), &b.Frames); err != nil {
		return nil, err
	}
	if err := readJSONLines(filepath.Join(dir, "events.cbor"), &b.Events); err != nil {
		return nil, err
	}
	_ = readJSON(filepath.Join(dir, "metadata.json"), &b.Metadata)
	_ = readJSON(filepath.Join(dir, "redactions.json"), &b.Redaction)
	return &b, nil
}

// Verify validates schema, index ordering, checksums, and required files.
func Verify(dir string) (*Bundle, error) {
	b, err := Open(dir)
	if err != nil {
		return nil, err
	}
	if b.Manifest.FrameCount != len(b.Frames) {
		return nil, fmt.Errorf("manifest frame_count=%d, index has %d", b.Manifest.FrameCount, len(b.Frames))
	}
	if b.Manifest.EventCount != len(b.Events) {
		return nil, fmt.Errorf("manifest event_count=%d, log has %d", b.Manifest.EventCount, len(b.Events))
	}
	lastTS := int64(-1)
	for i, fr := range b.Frames {
		if fr.Seq != i+1 {
			return nil, fmt.Errorf("frame seq out of order at index %d: %d", i, fr.Seq)
		}
		if fr.Timestamp < lastTS {
			return nil, fmt.Errorf("frame timestamps out of order at seq %d", fr.Seq)
		}
		lastTS = fr.Timestamp
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(fr.Path)))
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", fr.Seq, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); fr.SHA256 != "" && got != fr.SHA256 {
			return nil, fmt.Errorf("frame %d checksum mismatch", fr.Seq)
		}
	}
	lastTS = -1
	for i, ev := range b.Events {
		if ev.Seq != i+1 {
			return nil, fmt.Errorf("event seq out of order at index %d: %d", i, ev.Seq)
		}
		if ev.Timestamp < lastTS {
			return nil, fmt.Errorf("event timestamps out of order at seq %d", ev.Seq)
		}
		lastTS = ev.Timestamp
	}
	return b, nil
}

// ExportTrace writes a stable JSON trace for tooling consumers.
func ExportTrace(bundleDir, outPath string) error {
	b, err := Verify(bundleDir)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"manifest":   b.Manifest,
		"frames":     b.Frames,
		"events":     b.Events,
		"metadata":   b.Metadata,
		"redactions": b.Redaction,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if outPath == "" || outPath == "-" {
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(outPath, append(data, '\n'), 0o644)
}

// AddRedaction appends a render-time rectangle mask.
func AddRedaction(bundleDir string, mask RedactionMask) error {
	path := filepath.Join(bundleDir, "redactions.json")
	var r Redactions
	_ = readJSON(path, &r)
	r.Masks = append(r.Masks, mask)
	sort.SliceStable(r.Masks, func(i, j int) bool {
		if r.Masks[i].StartNS == r.Masks[j].StartNS {
			return r.Masks[i].SceneID < r.Masks[j].SceneID
		}
		return r.Masks[i].StartNS < r.Masks[j].StartNS
	})
	return writePretty(path, r)
}

// AddSelectorRedaction appends a selector mask that future renders/captures can honor.
func AddSelectorRedaction(bundleDir, selector string) error {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return errors.New("selector is required")
	}
	path := filepath.Join(bundleDir, "redactions.json")
	var r Redactions
	_ = readJSON(path, &r)
	for _, existing := range r.Selectors {
		if existing == selector {
			return nil
		}
	}
	r.Selectors = append(r.Selectors, selector)
	sort.Strings(r.Selectors)
	return writePretty(path, r)
}

// RedactEvent removes sensitive key and network fields before they hit disk.
func RedactEvent(ev Event) Event {
	lowerType := strings.ToLower(ev.Type)
	if ev.Redacted || strings.Contains(strings.ToLower(ev.Selector), "password") ||
		strings.Contains(strings.ToLower(ev.Selector), "one-time-code") ||
		strings.Contains(strings.ToLower(ev.Selector), "cc-") {
		ev.Redacted = true
		ev.Key = "<redacted>"
		ev.Text = "<redacted>"
	}
	if strings.Contains(lowerType, "network") && len(ev.Data) > 0 {
		var v any
		if json.Unmarshal(ev.Data, &v) == nil {
			ev.Data, _ = json.Marshal(redactNetwork(v))
		}
	}
	return ev
}

func redactNetwork(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			lk := strings.ToLower(k)
			if lk == "authorization" || lk == "cookie" || lk == "set-cookie" || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
				out[k] = "<redacted>"
				continue
			}
			out[k] = redactNetwork(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = redactNetwork(x[i])
		}
		return out
	default:
		return v
	}
}

// DecodeDataURL returns media bytes and extension from a browser data URL.
func DecodeDataURL(dataURL string) ([]byte, string, error) {
	prefix, encoded, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.Contains(prefix, ";base64") {
		return nil, "", errors.New("expected base64 data URL")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", err
	}
	ext := "png"
	if strings.Contains(prefix, "image/jpeg") {
		ext = "jpg"
	}
	return data, ext, nil
}

func appendJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func writePretty(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readJSONLines[T any](path string, out *[]T) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return sc.Err()
}

func checkSchema(version string) error {
	major, _, ok := strings.Cut(version, ".")
	if !ok {
		major = version
	}
	if major != "1" {
		return fmt.Errorf("unsupported borzrec schema version %q", version)
	}
	return nil
}

func compactStrings(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
