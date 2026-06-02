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
		{"--help as flag value", []string{"eval", "--json-arg", "--help", "flag"}, []string{"flag"}, false},
		{"-h as flag value", []string{"open", "--wait-for", "-h", "https://example.com"}, []string{"https://example.com"}, false},
		{"-h as timeout value", []string{"open", "--timeout", "-h", "https://example.com"}, []string{"https://example.com"}, false},
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

func TestFlagConsumesNextArg(t *testing.T) {
	for _, flag := range cliValueFlags {
		if !flagConsumesNextArg(flag) {
			t.Errorf("flagConsumesNextArg(%q) = false, want true", flag)
		}
	}
	for _, flag := range cliBoolFlags {
		if flagConsumesNextArg(flag) {
			t.Errorf("flagConsumesNextArg(%q) = true, want false", flag)
		}
	}
	if flagConsumesNextArg("literal") {
		t.Errorf("flagConsumesNextArg(%q) = true, want false", "literal")
	}
}

func TestCLIFlagTablesDoNotOverlap(t *testing.T) {
	for _, flag := range cliBoolFlags {
		if cliValueFlagSet[flag] {
			t.Errorf("flag %q is registered as both a value flag and a bool flag", flag)
		}
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
		"Usage: borz click <ref>",
		"Notes:",
		"Global flags",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("click --help missing %q; got:\n%s", want, out)
		}
	}
}

func TestActionCommandUsageMentionsWaitFor(t *testing.T) {
	for _, name := range []string{
		"back", "forward", "refresh", "click", "hover", "fill", "type",
		"check", "uncheck", "select", "press", "scroll", "eval",
	} {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() {
				if !printCommandHelp(name) {
					t.Fatalf("printCommandHelp(%q) returned false", name)
				}
			})
			for _, want := range []string{"[--wait-for <selector>]", "[--timeout <ms>]"} {
				if !strings.Contains(out, want) {
					t.Errorf("%s help missing %q; got:\n%s", name, want, out)
				}
			}
		})
	}
}

func TestPrintCommandHelpAlias(t *testing.T) {
	out := captureStdout(t, func() {
		printCommandHelp("--help")
	})
	if !strings.Contains(out, "Show help for borz") {
		t.Errorf("--help alias did not resolve to 'help' entry; got:\n%s", out)
	}
}

func TestHelpCommandHelpMentionsAllFlag(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("help") {
			t.Fatal("printCommandHelp should return true for help")
		}
	})
	for _, want := range []string{
		"--all",
		"Dump every registered command's help",
		"borz help --all | less",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help command help missing %q; got:\n%s", want, out)
		}
	}
}

func TestExtensionHelpMentionsAliases(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("extension") {
			t.Fatal("printCommandHelp should return true for extension")
		}
	})
	for _, want := range []string{
		"download|update|install|path|status|capabilities|call",
		"install               Alias for 'download'",
		"capabilities          Alias for 'status'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("extension help missing %q; got:\n%s", want, out)
		}
	}
}

func TestNetworkRequestsHelpMentionsTailFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("network.requests") {
			t.Fatal("printCommandHelp should return true for network.requests")
		}
	})
	for _, want := range []string{
		"--tail",
		"--interval <duration|ms>",
		"borz network requests --tail --filter /api/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("network.requests help missing %q; got:\n%s", want, out)
		}
	}
}

func TestClientSetupHelpMentionsSupportedFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("client.setup") {
			t.Fatal("printCommandHelp should return true for client.setup")
		}
	})
	for _, want := range []string{
		"--url <url>",
		"--token <token>",
		"--no-check",
		"borz client setup --url http://127.0.0.1:19824 --no-check",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("client.setup help missing %q; got:\n%s", want, out)
		}
	}
}

func TestTabNewHelpMentionsViewportFlags(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("tab.new") {
			t.Fatal("printCommandHelp should return true for tab.new")
		}
	})
	for _, want := range []string{
		"--viewport <preset|WxH>",
		"--dpr <n>",
		"--mobile",
		"--touch / --no-touch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tab.new help missing %q; got:\n%s", want, out)
		}
	}
}

func TestViewportHelpMentionsStatusMode(t *testing.T) {
	out := captureStdout(t, func() {
		if !printCommandHelp("viewport") {
			t.Fatal("printCommandHelp should return true for viewport")
		}
	})
	for _, want := range []string{
		"borz viewport [status|current|mobile|tablet|desktop|reset|<width>x<height>]",
		"borz viewport status",
		"Run without arguments, or with `status`/`current`, to inspect",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("viewport help missing %q; got:\n%s", want, out)
		}
	}
}

func TestPrintCommandHelpUnknownFallsBack(t *testing.T) {
	out := captureStdout(t, func() {
		if printCommandHelp("notarealcommand") {
			t.Error("printCommandHelp should return false for unknown command")
		}
	})
	if !strings.Contains(out, "borz - Your browser is the API") {
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
		"eval", "get", "screenshot", "viewport", "press", "clipboard-write", "term-text", "scroll", "wait",
		"snapshot", "tab", "frame", "dialog", "network", "console", "errors", "trace",
		"fetch", "mcp", "daemon", "server", "service", "client", "status", "doctor", "site", "update", "record", "history",
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
		"tab.list", "tab.new", "tab.select", "tab.close", "tab.events",
		// site (handleSite)
		"site.list", "site.search", "site.info", "site.update", "site.new", "site.lint", "site.trust", "site.run",
		// daemon (handleDaemon)
		"daemon.status", "daemon.shutdown", "daemon.stop",
		// server (handleServer)
		"server.status", "server.shutdown", "server.stop",
		// service (handleService)
		"service.install", "service.uninstall", "service.remove", "service.start", "service.stop", "service.status",
		// client (handleClient)
		"client.setup", "client.enable", "client.disable", "client.status",
		// trace
		"trace.start", "trace.stop", "trace.status",
		// record
		"record.start", "record.stop", "record.pause", "record.resume", "record.list", "record.info",
		"record.verify", "record.render", "record.redact", "record.export", "record.edit", "record.play",
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

func TestSuggestNamesNonPositiveLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		maxN int
	}{
		{"zero", 0},
		{"negative", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := suggestNames("opn", []string{"open"}, tc.maxN); len(got) != 0 {
				t.Fatalf("suggestNames with maxN=%d returned %v, want no suggestions", tc.maxN, got)
			}
		})
	}
}

func TestSuggestSubcommands(t *testing.T) {
	got := suggestSubcommands("extension", "statu", 3)
	if len(got) == 0 || got[0] != "status" {
		t.Fatalf("suggestSubcommands(extension, statu): want status first, got %v", got)
	}

	got = suggestSubcommands("cookies", "al", 3)
	if len(got) == 0 || got[0] != "all" {
		t.Fatalf("suggestSubcommands(cookies, al): want all first, got %v", got)
	}

	hint := unknownSubcommandHint("client", "enabel")
	for _, want := range []string{
		"Unknown client subcommand: enabel",
		"Did you mean: borz client enable?",
		"Available subcommands:",
		"Run 'borz help client' for usage.",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("unknownSubcommandHint missing %q; got:\n%s", want, hint)
		}
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
		"## record.pause",
		"## tab.events",
		"--unwrap",
		"--wait-for",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printAllHelp output missing %q", want)
		}
	}
}

func TestSiteRunHelpMentionsSupportedFlags(t *testing.T) {
	out := captureStdout(t, func() { printCommandHelp("site.run") })
	for _, want := range []string{
		"--timeout <ms>",
		"--unwrap",
		"--force",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("site.run help missing %q; got:\n%s", want, out)
		}
	}
}

func TestTopLevelHelpMentionsNewFlags(t *testing.T) {
	out := captureStdout(t, func() { printHelp() })
	for _, want := range []string{
		"--wait-for",
		"--unwrap",
		"--file",
		"--diff",
		"--role",
		"record start|stop|render",
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
