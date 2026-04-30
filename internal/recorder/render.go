package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RenderOptions configures deterministic rendering.
type RenderOptions struct {
	Preset      string
	Out         string
	FPS         int
	Width       int
	Height      int
	Format      string
	Annotations []string
	Trim        string
	Speed       string
	Watermark   string
	Smooth      bool
	Chapters    string
	FFmpegPath  string
}

// Preset contains renderer defaults.
type Preset struct {
	Name        string
	Width       int
	Height      int
	FPS         int
	Format      string
	Annotations []string
	Bitrate     string
}

// Presets are the named outputs exposed by the CLI.
var Presets = map[string]Preset{
	"share":   {Name: "share", Width: 1280, Height: 720, FPS: 30, Format: "mp4", Annotations: []string{"cursor", "clicks", "keys", "focus"}, Bitrate: "4M"},
	"docs":    {Name: "docs", Width: 1920, Height: 1080, FPS: 30, Format: "mp4", Annotations: []string{"cursor", "clicks", "keys", "focus"}, Bitrate: "8M"},
	"loop":    {Name: "loop", Width: 800, Height: 0, FPS: 24, Format: "webp", Annotations: []string{"cursor", "clicks", "keys"}, Bitrate: "3M"},
	"archive": {Name: "archive", Width: 0, Height: 0, FPS: 60, Format: "mov", Annotations: []string{"cursor", "clicks", "keys", "focus"}, Bitrate: "20M"},
	"gif":     {Name: "gif", Width: 1024, Height: 0, FPS: 15, Format: "gif", Annotations: []string{"cursor", "clicks", "keys"}, Bitrate: ""},
}

// Render renders a bundle to video/image via ffmpeg.
func Render(bundleDir string, opts RenderOptions) error {
	b, err := Verify(bundleDir)
	if err != nil {
		return err
	}
	if len(b.Frames) == 0 {
		return fmt.Errorf("bundle has no frames")
	}
	opts = fillRenderDefaults(b, opts)
	ffmpeg := opts.FFmpegPath
	if ffmpeg == "" {
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required for rendering; install it or pass --ffmpeg: %w", err)
		}
	}
	if opts.Out == "" {
		opts.Out = "out." + opts.Format
	}
	temp, err := os.MkdirTemp("", "borz-render-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := renderFrames(b, temp, opts); err != nil {
		return err
	}
	return runFFmpeg(ffmpeg, temp, opts)
}

func fillRenderDefaults(b *Bundle, opts RenderOptions) RenderOptions {
	preset := Presets["share"]
	if opts.Preset != "" {
		if p, ok := Presets[opts.Preset]; ok {
			preset = p
		}
	}
	if opts.FPS <= 0 {
		opts.FPS = preset.FPS
	}
	if opts.Format == "" {
		opts.Format = preset.Format
	}
	if len(opts.Annotations) == 0 {
		opts.Annotations = append([]string{}, preset.Annotations...)
	}
	if opts.Width == 0 {
		opts.Width = preset.Width
	}
	if opts.Height == 0 {
		opts.Height = preset.Height
	}
	nativeW, nativeH := b.Frames[0].Width, b.Frames[0].Height
	if opts.Width == 0 && opts.Height == 0 {
		opts.Width, opts.Height = nativeW, nativeH
	} else if opts.Width > 0 && opts.Height == 0 && nativeW > 0 {
		opts.Height = int(math.Round(float64(nativeH) * float64(opts.Width) / float64(nativeW)))
	} else if opts.Height > 0 && opts.Width == 0 && nativeH > 0 {
		opts.Width = int(math.Round(float64(nativeW) * float64(opts.Height) / float64(nativeH)))
	}
	if opts.Width <= 0 {
		opts.Width = nativeW
	}
	if opts.Height <= 0 {
		opts.Height = nativeH
	}
	if opts.Format == "" && opts.Out != "" {
		opts.Format = strings.TrimPrefix(strings.ToLower(filepath.Ext(opts.Out)), ".")
	}
	return opts
}

func renderFrames(b *Bundle, temp string, opts RenderOptions) error {
	trimStart, trimEnd := parseTrim(opts.Trim, b.Manifest.DurationNS)
	events := eventsByTime(b.Events)
	frameCount := targetFrameCount(trimStart, trimEnd, opts.FPS)
	if frameCount <= 0 {
		frameCount = 1
	}
	for i := 0; i < frameCount; i++ {
		ts := trimStart + int64(float64(i)*float64(1e9)/float64(opts.FPS))
		src := frameAt(b.Frames, ts)
		img, err := loadFrame(b.Dir, src)
		if err != nil {
			return err
		}
		canvas := fitImage(img, opts.Width, opts.Height)
		if annotationEnabled(opts.Annotations, "focus") {
			drawFocus(canvas, events, ts, opts, src)
		}
		drawRedactions(canvas, b.Redaction.Masks, src.SceneID, ts, opts, src)
		drawSelectorRedactions(canvas, b.Redaction.Selectors, events, ts, opts, src)
		if annotationEnabled(opts.Annotations, "clicks") {
			drawClickPulses(canvas, events, ts, opts, src)
		}
		if annotationEnabled(opts.Annotations, "cursor") {
			drawCursor(canvas, events, ts, opts, src)
		}
		if annotationEnabled(opts.Annotations, "keys") {
			drawKeyCaption(canvas, events, ts)
		}
		out := filepath.Join(temp, fmt.Sprintf("frame-%06d.png", i+1))
		if err := writePNG(out, canvas); err != nil {
			return err
		}
	}
	return nil
}

func runFFmpeg(ffmpeg, temp string, opts RenderOptions) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-framerate", strconv.Itoa(opts.FPS), "-i", filepath.Join(temp, "frame-%06d.png")}
	switch opts.Format {
	case "gif":
		args = append(args, "-vf", "fps="+strconv.Itoa(opts.FPS)+",split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse=dither=bayer")
	case "webp":
		args = append(args, "-loop", "0", "-c:v", "libwebp", "-lossless", "0", "-quality", "85")
	case "apng":
		args = append(args, "-plays", "0", "-f", "apng")
	case "mov":
		args = append(args, "-c:v", "prores_ks", "-profile:v", "1", "-pix_fmt", "yuv422p10le")
	case "webm":
		args = append(args, "-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-b:v", "3M")
	default:
		args = append(args, "-c:v", "libx264", "-profile:v", "high", "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-b:v", "4M")
	}
	args = append(args, opts.Out)
	cmd := exec.Command(ffmpeg, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg render failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func loadFrame(dir string, fr FrameRecord) (image.Image, error) {
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(fr.Path)))
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func fitImage(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{18, 18, 18, 255}), image.Point{}, draw.Src)
	sb := src.Bounds()
	scale := math.Min(float64(w)/float64(sb.Dx()), float64(h)/float64(sb.Dy()))
	if scale <= 0 {
		scale = 1
	}
	tw, th := int(float64(sb.Dx())*scale), int(float64(sb.Dy())*scale)
	ox, oy := (w-tw)/2, (h-th)/2
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			sx := sb.Min.X + int(float64(x)/scale)
			sy := sb.Min.Y + int(float64(y)/scale)
			dst.Set(ox+x, oy+y, src.At(sx, sy))
		}
	}
	return dst
}

func frameAt(frames []FrameRecord, ts int64) FrameRecord {
	idx := sort.Search(len(frames), func(i int) bool { return frames[i].Timestamp > ts }) - 1
	if idx < 0 {
		idx = 0
	}
	return frames[idx]
}

func eventsByTime(events []Event) []Event {
	out := append([]Event{}, events...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp < out[j].Timestamp })
	return out
}

func targetFrameCount(start, end int64, fps int) int {
	if fps <= 0 || end <= start {
		return 1
	}
	return int(math.Ceil(float64(end-start)/1e9*float64(fps))) + 1
}

func parseTrim(trim string, duration int64) (int64, int64) {
	if strings.TrimSpace(trim) == "" {
		return 0, duration
	}
	a, b, ok := strings.Cut(trim, "-")
	if !ok {
		return 0, duration
	}
	start := parseTimecode(a)
	end := parseTimecode(b)
	if end <= start {
		end = duration
	}
	if end > duration {
		end = duration
	}
	return start, end
}

func parseTimecode(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	var seconds float64
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		seconds = seconds*60 + v
	}
	return int64(seconds * 1e9)
}

func annotationEnabled(xs []string, want string) bool {
	for _, x := range xs {
		if strings.TrimSpace(x) == want {
			return true
		}
	}
	return false
}

func drawCursor(img *image.RGBA, events []Event, ts int64, opts RenderOptions, fr FrameRecord) {
	ev, ok := latestPointer(events, ts)
	if !ok || ev.X == nil || ev.Y == nil {
		return
	}
	x, y := scalePoint(*ev.X, *ev.Y, opts, fr)
	black := color.RGBA{0, 0, 0, 220}
	white := color.RGBA{255, 255, 255, 245}
	for dy := 0; dy < 22; dy++ {
		for dx := 0; dx <= dy/2+3; dx++ {
			setSafe(img, x+dx, y+dy, black)
		}
	}
	for dy := 1; dy < 18; dy++ {
		for dx := 1; dx <= dy/2+1; dx++ {
			setSafe(img, x+dx, y+dy, white)
		}
	}
}

func drawClickPulses(img *image.RGBA, events []Event, ts int64, opts RenderOptions, fr FrameRecord) {
	for _, ev := range events {
		if ev.Type != "pointerdown" && ev.Type != "mousedown" && ev.Type != "click" {
			continue
		}
		age := ts - ev.Timestamp
		if age < 0 || age > int64(240*timeMS) || ev.X == nil || ev.Y == nil {
			continue
		}
		x, y := scalePoint(*ev.X, *ev.Y, opts, fr)
		r := 8 + int(float64(age)/float64(240*timeMS)*28)
		alpha := uint8(220 - int(float64(age)/float64(240*timeMS)*180))
		drawCircle(img, x, y, r, color.RGBA{45, 164, 78, alpha})
	}
}

func drawFocus(img *image.RGBA, events []Event, ts int64, opts RenderOptions, fr FrameRecord) {
	var rect *Rect
	for _, ev := range events {
		if ev.Timestamp > ts {
			break
		}
		if ev.Type == "focus" && ev.FocusRect != nil {
			rect = ev.FocusRect
		}
	}
	if rect == nil {
		return
	}
	x1, y1 := scalePoint(rect.X, rect.Y, opts, fr)
	x2, y2 := scalePoint(rect.X+rect.W, rect.Y+rect.H, opts, fr)
	drawRectOutline(img, x1, y1, x2-x1, y2-y1, color.RGBA{9, 105, 218, 230})
}

func drawRedactions(img *image.RGBA, masks []RedactionMask, sceneID int, ts int64, opts RenderOptions, fr FrameRecord) {
	for _, m := range masks {
		if m.SceneID != 0 && m.SceneID != sceneID {
			continue
		}
		if m.StartNS != 0 && ts < m.StartNS {
			continue
		}
		if m.EndNS != 0 && ts > m.EndNS {
			continue
		}
		x1, y1 := scalePoint(m.X, m.Y, opts, fr)
		x2, y2 := scalePoint(m.X+m.W, m.Y+m.H, opts, fr)
		draw.Draw(img, image.Rect(x1, y1, x2, y2), image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
}

func drawSelectorRedactions(img *image.RGBA, selectors []string, events []Event, ts int64, opts RenderOptions, fr FrameRecord) {
	if len(selectors) == 0 {
		return
	}
	for _, ev := range events {
		if ev.Timestamp > ts {
			break
		}
		if ev.FocusRect == nil || !selectorMatchesAny(ev.Selector, selectors) {
			continue
		}
		x1, y1 := scalePoint(ev.FocusRect.X, ev.FocusRect.Y, opts, fr)
		x2, y2 := scalePoint(ev.FocusRect.X+ev.FocusRect.W, ev.FocusRect.Y+ev.FocusRect.H, opts, fr)
		draw.Draw(img, image.Rect(x1, y1, x2, y2), image.NewUniform(color.Black), image.Point{}, draw.Src)
	}
}

func selectorMatchesAny(captured string, selectors []string) bool {
	captured = strings.TrimSpace(captured)
	if captured == "" {
		return false
	}
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		if captured == selector || strings.HasSuffix(captured, selector) || strings.Contains(captured, selector) {
			return true
		}
	}
	return false
}

func drawKeyCaption(img *image.RGBA, events []Event, ts int64) {
	count := 0
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ts-ev.Timestamp > int64(1500*timeMS) {
			break
		}
		if ev.Type == "keydown" || ev.Type == "keyup" || ev.Type == "key" {
			count++
		}
	}
	if count == 0 {
		return
	}
	w, h := 160+count*8, 34
	x := (img.Bounds().Dx() - w) / 2
	y := img.Bounds().Dy() - h - 28
	drawRoundedBlock(img, x, y, w, h, color.RGBA{36, 41, 47, 220})
	for i := 0; i < min(count, 12); i++ {
		draw.Draw(img, image.Rect(x+16+i*12, y+12, x+24+i*12, y+22), image.NewUniform(color.RGBA{255, 255, 255, 230}), image.Point{}, draw.Src)
	}
}

const timeMS = int64(1e6)

func latestPointer(events []Event, ts int64) (Event, bool) {
	var out Event
	ok := false
	for _, ev := range events {
		if ev.Timestamp > ts {
			break
		}
		if ev.X != nil && ev.Y != nil && (strings.Contains(ev.Type, "pointer") || strings.Contains(ev.Type, "mouse") || ev.Type == "click") {
			out, ok = ev, true
		}
	}
	return out, ok
}

func scalePoint(x, y float64, opts RenderOptions, fr FrameRecord) (int, int) {
	sw, sh := fr.Width, fr.Height
	if sw <= 0 {
		sw = opts.Width
	}
	if sh <= 0 {
		sh = opts.Height
	}
	scale := math.Min(float64(opts.Width)/float64(sw), float64(opts.Height)/float64(sh))
	tw, th := float64(sw)*scale, float64(sh)*scale
	ox, oy := (float64(opts.Width)-tw)/2, (float64(opts.Height)-th)/2
	return int(ox + x*scale), int(oy + y*scale)
}

func setSafe(img *image.RGBA, x, y int, c color.Color) {
	if image.Pt(x, y).In(img.Bounds()) {
		img.Set(x, y, c)
	}
}

func drawCircle(img *image.RGBA, cx, cy, r int, c color.Color) {
	r2 := r * r
	inner := (r - 3) * (r - 3)
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if d <= r2 && d >= inner {
				setSafe(img, x, y, c)
			}
		}
	}
}

func drawRectOutline(img *image.RGBA, x, y, w, h int, c color.Color) {
	for i := 0; i < 3; i++ {
		for xx := x; xx <= x+w; xx++ {
			setSafe(img, xx, y+i, c)
			setSafe(img, xx, y+h-i, c)
		}
		for yy := y; yy <= y+h; yy++ {
			setSafe(img, x+i, yy, c)
			setSafe(img, x+w-i, yy, c)
		}
	}
}

func drawRoundedBlock(img *image.RGBA, x, y, w, h int, c color.Color) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), image.NewUniform(c), image.Point{}, draw.Over)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (opts RenderOptions) MarshalJSON() ([]byte, error) {
	type alias RenderOptions
	return json.Marshal(alias(opts))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
