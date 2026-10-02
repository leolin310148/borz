package daemon

import (
	"strings"
	"testing"
)

func TestTitleLabelPrefix(t *testing.T) {
	cases := map[string]string{"": "", "  ": "", "teams": "[teams] ", "camera_ops": "[camera_ops] ", "default": "[default] "}
	for in, want := range cases {
		if got := TitleLabelPrefix(in); got != want {
			t.Errorf("TitleLabelPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripTitleLabel(t *testing.T) {
	c := &CdpConnection{}
	if got := c.StripTitleLabel("[teams] Chat"); got != "[teams] Chat" {
		t.Fatalf("unlabeled connection stripped title: %q", got)
	}
	c.SetTitleLabel("[teams] ")
	for in, want := range map[string]string{
		"[teams] Chat | Microsoft Teams": "Chat | Microsoft Teams",
		"Chat":                           "Chat",
		"[other] Chat":                   "[other] Chat",
		"":                               "",
	} {
		if got := c.StripTitleLabel(in); got != want {
			t.Errorf("StripTitleLabel(%q) = %q, want %q", in, got, want)
		}
	}
	var nilConn *CdpConnection
	if got := nilConn.StripTitleLabel("[teams] x"); got != "[teams] x" {
		t.Fatalf("nil connection = %q", got)
	}
}

func TestTitleLabelScriptEmbedsPrefixOnce(t *testing.T) {
	if strings.Count(titleLabelScript, "%s") != 1 {
		t.Fatal("titleLabelScript must contain exactly one placeholder")
	}
}
