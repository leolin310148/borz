package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/leolin310148/borz/internal/protocol"
)

func applyViewport(cdp *CdpConnection, targetID string, opts *protocol.ViewportOptions) (*protocol.ViewportInfo, error) {
	if opts == nil {
		return readViewport(cdp, targetID)
	}
	if opts.Reset {
		if _, err := cdp.SessionCommand(targetID, "Emulation.clearDeviceMetricsOverride", nil); err != nil {
			return nil, err
		}
		if _, err := cdp.SessionCommand(targetID, "Emulation.setTouchEmulationEnabled", map[string]interface{}{"enabled": false}); err != nil {
			return nil, err
		}
		info, err := readViewport(cdp, targetID)
		if err != nil {
			return nil, err
		}
		info.Reset = true
		return info, nil
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		return nil, fmt.Errorf("viewport width and height must be positive")
	}
	dpr := opts.DPR
	if dpr <= 0 {
		dpr = 1
	}
	touch := opts.Mobile
	if opts.Touch != nil {
		touch = *opts.Touch
	}
	params := map[string]interface{}{
		"width":             opts.Width,
		"height":            opts.Height,
		"deviceScaleFactor": dpr,
		"mobile":            opts.Mobile,
	}
	if _, err := cdp.SessionCommand(targetID, "Emulation.setDeviceMetricsOverride", params); err != nil {
		return nil, err
	}
	if _, err := cdp.SessionCommand(targetID, "Emulation.setTouchEmulationEnabled", map[string]interface{}{"enabled": touch}); err != nil {
		return nil, err
	}
	return &protocol.ViewportInfo{
		Width: opts.Width, Height: opts.Height, DPR: dpr,
		Mobile: opts.Mobile, Touch: touch,
	}, nil
}

func readViewport(cdp *CdpConnection, targetID string) (*protocol.ViewportInfo, error) {
	raw, err := cdp.Evaluate(targetID, `(() => ({
		width: Math.round(window.innerWidth || 0),
		height: Math.round(window.innerHeight || 0),
		dpr: window.devicePixelRatio || 1,
		mobile: window.matchMedia ? window.matchMedia('(max-width: 767px)').matches : false,
		touch: !!(navigator.maxTouchPoints > 0 || ('ontouchstart' in window))
	}))()`, true)
	if err != nil {
		return nil, err
	}
	return decodeViewportInfo(raw)
}

func decodeViewportInfo(raw json.RawMessage) (*protocol.ViewportInfo, error) {
	var info protocol.ViewportInfo
	if err := json.Unmarshal(raw, &info); err == nil && rawJSONObject(raw) {
		return &info, nil
	}
	if first := rawJSONFirstByte(raw); first == 0 || first == 'n' {
		return nil, fmt.Errorf("decode viewport info: expected object or encoded object")
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode viewport info: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &info); err != nil {
		return nil, fmt.Errorf("decode encoded viewport info: %w", err)
	}
	return &info, nil
}

func rawJSONObject(raw json.RawMessage) bool {
	return rawJSONFirstByte(raw) == '{'
}

func rawJSONFirstByte(raw json.RawMessage) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return b
		}
	}
	return 0
}
