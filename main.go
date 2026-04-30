package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon"
	"github.com/leolin310148/borz/internal/jq"
	"github.com/leolin310148/borz/internal/jseval"
	mcpserver "github.com/leolin310148/borz/internal/mcp"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/selfupdate"
	"github.com/leolin310148/borz/internal/site"
	"github.com/leolin310148/borz/internal/winservice"
)

var version = "0.1.0"

var jqExpression string

type daemonRunner interface {
	Run() error
}

var newDaemonServer = func(opts daemon.ServerOptions) daemonRunner {
	return daemon.NewServer(opts)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		os.Exit(0)
	}

	// Parse global flags
	remoteRouting := hasFlag(args, "--remote")
	client.SetRemoteRouting(remoteRouting)
	globalTabID := getArgValue(args, "--tab")
	jqExpression = getArgValue(args, "--jq")
	jsonOutput := hasFlag(args, "--json") || jqExpression != ""
	unwrap := hasFlag(args, "--unwrap")
	globalSince := getArgValue(args, "--since")

	// Strip global flags from args for command parsing
	cleanArgs := stripFlags(args, []string{"--tab", "--jq", "--port", "--since", "--host", "--token", "--url", "--cdp-host", "--cdp-port", "--idle-tab-timeout", "--file", "--wait-for", "--timeout", "--json-arg", "--interval", "--limit", "--id", "--title", "--parent", "--filename", "--state", "--name", "--display-name", "--description", "--out", "--mode", "--audio", "--viewport", "--dpr", "--mask-selectors", "--max-size", "--preset", "--annotations", "--trim", "--speed", "--watermark", "--format", "--fps", "--width", "--height", "--ffmpeg", "--chapters", "--selector", "--rect"}, []string{"--json", "--help", "--version", "--force", "--check", "--unwrap", "--no-auto-await", "--tail", "--no-check", "--remote", "--recursive", "--save-as", "--focused", "--lossless", "--mask-by-default", "--recover", "--baked", "--smooth"})

	if len(cleanArgs) == 0 {
		printHelp()
		os.Exit(0)
	}

	command := cleanArgs[0]
	cmdArgs := cleanArgs[1:]

	// Intercept '<command> [sub] --help' / '<command> [sub] -h' before dispatch
	// so a help request never executes the command (e.g. 'borz update
	// --help' used to perform a real self-update). The top-level 'help
	// [command [sub]]' form is handled explicitly below.
	if command != "help" && helpRequested(args, cmdArgs) {
		// Adapter invocations ('platform/name --help', and also
		// 'site platform/name --help') forward to 'site info' so agents see
		// the adapter's args/domain/example.
		if strings.Contains(command, "/") {
			handleSite([]string{"info", command}, false, "")
			return
		}
		if command == "site" {
			for _, a := range cmdArgs {
				if strings.Contains(a, "/") {
					handleSite([]string{"info", a}, false, "")
					return
				}
			}
		}
		printCommandHelp(resolveHelpKey(command, cmdArgs))
		return
	}

	switch command {
	case "help", "--help", "-h":
		if hasFlag(args, "--all") {
			printAllHelp()
			return
		}
		if len(cmdArgs) > 0 {
			if strings.Contains(cmdArgs[0], "/") {
				handleSite([]string{"info", cmdArgs[0]}, false, "")
				return
			}
			printCommandHelp(resolveHelpKey(cmdArgs[0], cmdArgs[1:]))
			return
		}
		printHelp()
	case "version", "--version", "-v":
		fmt.Println("borz", version)

	// --- Navigation ---
	case "open":
		if len(cmdArgs) == 0 {
			fatal("Usage: borz open <url> [--tab <tabId>] [--new] [--wait-for <selector>] [--timeout <ms>]")
		}
		url := cmdArgs[0]
		req := &protocol.Request{ID: newID(), Action: protocol.ActionOpen, URL: url}
		if globalTabID != "" {
			req.TabID = globalTabID
		}
		if hasFlag(args, "--new") {
			req.New = true
		}
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Printf("Opened: %s (tab: %s)\n", resp.Data.URL, resp.Data.Tab)
			}
		})

	case "snapshot":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionSnapshot}
		if hasFlag(args, "--text-only") || hasFlag(args, "--text") {
			req.Mode = "text"
		}
		if hasFlag(args, "-i") || hasFlag(args, "--interactive") {
			req.Interactive = true
		}
		if hasFlag(args, "-c") || hasFlag(args, "--compact") {
			req.Compact = true
		}
		if v := getArgValue(args, "-d"); v != "" {
			if d, err := strconv.Atoi(v); err == nil {
				req.MaxDepth = &d
			}
		}
		if v := getArgValue(args, "--depth"); v != "" {
			if d, err := strconv.Atoi(v); err == nil {
				req.MaxDepth = &d
			}
		}
		if v := getArgValue(args, "-s"); v != "" {
			req.Selector = v
		}
		if v := getArgValue(args, "--selector"); v != "" {
			req.Selector = v
		}
		if globalTabID != "" {
			req.TabID = globalTabID
		}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil && resp.Data.SnapshotData != nil {
				fmt.Println(resp.Data.SnapshotData.Snapshot)
			}
		})

	case "click":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionClick, Ref: ref}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Clicked")
		})

	case "hover":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionHover, Ref: ref}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Hovered")
		})

	case "fill":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz fill <ref> <text>")
		}
		ref := normalizeRef(cmdArgs[0])
		text := strings.Join(cmdArgs[1:], " ")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionFill, Ref: ref, Text: text}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Filled with: %s\n", text)
		})

	case "type":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz type <ref> <text>")
		}
		ref := normalizeRef(cmdArgs[0])
		text := strings.Join(cmdArgs[1:], " ")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionType_, Ref: ref, Text: text}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Typed: %s\n", text)
		})

	case "check":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionCheck, Ref: ref}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Checked")
		})

	case "uncheck":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionUncheck, Ref: ref}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Unchecked")
		})

	case "select":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz select <ref> <value>")
		}
		ref := normalizeRef(cmdArgs[0])
		value := cmdArgs[1]
		req := &protocol.Request{ID: newID(), Action: protocol.ActionSelect, Ref: ref, Value: value}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Selected: %s\n", value)
		})

	case "eval":
		filePath := getArgValue(args, "--file")
		var script string
		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fatal(fmt.Sprintf("--file: %v", err))
			}
			script = string(data)
			if len(cmdArgs) > 0 {
				fatal("eval: --file and inline script are mutually exclusive")
			}
		} else {
			if len(cmdArgs) == 0 {
				fatal("Usage: borz eval <script> | --file <path>")
			}
			script = strings.Join(cmdArgs, " ")
		}
		if !hasFlag(args, "--no-auto-await") {
			script = jseval.AutoWrapAwait(script)
		}
		jsonArgs, err := jseval.ParseJSONArgs(getAllArgValues(args, "--json-arg"))
		if err != nil {
			fatal(err.Error())
		}
		if prefix := jseval.PrefixJSONArgs(jsonArgs); prefix != "" {
			script = prefix + script
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionEval, Script: script}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		printEval(req, jsonOutput, unwrap)

	case "get":
		if len(cmdArgs) == 0 {
			fatal("Usage: borz get <attribute> [ref]")
		}
		attribute := cmdArgs[0]
		var ref string
		if len(cmdArgs) > 1 {
			ref = normalizeRef(cmdArgs[1])
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionGet, Attribute: attribute, Ref: ref}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Println(resp.Data.Value)
			}
		})

	case "screenshot":
		var path string
		if len(cmdArgs) > 0 {
			path = cmdArgs[0]
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionScreenshot}
		setTab(req, globalTabID)
		sendPrepareAndPrint(req, jsonOutput, func(resp *protocol.Response) error {
			if path == "" {
				return nil
			}
			return saveScreenshotDataURL(path, resp)
		}, func(resp *protocol.Response) {
			if path != "" {
				fmt.Printf("Screenshot saved: %s\n", path)
			} else if resp.Data != nil && resp.Data.DataURL != "" {
				fmt.Println("Screenshot captured (data URL available in JSON output)")
			}
		})

	case "close":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionClose}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Tab closed")
		})

	case "back":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionBack}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Back") })

	case "forward":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionForward}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Forward") })

	case "refresh":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionRefresh}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Refreshed") })

	case "press":
		if len(cmdArgs) == 0 {
			fatal("Usage: borz press <key>")
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionPress, Key: cmdArgs[0]}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Pressed: %s\n", cmdArgs[0])
		})

	case "scroll":
		direction := "down"
		pixels := 300
		if len(cmdArgs) > 0 {
			direction = cmdArgs[0]
		}
		if len(cmdArgs) > 1 {
			if p, err := strconv.Atoi(cmdArgs[1]); err == nil {
				pixels = p
			}
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionScroll, Direction: direction, Pixels: &pixels}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Scrolled %s %d pixels\n", direction, pixels)
		})

	case "wait":
		ms := 1000
		if len(cmdArgs) > 0 {
			if m, err := strconv.Atoi(cmdArgs[0]); err == nil {
				ms = m
			}
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionWait, Ms: &ms}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Waited %d ms\n", ms)
		})

	// --- Tab ---
	case "tab":
		handleTab(cmdArgs, jsonOutput, globalTabID, args)

	// --- Cookies (extension-backed: cross-domain) ---
	case "cookies":
		handleCookies(cmdArgs, jsonOutput)

	case "bookmarks":
		handleBookmarks(cmdArgs, jsonOutput, args)

	case "browser-history":
		handleBrowserHistory(cmdArgs, jsonOutput, args)

	case "downloads":
		handleDownloads(cmdArgs, jsonOutput, args)

	case "window", "windows":
		handleWindows(cmdArgs, jsonOutput, args)

	// --- Frame ---
	case "frame":
		if len(cmdArgs) == 0 || cmdArgs[0] == "main" {
			req := &protocol.Request{ID: newID(), Action: protocol.ActionFrameMain}
			setTab(req, globalTabID)
			sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
				fmt.Println("Switched to main frame")
			})
		} else {
			req := &protocol.Request{ID: newID(), Action: protocol.ActionFrame, Selector: cmdArgs[0]}
			setTab(req, globalTabID)
			sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
				fmt.Printf("Switched to frame: %s\n", cmdArgs[0])
			})
		}

	// --- Dialog ---
	case "dialog":
		subCmd := "accept"
		if len(cmdArgs) > 0 {
			subCmd = cmdArgs[0]
		}
		var promptText string
		if len(cmdArgs) > 1 {
			promptText = cmdArgs[1]
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionDialog, DialogResponse: subCmd, PromptText: promptText}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Dialog handler armed: %s\n", subCmd)
		})

	// --- Network ---
	case "network":
		handleNetwork(cmdArgs, jsonOutput, globalTabID, globalSince, args)

	// --- Console ---
	case "console":
		clear := hasFlag(args, "--clear")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionConsole}
		if clear {
			req.ConsoleCommand = "clear"
		} else {
			req.ConsoleCommand = "get"
		}
		req.Filter = getArgValue(args, "--filter")
		setSince(req, globalSince)
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				for _, msg := range resp.Data.ConsoleMessages {
					fmt.Printf("[%s] %s\n", msg.Type, msg.Text)
				}
			}
		})

	// --- Errors ---
	case "errors":
		clear := hasFlag(args, "--clear")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionErrors}
		if clear {
			req.ErrorsCommand = "clear"
		} else {
			req.ErrorsCommand = "get"
		}
		req.Filter = getArgValue(args, "--filter")
		setSince(req, globalSince)
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				for _, err := range resp.Data.JSErrors {
					fmt.Printf("[error] %s\n", err.Message)
				}
			}
		})

	// --- Trace ---
	case "trace":
		subCmd := "status"
		if len(cmdArgs) > 0 {
			subCmd = cmdArgs[0]
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTrace, TraceCommand: subCmd}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil && resp.Data.TraceStatus != nil {
				s := resp.Data.TraceStatus
				fmt.Printf("Recording: %v, Events: %d\n", s.Recording, s.EventCount)
			}
		})

	// --- Fetch ---
	case "fetch":
		if len(cmdArgs) == 0 {
			fatal("Usage: borz fetch <url>")
		}
		handleFetch(cmdArgs, jsonOutput, globalTabID, args)

	// --- MCP ---
	case "mcp":
		mcpserver.Run(version)

	// --- Daemon ---
	case "daemon":
		handleDaemon(cmdArgs, args)

	// --- Server (remote-accessible HTTP mode) ---
	case "server":
		handleServer(cmdArgs, args)

	// --- Windows service ---
	case "service":
		handleService(cmdArgs, args)

	// --- Client (remote server mode) ---
	case "client":
		handleClient(cmdArgs, args, jsonOutput)

	// --- Status ---
	case "status":
		raw, err := client.GetDaemonStatus()
		if err != nil {
			fatal(err.Error())
		}
		var pretty json.RawMessage
		json.Unmarshal(raw, &pretty)
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))

	// --- Doctor ---
	case "doctor":
		runDoctor(jsonOutput)

	// --- Site ---
	case "site":
		handleSite(cmdArgs, jsonOutput, globalTabID)

	// --- Self-update ---
	case "update":
		err := selfupdate.Run(context.Background(), selfupdate.Options{
			CurrentVersion: version,
			Force:          hasFlag(args, "--force"),
			CheckOnly:      hasFlag(args, "--check"),
			OnReplaced:     stopDaemonAfterUpdate,
		})
		if err != nil {
			fatal(err.Error())
		}

	// --- Extension ---
	case "extension":
		handleExtension(cmdArgs, jsonOutput)

	// --- Recording ---
	case "record":
		handleRecord(cmdArgs, args, jsonOutput)

	// --- History ---
	case "history":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionHistory}
		sendAndPrint(req, jsonOutput, nil)

	default:
		// Try as site command: borz twitter/search "AI"
		if strings.Contains(command, "/") {
			handleSiteRun(command, cmdArgs, jsonOutput, globalTabID)
		} else {
			fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
			if suggestions := suggestCommands(command, 3); len(suggestions) > 0 {
				fmt.Fprintf(os.Stderr, "Did you mean: %s?\n", strings.Join(formatCommandSuggestions("", suggestions), ", "))
			}
			fmt.Fprintln(os.Stderr, "Run 'borz help' for the full command list.")
			os.Exit(1)
		}
	}
}

// --- Tab handling ---

func handleTab(cmdArgs []string, jsonOutput bool, globalTabID string, rawArgs []string) {
	if len(cmdArgs) == 0 || cmdArgs[0] == "list" {
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTabList}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Printf("Tabs (%d total):\n", len(resp.Data.Tabs))
				for _, tab := range resp.Data.Tabs {
					prefix := "  "
					if tab.Active {
						prefix = "* "
					}
					title := tab.Title
					if title == "" {
						title = "(untitled)"
					}
					fmt.Printf("%s[%d] %s - %s (tab: %s)\n", prefix, tab.Index, tab.URL, title, tab.Tab)
				}
			}
		})
		return
	}

	sub := cmdArgs[0]
	switch sub {
	case "events":
		handleTabEvents(rawArgs, jsonOutput)
		return
	case "new":
		url := "about:blank"
		if len(cmdArgs) > 1 {
			url = cmdArgs[1]
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTabNew, URL: url}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Printf("Created tab: %s (tab: %s)\n", resp.Data.URL, resp.Data.Tab)
			}
		})
	case "select":
		tabID := getArgValue(rawArgs, "--id")
		if tabID == "" && len(cmdArgs) > 1 {
			tabID = cmdArgs[1]
		}
		if tabID == "" && globalTabID != "" {
			tabID = globalTabID
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTabSelect}
		// Let the daemon resolve short IDs first, then fall back to numeric
		// indexes. Short tab IDs are hex suffixes and can be all digits.
		req.TabID = tabID
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Printf("Selected: %s - %s\n", resp.Data.URL, resp.Data.Title)
			}
		})
	case "close":
		tabID := getArgValue(rawArgs, "--id")
		if tabID == "" && len(cmdArgs) > 1 {
			tabID = cmdArgs[1]
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTabClose}
		if tabID != "" {
			req.TabID = tabID
		}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Tab closed")
		})
	default:
		// "tab <n>" - select by index
		if idx, err := strconv.Atoi(sub); err == nil {
			i := idx
			req := &protocol.Request{ID: newID(), Action: protocol.ActionTabSelect, Index: &i}
			sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
				if resp.Data != nil {
					fmt.Printf("Selected: %s - %s\n", resp.Data.URL, resp.Data.Title)
				}
			})
		} else {
			fatal(unknownSubcommandHint("tab", sub))
		}
	}
}

// --- Network handling ---

func handleNetwork(cmdArgs []string, jsonOutput bool, globalTabID, globalSince string, rawArgs []string) {
	subCmd := "requests"
	if len(cmdArgs) > 0 {
		subCmd = cmdArgs[0]
	}

	req := &protocol.Request{ID: newID(), Action: protocol.ActionNetwork}

	switch subCmd {
	case "requests":
		req.NetworkCommand = "requests"
		req.Filter = getArgValue(rawArgs, "--filter")
		req.WithBody = hasFlag(rawArgs, "--with-body")
		req.Method = getArgValue(rawArgs, "--method")
		req.Status = getArgValue(rawArgs, "--status")
		setSince(req, globalSince)
	case "clear":
		req.NetworkCommand = "clear"
	default:
		req.NetworkCommand = subCmd
	}

	setTab(req, globalTabID)

	if subCmd == "requests" && hasFlag(rawArgs, "--tail") {
		runTail(req, jsonOutput, parseTailInterval(rawArgs), emitNetworkTail)
		return
	}

	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data != nil && len(resp.Data.NetworkRequests) > 0 {
			for _, nr := range resp.Data.NetworkRequests {
				status := "-"
				if nr.Status != nil {
					status = strconv.Itoa(*nr.Status)
				}
				fmt.Printf("[%s] %s %s %s\n", status, nr.Method, nr.URL, nr.Type)
			}
		}
	})
}

// --- Fetch handling ---

func handleFetch(cmdArgs []string, jsonOutput bool, globalTabID string, rawArgs []string) {
	url := cmdArgs[0]
	method := "GET"
	if v := getArgValue(rawArgs, "--method"); v != "" {
		method = strings.ToUpper(v)
	}

	// Build fetch script
	script := fmt.Sprintf(`(async () => {
		try {
			const resp = await fetch(%q, { method: %q, credentials: 'include' });
			const contentType = resp.headers.get('content-type') || '';
			const isJson = contentType.includes('application/json');
			const text = await resp.text();
			return {
				status: resp.status,
				statusText: resp.statusText,
				contentType: contentType,
				body: isJson ? JSON.parse(text) : text
			};
		} catch(e) {
			return { error: e.message };
		}
	})()`, url, method)

	req := &protocol.Request{ID: newID(), Action: protocol.ActionEval, Script: script}
	setTab(req, globalTabID)
	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data != nil && resp.Data.Result != nil {
			out, _ := json.MarshalIndent(resp.Data.Result, "", "  ")
			fmt.Println(string(out))
		}
	})
}

// resolveIdleTabTimeout returns the idle-tab-close threshold in minutes.
// Precedence: --idle-tab-timeout flag > BORZ_TAB_IDLE_TIMEOUT env >
// config.DefaultIdleTabCloseMinutes. 0 disables the reaper. Negative values
// are clamped to 0. Non-numeric inputs fall back to the next source.
func resolveIdleTabTimeout(rawArgs []string) int {
	if v := getArgValue(rawArgs, "--idle-tab-timeout"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 0 {
				n = 0
			}
			return n
		}
	}
	if v := config.Env("BORZ_TAB_IDLE_TIMEOUT", "BB_BROWSER_TAB_IDLE_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 0 {
				n = 0
			}
			return n
		}
	}
	return config.DefaultIdleTabCloseMinutes
}

// --- Daemon handling ---

func handleDaemon(cmdArgs []string, rawArgs []string) {
	if len(cmdArgs) == 0 {
		// Start daemon in foreground
		startDaemonForeground(rawArgs)
		return
	}

	switch cmdArgs[0] {
	case "status":
		raw, err := client.GetLocalDaemonStatus()
		if err != nil {
			fmt.Println("Daemon is not running")
			return
		}
		out, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
		fmt.Println(string(out))
	case "shutdown", "stop":
		if err := client.StopDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Daemon stopped")
	default:
		startDaemonForeground(rawArgs)
	}
}

// stopDaemonAfterUpdate shuts down a running daemon following a self-update.
// The running daemon is still the *old* binary's process and will silently
// ignore any new request fields the upgraded CLI sends; stopping it lets the
// next CLI call respawn the daemon from the new binary on disk. Best-effort:
// no-op if no daemon is running, and shutdown errors are reported but do not
// fail the update.
//
// Server-mode (non-loopback bind) processes are deliberately *not* auto-stopped:
// they are started by hand with config (CDP host/port, idle-tab timeout, token)
// that we don't have a reliable way to replay, so silently respawning them
// could change their effective configuration. Instead we surface a clear
// instruction so the operator can restart with their original flags.
func stopDaemonAfterUpdate() {
	info, err := client.ReadDaemonJSON()
	if err != nil || info == nil {
		return
	}
	if isRemoteBind(info.Host) {
		fmt.Fprintf(os.Stderr, "Note: borz server running on %s:%d (pid %d) is still on the old binary.\n", info.Host, info.Port, info.PID)
		fmt.Fprintln(os.Stderr, "      Restart it with your original flags so the new binary takes effect:")
		fmt.Fprintln(os.Stderr, "          borz server shutdown")
		fmt.Fprintln(os.Stderr, "          borz server --host <host> --port <port> --token <token> [other flags]")
		return
	}
	if err := client.StopDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "Note: could not stop running daemon (pid %d): %v\n", info.PID, err)
		fmt.Fprintln(os.Stderr, "      Restart it manually so the new binary is in effect: borz daemon shutdown")
		return
	}
	fmt.Fprintf(os.Stderr, "Stopped running daemon (pid %d); next command will relaunch it from the new binary.\n", info.PID)
}

func startDaemonForeground(rawArgs []string) {
	cdpHost := getArgValue(rawArgs, "--cdp-host")
	if cdpHost == "" {
		cdpHost = "127.0.0.1"
	}
	cdpPortStr := getArgValue(rawArgs, "--cdp-port")
	cdpPort := 19825
	if cdpPortStr != "" {
		if p, err := strconv.Atoi(cdpPortStr); err == nil {
			cdpPort = p
		}
	}
	host := getArgValue(rawArgs, "--host")
	if host == "" {
		host = "127.0.0.1"
	}
	portStr := getArgValue(rawArgs, "--port")
	port := 19824
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	// If a healthy daemon is already running, don't try to bind (which would
	// clobber daemon.json and then fail with "address already in use").
	if existing, err := client.ReadDaemonJSON(); err == nil && existing != nil && existing.Port == port && existing.Host == host {
		if client.IsProcessAlive(existing.PID) {
			if _, err := client.GetLocalDaemonStatus(); err == nil {
				fmt.Fprintf(os.Stderr, "borz daemon already running on %s:%d (pid %d)\n", existing.Host, existing.Port, existing.PID)
				return
			}
		}
	}

	// Generate token
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	srv := newDaemonServer(daemon.ServerOptions{
		Host:                host,
		Port:                port,
		Token:               token,
		CDPHost:             cdpHost,
		CDPPort:             cdpPort,
		IdleTabCloseMinutes: resolveIdleTabTimeout(rawArgs),
		Version:             version,
	})

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
		os.Exit(1)
	}
}

// --- Server handling ---

func handleServer(cmdArgs []string, rawArgs []string) {
	if len(cmdArgs) > 0 {
		switch cmdArgs[0] {
		case "status":
			raw, err := client.GetLocalDaemonStatus()
			if err != nil {
				fmt.Println("Server is not running")
				return
			}
			out, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
			fmt.Println(string(out))
			return
		case "shutdown", "stop":
			if err := client.StopDaemon(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Server stopped")
			return
		}
	}

	opts, err := serverOptionsFromArgs(rawArgs, "0.0.0.0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	srv := newDaemonServer(opts)

	fmt.Fprintf(os.Stderr, "borz server starting on %s:%d\n", opts.Host, opts.Port)
	if opts.Token != "" {
		fmt.Fprintln(os.Stderr, "Authorization required: Authorization: Bearer <token>")
	} else {
		fmt.Fprintln(os.Stderr, "Authorization disabled (loopback bind, no token set)")
	}

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func serverOptionsFromArgs(rawArgs []string, defaultHost string) (daemon.ServerOptions, error) {
	cdpHost := getArgValue(rawArgs, "--cdp-host")
	if cdpHost == "" {
		cdpHost = "127.0.0.1"
	}
	cdpPort := 19825
	if v := getArgValue(rawArgs, "--cdp-port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			cdpPort = p
		}
	}

	host := getArgValue(rawArgs, "--host")
	if host == "" {
		host = config.Env("BORZ_SERVER_HOST", "BB_BROWSER_SERVER_HOST")
	}
	if host == "" {
		host = defaultHost
	}

	port := 19824
	if v := getArgValue(rawArgs, "--port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	} else if v := config.Env("BORZ_SERVER_PORT", "BB_BROWSER_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	token := getArgValue(rawArgs, "--token")
	if token == "" {
		token = config.Env("BORZ_TOKEN", "BB_BROWSER_TOKEN")
	}

	if isRemoteBind(host) && token == "" {
		return daemon.ServerOptions{}, fmt.Errorf("--host=%s is non-loopback; refusing to start without a token. Pass --token <secret> or set BORZ_TOKEN", host)
	}

	return daemon.ServerOptions{
		Host:                host,
		Port:                port,
		Token:               token,
		CDPHost:             cdpHost,
		CDPPort:             cdpPort,
		IdleTabCloseMinutes: resolveIdleTabTimeout(rawArgs),
		Version:             version,
	}, nil
}

// --- Windows service handling ---

func handleService(cmdArgs []string, rawArgs []string) {
	sub := "status"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	name := getArgValue(rawArgs, "--name")
	if name == "" {
		name = winservice.DefaultName
	}

	switch sub {
	case "install":
		opts, err := serverOptionsFromArgs(rawArgs, "127.0.0.1")
		if err != nil {
			fatal(err.Error())
		}
		cfg := winservice.Config{
			Name:        name,
			DisplayName: firstNonEmpty(getArgValue(rawArgs, "--display-name"), winservice.DefaultDisplayName),
			Description: firstNonEmpty(getArgValue(rawArgs, "--description"), winservice.DefaultDescription),
			Args:        serviceRunArgs(name, opts),
		}
		if err := winservice.Install(cfg); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Windows service %q installed\n", name)
		fmt.Printf("Run 'borz service start --name %s' to start it.\n", name)
	case "uninstall", "remove":
		if err := winservice.Uninstall(name); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Windows service %q uninstalled\n", name)
	case "start":
		if err := winservice.Start(name); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Windows service %q started\n", name)
	case "stop":
		if err := winservice.Stop(name); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Windows service %q stopped\n", name)
	case "status":
		status, err := winservice.Status(name)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Windows service %q is %s\n", name, status)
	case "run":
		if err := winservice.Run(name, func(ctx context.Context) error {
			opts, err := serverOptionsFromArgs(rawArgs, "127.0.0.1")
			if err != nil {
				return err
			}
			srv := daemon.NewServer(opts)
			return srv.RunContext(ctx)
		}); err != nil {
			fatal(err.Error())
		}
	default:
		fatal("Usage: borz service [install|uninstall|start|stop|status] [--name <name>] [server flags]")
	}
}

func serviceRunArgs(name string, opts daemon.ServerOptions) []string {
	args := []string{
		"service", "run",
		"--name", name,
		"--host", opts.Host,
		"--port", strconv.Itoa(opts.Port),
		"--cdp-host", opts.CDPHost,
		"--cdp-port", strconv.Itoa(opts.CDPPort),
		"--idle-tab-timeout", strconv.Itoa(opts.IdleTabCloseMinutes),
	}
	if opts.Token != "" {
		args = append(args, "--token", opts.Token)
	}
	return args
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isRemoteBind(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return false
	}
	return true
}

// --- Site handling ---

func handleSite(cmdArgs []string, jsonOutput bool, globalTabID string) {
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"list"}
	}

	sub := cmdArgs[0]
	switch sub {
	case "list":
		sites := site.AllSites()
		if jsonOutput {
			printJSON(sites)
		} else {
			grouped := make(map[string][]*site.SiteMeta)
			for _, s := range sites {
				parts := strings.SplitN(s.Name, "/", 2)
				platform := parts[0]
				grouped[platform] = append(grouped[platform], s)
			}
			platforms := make([]string, 0, len(grouped))
			for platform := range grouped {
				platforms = append(platforms, platform)
			}
			sort.Strings(platforms)
			for _, platform := range platforms {
				adapters := grouped[platform]
				fmt.Printf("\n%s:\n", platform)
				for _, a := range adapters {
					var tags []string
					if a.Source == "local" {
						tags = append(tags, "local")
					}
					if a.ReadOnly {
						tags = append(tags, "read-only")
					}
					if a.UsageCount > 0 {
						tags = append(tags, fmt.Sprintf("used:%d", a.UsageCount))
					}
					tagText := ""
					if len(tags) > 0 {
						tagText = " [" + strings.Join(tags, ",") + "]"
					}
					fmt.Printf("  %s - %s%s\n", a.Name, a.Description, tagText)
				}
			}
			fmt.Printf("\nTotal: %d adapters\n", len(sites))
		}

	case "search":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site search <query>")
		}
		results := site.SearchSites(strings.Join(cmdArgs[1:], " "))
		if jsonOutput {
			printJSON(results)
		} else {
			for _, s := range results {
				fmt.Printf("  %s - %s (%s)\n", s.Name, s.Description, s.Domain)
			}
			fmt.Printf("\n%d results\n", len(results))
		}

	case "info":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site info <name>")
		}
		s := site.FindSite(cmdArgs[1])
		if s == nil {
			fatal("Adapter not found: " + cmdArgs[1])
		}
		if jsonOutput {
			printJSON(s)
		} else {
			fmt.Printf("Name:        %s\n", s.Name)
			fmt.Printf("Description: %s\n", s.Description)
			fmt.Printf("Domain:      %s\n", s.Domain)
			fmt.Printf("Source:       %s\n", s.Source)
			fmt.Printf("Source repo:  %s\n", s.SourceRepo)
			fmt.Printf("SHA256:      %s\n", s.SHA256)
			fmt.Printf("Read-only:   %v\n", s.ReadOnly)
			fmt.Printf("Trusted:     %v\n", s.Trusted)
			if s.TimeoutMs > 0 {
				fmt.Printf("Timeout:     %d ms\n", s.TimeoutMs)
			}
			if len(s.ArgOrder) > 0 {
				fmt.Printf("Arg order:   %s\n", strings.Join(s.ArgOrder, ", "))
			}
			if s.Example != "" {
				fmt.Printf("Example:     %s\n", s.Example)
			}
			if len(s.Args) > 0 {
				fmt.Println("Args:")
				for idx, name := range orderedSiteArgNames(s) {
					arg := s.Args[name]
					req := ""
					if arg.Required {
						req = " (required)"
					}
					def := ""
					if arg.Default != "" {
						def = fmt.Sprintf(" default=%q", arg.Default)
					}
					fmt.Printf("  %d. %s%s%s - %s (positional or --%s)\n", idx+1, name, req, def, arg.Description, name)
				}
			}
			if len(s.Output) > 0 {
				fmt.Printf("Output:      %s\n", string(s.Output))
			}
		}

	case "update":
		if err := site.UpdateCommunityRepo(getArgValue(os.Args[1:], "--ref")); err != nil {
			fatal("Update failed: " + err.Error())
		}
		fmt.Println("Community adapters updated")

	case "new":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site new <platform/name>")
		}
		path, err := site.NewAdapterScaffold(cmdArgs[1])
		if err != nil {
			fatal(err.Error())
		}
		fmt.Println(path)

	case "lint":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site lint <name-or-path>")
		}
		handleSiteLint(cmdArgs[1])

	case "trust":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site trust <name>")
		}
		s := site.FindSite(cmdArgs[1])
		if s == nil {
			fatal("Adapter not found: " + cmdArgs[1])
		}
		if err := site.TrustAdapter(s); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("Trusted %s (%s)\n", s.Name, s.SHA256)

	case "run":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site run <name> [args...]")
		}
		handleSiteRun(cmdArgs[1], cmdArgs[2:], jsonOutput, globalTabID)

	default:
		// Try as site name: "borz site twitter/search AI"
		if strings.Contains(sub, "/") {
			handleSiteRun(sub, cmdArgs[1:], jsonOutput, globalTabID)
		} else {
			fatal(unknownSubcommandHint("site", sub))
		}
	}
}

func handleSiteRun(name string, cmdArgs []string, jsonOutput bool, globalTabID string) {
	meta := site.FindSite(name)
	if meta == nil {
		fmt.Fprintf(os.Stderr, "Adapter not found: %s\n", name)
		fmt.Fprintf(os.Stderr, "Run 'borz site update' to pull community adapters.\n")
		os.Exit(1)
	}

	args, err := site.ParseAdapterArgs(meta, cmdArgs)
	if err != nil {
		fatal(err.Error())
	}
	rawArgs := os.Args[1:]
	force := hasFlag(rawArgs, "--force")
	if !force {
		if err := confirmCommunityAdapter(meta); err != nil {
			fatal(err.Error())
		}
	}
	evalReq, err := site.BuildEvalRequestWithOptions(meta, args, globalTabID, site.EvalOptions{
		Force:     force,
		TimeoutMs: parsePositiveInt(getArgValue(rawArgs, "--timeout")),
	})
	if err != nil {
		fatal(err.Error())
	}

	if printEval(evalReq, jsonOutput, hasFlag(rawArgs, "--unwrap")) {
		site.RecordUsage(meta.Name)
	}
}

func handleSiteLint(nameOrPath string) {
	var meta *site.SiteMeta
	var err error
	if strings.HasSuffix(nameOrPath, ".js") || strings.Contains(nameOrPath, string(filepath.Separator)) {
		meta, err = site.ParseSiteMeta(nameOrPath, "local")
	} else {
		meta = site.FindSite(nameOrPath)
		if meta == nil {
			fatal("Adapter not found: " + nameOrPath)
		}
	}
	if err != nil {
		fatal(err.Error())
	}
	issues := site.LintAdapter(meta)
	if len(issues) == 0 {
		fmt.Printf("OK: %s\n", meta.Name)
		return
	}
	hasError := false
	for _, issue := range issues {
		fmt.Printf("%s: %s\n", issue.Level, issue.Message)
		if issue.Level == "error" {
			hasError = true
		}
	}
	if hasError {
		os.Exit(1)
	}
}

func confirmCommunityAdapter(meta *site.SiteMeta) error {
	if meta.Source != "community" {
		return nil
	}
	status, err := site.AdapterTrustStatus(meta)
	if err != nil || status.Trusted {
		return err
	}
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return site.CheckAdapterTrust(meta, false)
	}
	fmt.Fprintf(os.Stderr, "Community adapter %q will run JavaScript in your Chrome session.\nSHA256: %s\nTrust and continue? [y/N] ", meta.Name, status.Hash)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("adapter not trusted")
	}
	return site.TrustAdapter(meta)
}

func orderedSiteArgNames(meta *site.SiteMeta) []string {
	if len(meta.ArgOrder) > 0 {
		return append([]string(nil), meta.ArgOrder...)
	}
	names := make([]string, 0, len(meta.Args))
	for name := range meta.Args {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parsePositiveInt(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// --- Helpers ---

func sendAndPrint(req *protocol.Request, jsonOutput bool, prettyPrint func(*protocol.Response)) bool {
	return sendPrepareAndPrint(req, jsonOutput, nil, prettyPrint)
}

func sendPrepareAndPrint(req *protocol.Request, jsonOutput bool, prepare func(*protocol.Response) error, prettyPrint func(*protocol.Response)) bool {
	resp, err := client.SendCommand(req)
	if err != nil {
		if jsonOutput {
			printJSON(map[string]interface{}{"success": false, "error": err.Error()})
		} else {
			fatal(err.Error())
		}
		return false
	}

	if resp.Success && prepare != nil {
		if err := prepare(resp); err != nil {
			if jsonOutput {
				printJSON(protocol.Response{ID: req.ID, Success: false, Error: err.Error()})
			} else {
				fatal(err.Error())
			}
			return false
		}
	}

	// Apply jq filter
	if jqExpression != "" {
		target := interface{}(resp.Data)
		if target == nil {
			target = resp
		}
		// Marshal + unmarshal to get generic interface
		raw, _ := json.Marshal(target)
		var generic interface{}
		json.Unmarshal(raw, &generic)
		results := jq.Apply(generic, jqExpression)
		for _, r := range results {
			if s, ok := r.(string); ok {
				fmt.Println(s)
			} else {
				out, _ := json.Marshal(r)
				fmt.Println(string(out))
			}
		}
		return resp.Success
	}

	if jsonOutput {
		printJSON(resp)
		return resp.Success
	}

	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if prettyPrint != nil {
		prettyPrint(resp)
	}
	return true
}

func saveScreenshotDataURL(path string, resp *protocol.Response) error {
	if resp == nil || resp.Data == nil || resp.Data.DataURL == "" {
		return fmt.Errorf("screenshot response did not include image data")
	}
	header, encoded, ok := strings.Cut(resp.Data.DataURL, ",")
	if !ok || !strings.HasPrefix(header, "data:image/") || !strings.Contains(header, ";base64") {
		return fmt.Errorf("screenshot response did not include a base64 image data URL")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode screenshot data: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("screenshot response did not include image data")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create screenshot directory: %w", err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write screenshot: %w", err)
	}
	resp.Data.ScreenshotPath = path
	return nil
}

func printJSON(v interface{}) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

// printEval handles eval (and adapter run) output: --jq > --json > --unwrap >
// pretty default. Unwrap prints resp.Data.Result raw — strings without quotes,
// other shapes as JSON.
func printEval(req *protocol.Request, jsonOutput, unwrap bool) bool {
	if jqExpression != "" || jsonOutput {
		return sendAndPrint(req, jsonOutput, nil)
	}
	resp, err := client.SendCommand(req)
	if err != nil {
		fatal(err.Error())
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	if resp.Data == nil || resp.Data.Result == nil {
		return true
	}
	if unwrap {
		switch v := resp.Data.Result.(type) {
		case string:
			fmt.Println(v)
		default:
			out, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(out))
		}
		return true
	}
	out, _ := json.MarshalIndent(resp.Data.Result, "", "  ")
	fmt.Println(string(out))
	return true
}

func setTab(req *protocol.Request, tabID string) {
	if tabID != "" {
		req.TabID = tabID
	}
}

// applyCLIWaitFor pulls --wait-for / --timeout out of rawArgs and onto req.
// Called by every action that benefits from waiting for a post-action DOM
// change (click, fill, press, ..., open). Read-only commands like snapshot
// and get don't bother.
func applyCLIWaitFor(req *protocol.Request, rawArgs []string) {
	if waitFor := getArgValue(rawArgs, "--wait-for"); waitFor != "" {
		req.WaitFor = waitFor
	}
	if v := getArgValue(rawArgs, "--timeout"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms < 0 {
			fatal("--timeout must be a non-negative integer (ms)")
		}
		req.TimeoutMs = &ms
	}
}

func setSince(req *protocol.Request, since string) {
	if since == "" {
		return
	}
	if since == "last_action" {
		req.Since = "last_action"
	} else if n, err := strconv.Atoi(since); err == nil {
		req.Since = n
	}
}

func getRef(args []string) string {
	if len(args) == 0 {
		fatal("Missing ref parameter")
	}
	return normalizeRef(args[0])
}

func normalizeRef(ref string) string {
	return strings.TrimPrefix(ref, "@")
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	os.Exit(1)
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func getArgValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// getAllArgValues collects every value of a repeatable flag, preserving the
// order they appeared on the command line.
func getAllArgValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

func stripFlags(args []string, valueFlags, boolFlags []string) []string {
	valueFlagSet := make(map[string]bool)
	for _, f := range valueFlags {
		valueFlagSet[f] = true
	}
	boolFlagSet := make(map[string]bool)
	for _, f := range boolFlags {
		boolFlagSet[f] = true
	}

	var result []string
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if valueFlagSet[a] {
			skip = true
			continue
		}
		if boolFlagSet[a] {
			continue
		}
		// Also strip short flags that have already been handled
		if a == "-i" || a == "-c" || a == "--interactive" || a == "--compact" ||
			a == "--with-body" || a == "--clear" || a == "--json" || a == "--new" ||
			a == "--text-only" || a == "--text" {
			continue
		}
		if a == "-d" || a == "--depth" || a == "-s" || a == "--selector" ||
			a == "--filter" || a == "--method" || a == "--status" || a == "--id" {
			skip = true
			continue
		}
		result = append(result, a)
	}
	return result
}

func printHelp() {
	fmt.Println(`borz - Your browser is the API

Usage: borz <command> [options]

Per-command help (most useful flags only show up here):
  borz <command> --help        Detailed usage, flags, examples
  borz help <command>          Same, via the 'help' subcommand
  borz help <command> <sub>    Drill into subcommands (e.g. 'tab new')
  borz help --all              Dump every command's help (pipe to a pager)

Navigation:
  open <url> [--new] [--wait-for <sel>] [--timeout <ms>]
                                Open URL (reuses same-URL tab unless --new;
                                --wait-for blocks until the selector appears)
  back / forward / refresh      History navigation
  close                         Close current tab

Interaction:
  click <ref>                   Click element
  hover <ref>                   Hover element
  fill <ref> <text>             Clear and fill input
  type <ref> <text>             Type text (append)
  check <ref> / uncheck <ref>   (Un)check checkbox
  select <ref> <value>          Select option
  press <key>                   Press key (Enter, Tab, ArrowDown, ...)
  scroll <direction> [pixels]   Scroll page
  eval <script> [--unwrap] [--file <path>] [--no-auto-await] [--json-arg name=value]...
                                Execute JavaScript (top-level await
                                auto-wraps in async IIFE; --unwrap prints
                                the result raw; --file reads from disk;
                                --json-arg injects JSON values as top-level
                                consts, repeatable)

Observation:
  snapshot [-i] [-c] [-d N] [-s <sel>] [--text-only]
                                Get accessibility tree (or reader-mode
                                plain text with --text-only)
  screenshot [path]             Take screenshot (path saves on the CLI host)
  get <attribute> [ref]         Get element attribute
  network [requests|clear] [--tail]
                                Network traffic; --tail streams new
                                requests live (Ctrl+C to stop)
  console [--clear]             Console messages
  errors [--clear]              JavaScript errors
  trace [start|stop|status]     Record user actions

Tab Management:
  tab                           List tabs
  tab new [url]                 Create tab
  tab <n>                       Switch to tab
  tab close [n]                 Close tab
  tab select --id <id>          Select by ID
  tab events [--tail]           Browser-level tab events (extension required)

Browser-level (Chrome extension):
  extension status              Connected extension capabilities
  cookies all [domain]          Cookies across every domain
  bookmarks tree/search/...     Browser bookmarks
  browser-history search        Browser history (Chrome-level)
  downloads list/search/...     Browser download manager
  window list/new/focus/close   Browser windows
  tab events [--tail]           Browser event stream (tabs, windows, etc.)

Site Adapters:
  site list / search / info / update     Discover and refresh adapters
  site run <name> [args]                 Run an adapter
  <platform>/<adapter> [args]            Shorthand for 'site run'

Utility:
  fetch <url>                   Authenticated fetch via page session
  status                        Daemon status
  doctor [--json]               Run diagnostic checks on the full stack
  daemon [shutdown]             Start/stop the local daemon
  server --host H --port P --token T [shutdown]
                                Start remote-accessible HTTP server
                                (--token required on non-loopback binds)
  service install|start|stop    Install/control Windows service mode
  client setup <url> [--token T]
  --remote <command>            Route one command to configured server
  update [--check] [--force]    Download latest release and replace self

Global Flags:
  --remote                      Send browser actions/status to configured server
  --tab <id>                    Target tab
  --json                        JSON output
  --jq <expr>                   Filter with jq expression (implies --json)
  --unwrap                      For 'eval'/site adapters: print result raw
  --since <seq|last_action>     Incremental query (network/console/errors)

Refs & snapshots:
  Interaction commands (click, fill, ...) take a <ref> from a prior
  accessibility snapshot. Snapshots render elements as 'button [ref=5]';
  pass "5" (or "@5") as <ref>. Refs regenerate on every snapshot — always
  re-snapshot after navigation or DOM changes.

Tips:
  - Prefer '--wait-for <selector>' over 'wait <ms>' for any SPA-driven
    DOM change. Works on open, click, fill, eval, and other actions.
  - Use 'eval --unwrap' to skip the {success,data,result,...} envelope.
  - Use '--since last_action' on network/console/errors for incremental reads.

Agents & automation:
  See skill.md / llm.txt in this repo for end-to-end guidance on driving
  borz from an agent (MCP, CLI, and HTTP modes).`)
}
