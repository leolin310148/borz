package protocol

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

var canonicalViewportPresetNames = []string{"mobile", "tablet", "desktop"}

var viewportPresets = map[string]ViewportOptions{
	"mobile":  viewportPreset(390, 844, 3, true, true),
	"iphone":  viewportPreset(390, 844, 3, true, true),
	"tablet":  viewportPreset(768, 1024, 2, true, true),
	"ipad":    viewportPreset(768, 1024, 2, true, true),
	"desktop": viewportPreset(1365, 900, 1, false, false),
}

// ViewportPreset returns named viewport profiles useful for responsive testing.
func ViewportPreset(name string) (ViewportOptions, bool) {
	preset, ok := viewportPresets[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return ViewportOptions{}, false
	}
	if preset.Touch != nil {
		touch := *preset.Touch
		preset.Touch = &touch
	}
	return preset, true
}

// ViewportPresetNames returns the canonical preset names for user-facing
// validation, schemas, and help. Aliases such as "iphone" and "ipad" remain
// accepted by ViewportPreset but are intentionally omitted here.
func ViewportPresetNames() []string {
	return append([]string(nil), canonicalViewportPresetNames...)
}

// ParseViewportSize parses a CLI-style viewport size, such as "800x600".
func ParseViewportSize(raw string) (int, int, bool) {
	raw = strings.TrimSpace(raw)
	sep, sepWidth := -1, 0
	for i, r := range raw {
		if !isViewportSizeSeparator(r) {
			continue
		}
		if sep >= 0 {
			return 0, 0, false
		}
		sep, sepWidth = i, utf8.RuneLen(r)
	}
	if sep < 0 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(raw[:sep]))
	if err != nil || width <= 0 {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(raw[sep+sepWidth:]))
	if err != nil || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func isViewportSizeSeparator(r rune) bool {
	return r == 'x' || r == 'X' || r == '\u00d7'
}

func viewportPreset(width, height int, dpr float64, mobile, touch bool) ViewportOptions {
	return ViewportOptions{Width: width, Height: height, DPR: dpr, Mobile: mobile, Touch: &touch}
}
