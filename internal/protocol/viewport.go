package protocol

import "strings"

// ViewportPreset returns named viewport profiles useful for responsive testing.
func ViewportPreset(name string) (ViewportOptions, bool) {
	touchOn := true
	touchOff := false
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mobile", "iphone":
		return ViewportOptions{Width: 390, Height: 844, DPR: 3, Mobile: true, Touch: &touchOn}, true
	case "tablet", "ipad":
		return ViewportOptions{Width: 768, Height: 1024, DPR: 2, Mobile: true, Touch: &touchOn}, true
	case "desktop":
		return ViewportOptions{Width: 1365, Height: 900, DPR: 1, Mobile: false, Touch: &touchOff}, true
	default:
		return ViewportOptions{}, false
	}
}
