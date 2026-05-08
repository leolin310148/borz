package main

import "testing"

func TestParseViewportSizeRequiresSingleSeparator(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		width, height int
	}{
		{raw: "800x600", width: 800, height: 600},
		{raw: " 800 X 600 ", width: 800, height: 600},
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
	})
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
				_ = buildViewportOptions("800x600", []string{"--dpr", dpr})
			})
		})
	}
}
