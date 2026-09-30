package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/leolin310148/borz/internal/protocol"
)

// navigationURLArg returns the URL for open/navigate from the positional
// argument or, with --stdin, from the first non-empty stdin line. --stdin keeps
// long or credential-bearing URLs out of argv and shell history.
func navigationURLArg(command string, positional, raw []string) string {
	if !hasFlag(raw, "--stdin") {
		if len(positional) == 0 {
			return ""
		}
		return positional[0]
	}
	if len(positional) > 0 {
		fatal(command + ": pass the URL either as an argument or with --stdin, not both")
	}
	data, err := io.ReadAll(io.LimitReader(stdinReader, 1<<20))
	if err != nil {
		fatal(command + ": read URL from stdin: " + err.Error())
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	fatal(command + " --stdin: no URL on stdin")
	return ""
}

// stdinReader is swapped by tests.
var stdinReader io.Reader = os.Stdin

// warnHiddenTab tells the operator that a tab switch left the page hidden:
// Chrome activated the tab, but its window is minimized, covered, or on
// another Space, so the page keeps throttling and visibility-gated work
// (uploads, composers) will not run.
func warnHiddenTab(resp *protocol.Response) {
	if resp == nil || resp.Data == nil || resp.Data.VisibilityState != "hidden" {
		return
	}
	fmt.Fprintln(os.Stderr, "Note: the tab is active in Chrome but the page is still hidden (window minimized, covered, or on another Space). Run 'borz tab front' to restore/raise it; if the window stays covered, bring it forward on the host or use 'borz page visibility visible'.")
}
