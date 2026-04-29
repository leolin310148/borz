package main

import (
	"strings"
	"testing"
)

// captureStdout is defined in main_handlers_test.go.

func TestHelpRequested(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rawArgs []string
		cmdArgs []string
		want    bool
	}{
		{"no help", []string{"click", "5"}, []string{"5"}, false},
		{"--help flag", []string{"click", "--help"}, []string{"5"}, true},
		{"-h flag", []string{"click", "-h"}, []string{"5"}, true},
		{"help subcommand arg", []string{"click"}, []string{"help"}, true},
		{"--help as subcommand arg", []string{"click"}, []string{"--help"}, true},
		{"help token mid-args does not count",
			[]string{"eval", "window.helping"}, []string{"window.helping"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := helpRequested(tc.rawArgs, tc.cmdArgs); got != tc.want {
				t.Errorf("helpRequested(%v, %v) = %v, want %v",
					tc.rawArgs, tc.cmdArgs, got, tc.want)
			}
		})
	}
}

func TestPrintCommandHelpKnown(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("click") {
			t.Fatal("printCommandHelp should return true for known command")
		}
	})
	for _, want := range []string{
		"Click an element by ref.",
		"Usage: bb-browser click <ref>",
		"Notes:",
		"Global flags",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("click --help missing %q; got:\n%s", want, out)
		}
	}
}

func TestPrintCommandHelpAlias(t *testing.T) {
	out := captureStdout(t, func() {
		printCommandHelp("--help")
	})
	if !strings.Contains(out, "Show help for bb-browser") {
		t.Errorf("--help alias did not resolve to 'help' entry; got:\n%s", out)
	}
}

func TestPrintCommandHelpUnknownFallsBack(t *testing.T) {
	out := captureStdout(t, func() {
		if printCommandHelp("notarealcommand") {
			t.Error("printCommandHelp should return false for unknown command")
		}
	})
	if !strings.Contains(out, "bb-browser-go - Your browser is the API") {
		t.Errorf("unknown command should fall back to top-level help; got:\n%s", out)
	}
}

// TestCommandHelpCoversDispatch ensures every command case in main.go has a
// matching commandHelp entry, so we don't silently ship a command with no
// per-command help. If you add a new 'case "foo":' to main(), add a
// commandHelp["foo"] entry too.
func TestCommandHelpCoversDispatch(t *testing.T) {
	expected := []string{
		"open", "back", "forward", "refresh", "close",
		"click", "hover", "fill", "type", "check", "uncheck", "select",
		"eval", "get", "screenshot", "press", "scroll", "wait",
		"snapshot", "tab", "frame", "dialog", "network", "console", "errors", "trace",
		"fetch", "mcp", "daemon", "server", "client", "status", "site", "update", "history",
		"cookies", "bookmarks", "browser-history", "downloads", "window", "windows", "extension",
		"help", "version",
	}
	for _, name := range expected {
		if _, ok := commandHelp[name]; !ok {
			t.Errorf("commandHelp missing entry for command %q", name)
		}
	}
}

// TestCommandHelpCoversSubcommands ensures every subcommand surfaced by the
// dispatch handlers in main.go has its own drill-down help page. If you add a
// new subcommand (e.g. a new 'case "X":' inside handleTab/handleSite/...),
// add a commandHelp["<parent>.X"] entry too.
func TestCommandHelpCoversSubcommands(t *testing.T) {
	expected := []string{
		// tab (handleTab)
		"tab.list", "tab.new", "tab.select", "tab.close",
		// site (handleSite)
		"site.list", "site.search", "site.info", "site.update", "site.run",
		// daemon (handleDaemon)
		"daemon.status", "daemon.shutdown", "daemon.stop",
		// server (handleServer)
		"server.status", "server.shutdown", "server.stop",
		// client (handleClient)
		"client.setup", "client.enable", "client.disable", "client.status",
		// trace
		"trace.start", "trace.stop", "trace.status",
		// network (handleNetwork)
		"network.requests", "network.clear",
		// dialog
		"dialog.accept", "dialog.dismiss",
		// frame
		"frame.main",
		// extension-backed APIs
		"extension.download", "extension.update", "extension.install", "extension.path", "extension.status", "extension.capabilities", "extension.call",
		"bookmarks.tree", "bookmarks.search", "bookmarks.create", "bookmarks.update", "bookmarks.remove",
		"browser-history.search", "browser-history.delete-url",
		"downloads.list", "downloads.search", "downloads.start", "downloads.erase", "downloads.cancel", "downloads.pause", "downloads.resume", "downloads.show", "downloads.show-folder",
		"window.list", "window.new", "window.focus", "window.close",
		"windows.list", "windows.new", "windows.focus", "windows.close",
	}
	for _, name := range expected {
		if _, ok := commandHelp[name]; !ok {
			t.Errorf("commandHelp missing subcommand entry %q", name)
		}
	}
}

func TestResolveHelpKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		parent  string
		cmdArgs []string
		want    string
	}{
		{"no args returns parent", "tab", nil, "tab"},
		{"known sub resolves", "tab", []string{"new"}, "tab.new"},
		{"skip help token", "tab", []string{"--help", "new"}, "tab.new"},
		{"skip bare help word", "tab", []string{"help", "new"}, "tab.new"},
		{"unknown sub falls back", "tab", []string{"foo"}, "tab"},
		{"numeric falls back", "tab", []string{"5"}, "tab"},
		{"flag-looking arg is skipped", "tab", []string{"-h", "close"}, "tab.close"},
		{"second non-flag does not match", "tab", []string{"close", "new"}, "tab.close"},
		{"alias resolves", "daemon", []string{"stop"}, "daemon.stop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveHelpKey(tc.parent, tc.cmdArgs); got != tc.want {
				t.Errorf("resolveHelpKey(%q, %v) = %q, want %q",
					tc.parent, tc.cmdArgs, got, tc.want)
			}
		})
	}
}

func TestSuggestCommands(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    string
		wantHead string // first suggestion (the closest); "" means expect no suggestion
		wantAny  string // additional name we expect to appear somewhere in the output
	}{
		{"close typo to open", "opn", "open", ""},
		{"common typo for snapshot", "snapsho", "snapshot", ""},
		{"close typo to click", "clic", "click", ""},
		{"long unrelated returns nothing", "xyzzyplover", "", ""},
		{"case-insensitive", "OPEN", "open", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := suggestCommands(tc.input, 3)
			if tc.wantHead == "" {
				if len(got) != 0 {
					t.Fatalf("expected no suggestions, got %v", got)
				}
				return
			}
			if len(got) == 0 || got[0] != tc.wantHead {
				t.Fatalf("suggestCommands(%q): want first=%q, got %v", tc.input, tc.wantHead, got)
			}
		})
	}
}

func TestPrintAllHelp(t *testing.T) {
	out := captureStdout(t, func() { printAllHelp() })
	for _, want := range []string{
		"## open",
		"## eval",
		"## snapshot",
		"## tab",
		"## tab.new",
		"--unwrap",
		"--wait-for",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printAllHelp output missing %q", want)
		}
	}
}

func TestTopLevelHelpMentionsNewFlags(t *testing.T) {
	out := captureStdout(t, func() { printHelp() })
	for _, want := range []string{
		"--wait-for",
		"--unwrap",
		"--file",
		"help --all",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("top-level help missing %q", want)
		}
	}
}

func TestPrintCommandHelpAllCommands(t *testing.T) {
	// Every registered command renders a non-empty help block with a Usage line.
	for _, name := range commandNames() {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() { printCommandHelp(name) })
			if len(out) < 50 {
				t.Fatalf("help for %q suspiciously short: %q", name, out)
			}
			if !strings.Contains(out, "Usage:") {
				t.Errorf("help for %q missing Usage line; got:\n%s", name, out)
			}
			if !strings.Contains(out, "Global flags") {
				t.Errorf("help for %q missing Global flags footer", name)
			}
		})
	}
}
