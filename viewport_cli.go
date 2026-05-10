package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

func handleViewport(cmdArgs []string, jsonOutput bool, globalTabID string, rawArgs []string) {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionViewport}
	setTab(req, globalTabID)
	req.Viewport = viewportOptionsFromCommand(cmdArgs, rawArgs)
	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data == nil || resp.Data.Viewport == nil {
			fmt.Println("Viewport updated")
			return
		}
		fmt.Println(formatViewportInfo(resp.Data.Viewport))
	})
}

func applyCLIViewport(req *protocol.Request, rawArgs []string) {
	if opts := viewportOptionsFromFlags(rawArgs); opts != nil {
		req.Viewport = opts
	}
}

func viewportOptionsFromCommand(cmdArgs []string, rawArgs []string) *protocol.ViewportOptions {
	spec := firstNonEmpty(getArgValue(rawArgs, "--preset"), getArgValue(rawArgs, "--viewport"))
	if len(cmdArgs) > 0 {
		spec = cmdArgs[0]
	}
	if spec == "" && !hasViewportFlags(rawArgs) {
		return nil
	}
	return buildViewportOptions(spec, rawArgs, true)
}

func viewportOptionsFromFlags(rawArgs []string) *protocol.ViewportOptions {
	spec := firstNonEmpty(getArgValue(rawArgs, "--viewport"), getArgValue(rawArgs, "--preset"))
	if spec == "" && !hasViewportFlags(rawArgs) {
		return nil
	}
	return buildViewportOptions(spec, rawArgs, false)
}

func hasViewportFlags(rawArgs []string) bool {
	return hasFlag(rawArgs, "--mobile") ||
		hasFlag(rawArgs, "--touch") ||
		hasFlag(rawArgs, "--no-touch") ||
		hasFlag(rawArgs, "--reset") ||
		hasArgValue(rawArgs, "--width") ||
		hasArgValue(rawArgs, "--height") ||
		hasArgValue(rawArgs, "--dpr") ||
		hasArgValue(rawArgs, "--preset") ||
		hasArgValue(rawArgs, "--viewport")
}

func hasViewportSettingFlags(rawArgs []string) bool {
	return hasFlag(rawArgs, "--mobile") ||
		hasFlag(rawArgs, "--touch") ||
		hasFlag(rawArgs, "--no-touch") ||
		hasFlag(rawArgs, "--reset") ||
		hasArgValue(rawArgs, "--width") ||
		hasArgValue(rawArgs, "--height") ||
		hasArgValue(rawArgs, "--dpr")
}

func hasArgValue(rawArgs []string, flag string) bool {
	_, ok := getArgValueOK(rawArgs, flag)
	return ok
}

func buildViewportOptions(spec string, rawArgs []string, allowStatus bool) *protocol.ViewportOptions {
	spec = strings.TrimSpace(strings.ToLower(spec))
	if spec == "status" || spec == "current" {
		if !allowStatus {
			fatal("viewport status is only valid with the viewport command")
		}
		if !hasViewportSettingFlags(rawArgs) {
			return nil
		}
		fatal("viewport status cannot be combined with viewport-setting flags")
	}
	if hasFlag(rawArgs, "--reset") || spec == "reset" || spec == "clear" {
		return &protocol.ViewportOptions{Reset: true}
	}
	if spec == "" && hasFlag(rawArgs, "--mobile") {
		spec = "mobile"
	}

	var opts protocol.ViewportOptions
	if spec != "" && spec != "status" && spec != "current" {
		if preset, ok := protocol.ViewportPreset(spec); ok {
			opts = preset
		} else if width, height, ok := parseViewportSize(spec); ok {
			opts.Width = width
			opts.Height = height
		} else {
			fatal("viewport must be mobile, tablet, desktop, reset, or <width>x<height>")
		}
	}

	if v, ok := getArgValueOK(rawArgs, "--width"); ok {
		opts.Width = parseViewportInt("--width", v)
	}
	if v, ok := getArgValueOK(rawArgs, "--height"); ok {
		opts.Height = parseViewportInt("--height", v)
	}
	if v, ok := getArgValueOK(rawArgs, "--dpr"); ok {
		dpr, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || math.IsNaN(dpr) || math.IsInf(dpr, 0) || dpr <= 0 {
			fatal("--dpr must be a positive number")
		}
		opts.DPR = dpr
	}
	if hasFlag(rawArgs, "--mobile") {
		opts.Mobile = true
		if !hasFlag(rawArgs, "--touch") && !hasFlag(rawArgs, "--no-touch") {
			touch := true
			opts.Touch = &touch
		}
	}
	if hasFlag(rawArgs, "--touch") {
		touch := true
		opts.Touch = &touch
	}
	if hasFlag(rawArgs, "--no-touch") {
		touch := false
		opts.Touch = &touch
	}
	if opts.DPR <= 0 {
		opts.DPR = 1
	}
	if opts.Width <= 0 || opts.Height <= 0 {
		fatal("viewport width and height must be positive; use a preset or <width>x<height>")
	}
	return &opts
}

func parseViewportSize(raw string) (int, int, bool) {
	return protocol.ParseViewportSize(raw)
}

func parseViewportInt(flag, raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		fatal(flag + " must be a positive integer")
	}
	return n
}

func formatViewportInfo(vp *protocol.ViewportInfo) string {
	mode := "desktop"
	if vp.Mobile {
		mode = "mobile"
	}
	prefix := "Viewport"
	if vp.Reset {
		prefix = "Viewport reset"
	}
	return fmt.Sprintf("%s: %dx%d @ %sx (%s, touch=%v)",
		prefix,
		vp.Width,
		vp.Height,
		strconv.FormatFloat(vp.DPR, 'f', -1, 64),
		mode,
		vp.Touch,
	)
}
