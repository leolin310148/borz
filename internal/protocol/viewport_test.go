package protocol

import (
	"reflect"
	"testing"
)

func TestViewportPresetAcceptsCanonicalNamesAndAliases(t *testing.T) {
	for _, name := range []string{"mobile", "MOBILE", " iphone ", "tablet", "ipad", "desktop"} {
		t.Run(name, func(t *testing.T) {
			got, ok := ViewportPreset(name)
			if !ok {
				t.Fatalf("ViewportPreset(%q) returned !ok", name)
			}
			if got.Width <= 0 || got.Height <= 0 || got.DPR <= 0 {
				t.Fatalf("ViewportPreset(%q) returned invalid dimensions: %+v", name, got)
			}
			if got.Touch == nil {
				t.Fatalf("ViewportPreset(%q) returned nil Touch pointer", name)
			}
		})
	}
}

func TestViewportPresetReturnsIndependentTouchPointers(t *testing.T) {
	first, ok := ViewportPreset("mobile")
	if !ok {
		t.Fatal("mobile preset should exist")
	}
	second, ok := ViewportPreset("mobile")
	if !ok {
		t.Fatal("mobile preset should exist")
	}

	*first.Touch = false
	if *second.Touch != true {
		t.Fatal("mutating one preset result should not affect later results")
	}
}

func TestViewportPresetNamesOnlyReturnsCanonicalPublicNames(t *testing.T) {
	got := ViewportPresetNames()
	want := []string{"mobile", "tablet", "desktop"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ViewportPresetNames() = %#v, want %#v", got, want)
	}

	got[0] = "changed"
	gotAgain := ViewportPresetNames()
	if !reflect.DeepEqual(gotAgain, want) {
		t.Fatalf("ViewportPresetNames() exposed mutable backing storage: %#v", gotAgain)
	}
}

func TestParseViewportSize(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantWidth  int
		wantHeight int
		wantOK     bool
	}{
		{name: "lowercase separator", raw: "800x600", wantWidth: 800, wantHeight: 600, wantOK: true},
		{name: "uppercase separator", raw: "1024X768", wantWidth: 1024, wantHeight: 768, wantOK: true},
		{name: "multiply separator", raw: "390×844", wantWidth: 390, wantHeight: 844, wantOK: true},
		{name: "trims parts", raw: " 1280 x 720 ", wantWidth: 1280, wantHeight: 720, wantOK: true},
		{name: "missing separator", raw: "800", wantOK: false},
		{name: "multiple separators", raw: "800x600x2", wantOK: false},
		{name: "zero width", raw: "0x600", wantOK: false},
		{name: "negative height", raw: "800x-1", wantOK: false},
		{name: "non numeric", raw: "wide x tall", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWidth, gotHeight, gotOK := ParseViewportSize(tt.raw)
			if gotOK != tt.wantOK {
				t.Fatalf("ParseViewportSize(%q) ok = %v, want %v", tt.raw, gotOK, tt.wantOK)
			}
			if gotWidth != tt.wantWidth || gotHeight != tt.wantHeight {
				t.Fatalf("ParseViewportSize(%q) = %dx%d, want %dx%d", tt.raw, gotWidth, gotHeight, tt.wantWidth, tt.wantHeight)
			}
		})
	}
}
