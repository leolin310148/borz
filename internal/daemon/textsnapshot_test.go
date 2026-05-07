package daemon

import (
	"strings"
	"testing"
)

func TestFormatTextSnapshot_AllParts(t *testing.T) {
	got := formatTextSnapshot("Hello", "https://example.com/", "Some\nbody text")
	want := "# Hello\nhttps://example.com/\n\nSome\nbody text"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatTextSnapshot_NoTitleOrURL(t *testing.T) {
	got := formatTextSnapshot("", "", "just text")
	if got != "just text" {
		t.Errorf("got %q, want %q", got, "just text")
	}
}

func TestFormatTextSnapshot_NoBody(t *testing.T) {
	got := formatTextSnapshot("Title only", "", "")
	if !strings.HasPrefix(got, "# Title only") {
		t.Errorf("expected title line; got %q", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Errorf("should not have empty body separator; got %q", got)
	}
}

func TestFormatTextSnapshot_TrimsBody(t *testing.T) {
	got := formatTextSnapshot("T", "", "\n\n  spaced  \n\n")
	if !strings.HasSuffix(got, "spaced") {
		t.Errorf("body trailing whitespace not trimmed; got %q", got)
	}
}

func TestFormatTextSnapshot_TrimsMetadata(t *testing.T) {
	got := formatTextSnapshot("  Title  ", "  https://example.com/  ", "body")
	want := "# Title\nhttps://example.com/\n\nbody"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestFormatTextSnapshot_IgnoresWhitespaceOnlyMetadata(t *testing.T) {
	got := formatTextSnapshot(" \t\n", " \n", "body")
	if got != "body" {
		t.Errorf("got %q, want %q", got, "body")
	}
}
