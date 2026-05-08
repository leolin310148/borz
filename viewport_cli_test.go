package main

import "testing"

func TestParseViewportSizeRequiresSingleSeparator(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		width, height int
	}{
		{raw: "800x600", width: 800, height: 600},
		{raw: " 800 X 600 ", width: 800, height: 600},
		{raw: "390\u00d7844", width: 390, height: 844},
	} {
		width, height, ok := parseViewportSize(tc.raw)
		if !ok {
			t.Fatalf("parseViewportSize(%q) returned !ok", tc.raw)
		}
		if width != tc.width || height != tc.height {
			t.Fatalf("parseViewportSize(%q) = %dx%d, want %dx%d", tc.raw, width, height, tc.width, tc.height)
		}
	}

	for _, raw := range []string{"800xx600", "800x600x1", "800x", "x600", "800"} {
		if width, height, ok := parseViewportSize(raw); ok {
			t.Fatalf("parseViewportSize(%q) = %dx%d, true; want false", raw, width, height)
		}
	}
}

func TestBuildViewportOptionsTrimsNumericFlags(t *testing.T) {
	opts := buildViewportOptions("", []string{
		"--width", " 800 ",
		"--height= 600 ",
		"--dpr", " 2 ",
	}, true)
	if opts == nil {
		t.Fatal("viewport options should be built from numeric flags")
	}
	if opts.Width != 800 || opts.Height != 600 || opts.DPR != 2 {
		t.Fatalf("viewport options = %+v", opts)
	}
}

func TestBuildViewportOptionsRejectsNonFiniteDPR(t *testing.T) {
	for _, dpr := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(dpr, func(t *testing.T) {
			expectExit(t, 1, func() {
				_ = buildViewportOptions("800x600", []string{"--dpr", dpr}, true)
			})
		})
	}
}

func TestViewportOptionsRejectsEmptyValueFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rawArgs []string
	}{
		{name: "width inline", rawArgs: []string{"viewport", "--width="}},
		{name: "width separate", rawArgs: []string{"viewport", "--width"}},
		{name: "height inline", rawArgs: []string{"viewport", "--height="}},
		{name: "dpr inline", rawArgs: []string{"viewport", "800x600", "--dpr="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectExit(t, 1, func() {
				_ = viewportOptionsFromCommand(nil, tc.rawArgs)
			})
		})
	}
}

func TestViewportOptionsFromCommandAllowsStatusFlag(t *testing.T) {
	for _, spec := range []string{"status", "current"} {
		t.Run(spec, func(t *testing.T) {
			opts := viewportOptionsFromCommand(nil, []string{"viewport", "--viewport", spec})
			if opts != nil {
				t.Fatalf("viewportOptionsFromCommand(%q) = %+v, want nil status request", spec, opts)
			}
		})
	}
}

func TestViewportStatusRejectsSettingFlags(t *testing.T) {
	for _, tc := range []struct {
		cmdArgs []string
		rawArgs []string
	}{
		{rawArgs: []string{"viewport", "--viewport", "status", "--width", "800"}},
		{cmdArgs: []string{"status"}, rawArgs: []string{"viewport", "status", "--reset"}},
	} {
		expectExit(t, 1, func() {
			_ = viewportOptionsFromCommand(tc.cmdArgs, tc.rawArgs)
		})
	}
}

func TestViewportStatusRejectedOutsideViewportCommand(t *testing.T) {
	expectExit(t, 1, func() {
		_ = viewportOptionsFromFlags([]string{"open", "--viewport", "status"})
	})
}
