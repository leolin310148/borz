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
