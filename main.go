package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/leolin310148/bb-browser-go/internal/client"
	"github.com/leolin310148/bb-browser-go/internal/daemon"
	"github.com/leolin310148/bb-browser-go/internal/jq"
	mcpserver "github.com/leolin310148/bb-browser-go/internal/mcp"
	"github.com/leolin310148/bb-browser-go/internal/protocol"
	"github.com/leolin310148/bb-browser-go/internal/selfupdate"
	"github.com/leolin310148/bb-browser-go/internal/site"
)

var version = "0.1.0"

var jqExpression string

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		os.Exit(0)
	}

	// Parse global flags
	globalTabID := getArgValue(args, "--tab")
	jqExpression = getArgValue(args, "--jq")
	jsonOutput := hasFlag(args, "--json") || jqExpression != ""
	globalSince := getArgValue(args, "--since")

	// Strip global flags from args for command parsing
	cleanArgs := stripFlags(args, []string{"--tab", "--jq", "--port", "--since", "--host", "--token", "--cdp-host", "--cdp-port"}, []string{"--json", "--help", "--version", "--force", "--check"})

	if len(cleanArgs) == 0 {
		printHelp()
		os.Exit(0)
	}

	command := cleanArgs[0]
	cmdArgs := cleanArgs[1:]

	// Intercept '<command> [sub] --help' / '<command> [sub] -h' before dispatch
	// so a help request never executes the command (e.g. 'bb-browser update
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
		fmt.Println("bb-browser-go", version)

	// --- Navigation ---
	case "open":
		if len(cmdArgs) == 0 {
			fatal("Usage: bb-browser open <url> [--tab <tabId>] [--new]")
		}
		url := cmdArgs[0]
		req := &protocol.Request{ID: newID(), Action: protocol.ActionOpen, URL: url}
		if globalTabID != "" {
			req.TabID = globalTabID
		}
		if hasFlag(args, "--new") {
			req.New = true
		}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Printf("Opened: %s (tab: %s)\n", resp.Data.URL, resp.Data.Tab)
			}
		})

	case "snapshot":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionSnapshot}
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
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Clicked")
		})

	case "hover":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionHover, Ref: ref}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Hovered")
		})

	case "fill":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser fill <ref> <text>")
		}
		ref := normalizeRef(cmdArgs[0])
		text := strings.Join(cmdArgs[1:], " ")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionFill, Ref: ref, Text: text}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Filled with: %s\n", text)
		})

	case "type":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser type <ref> <text>")
		}
		ref := normalizeRef(cmdArgs[0])
		text := strings.Join(cmdArgs[1:], " ")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionType_, Ref: ref, Text: text}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Typed: %s\n", text)
		})

	case "check":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionCheck, Ref: ref}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Checked")
		})

	case "uncheck":
		ref := getRef(cmdArgs)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionUncheck, Ref: ref}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Println("Unchecked")
		})

	case "select":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser select <ref> <value>")
		}
		ref := normalizeRef(cmdArgs[0])
		value := cmdArgs[1]
		req := &protocol.Request{ID: newID(), Action: protocol.ActionSelect, Ref: ref, Value: value}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Selected: %s\n", value)
		})

	case "eval":
		if len(cmdArgs) == 0 {
			fatal("Usage: bb-browser eval <script>")
		}
		script := strings.Join(cmdArgs, " ")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionEval, Script: script}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil && resp.Data.Result != nil {
				out, _ := json.MarshalIndent(resp.Data.Result, "", "  ")
				fmt.Println(string(out))
			}
		})

	case "get":
		if len(cmdArgs) == 0 {
			fatal("Usage: bb-browser get <attribute> [ref]")
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
		req := &protocol.Request{ID: newID(), Action: protocol.ActionScreenshot, Path: path}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil && resp.Data.DataURL != "" {
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
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Back") })

	case "forward":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionForward}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Forward") })

	case "refresh":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionRefresh}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) { fmt.Println("Refreshed") })

	case "press":
		if len(cmdArgs) == 0 {
			fatal("Usage: bb-browser press <key>")
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionPress, Key: cmdArgs[0]}
		setTab(req, globalTabID)
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
			fatal("Usage: bb-browser fetch <url>")
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

	// --- Site ---
	case "site":
		handleSite(cmdArgs, jsonOutput, globalTabID)

	// --- Self-update ---
	case "update":
		err := selfupdate.Run(context.Background(), selfupdate.Options{
			CurrentVersion: version,
			Force:          hasFlag(args, "--force"),
			CheckOnly:      hasFlag(args, "--check"),
		})
		if err != nil {
			fatal(err.Error())
		}

	// --- History ---
	case "history":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionHistory}
		sendAndPrint(req, jsonOutput, nil)

	default:
		// Try as site command: bb-browser twitter/search "AI"
		if strings.Contains(command, "/") {
			handleSiteRun(command, cmdArgs, jsonOutput, globalTabID)
		} else {
			fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'bb-browser help' for usage.\n", command)
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
		// Try as index
		if idx, err := strconv.Atoi(tabID); err == nil {
			req.Index = &idx
		} else {
			req.TabID = tabID
		}
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
		if idx, err := strconv.Atoi(tabID); err == nil {
			req.Index = &idx
		} else if tabID != "" {
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
			fatal("Unknown tab subcommand: " + sub)
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

// --- Daemon handling ---

func handleDaemon(cmdArgs []string, rawArgs []string) {
	if len(cmdArgs) == 0 {
		// Start daemon in foreground
		startDaemonForeground(rawArgs)
		return
	}

	switch cmdArgs[0] {
	case "status":
		raw, err := client.GetDaemonStatus()
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
			if _, err := client.GetDaemonStatus(); err == nil {
				fmt.Fprintf(os.Stderr, "bb-browser daemon already running on %s:%d (pid %d)\n", existing.Host, existing.Port, existing.PID)
				return
			}
		}
	}

	// Generate token
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	srv := daemon.NewServer(daemon.ServerOptions{
		Host:    host,
		Port:    port,
		Token:   token,
		CDPHost: cdpHost,
		CDPPort: cdpPort,
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
			raw, err := client.GetDaemonStatus()
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
		host = os.Getenv("BB_BROWSER_SERVER_HOST")
	}
	if host == "" {
		host = "0.0.0.0"
	}

	port := 19824
	if v := getArgValue(rawArgs, "--port"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	} else if v := os.Getenv("BB_BROWSER_SERVER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			port = p
		}
	}

	token := getArgValue(rawArgs, "--token")
	if token == "" {
		token = os.Getenv("BB_BROWSER_TOKEN")
	}

	if isRemoteBind(host) && token == "" {
		fmt.Fprintf(os.Stderr,
			"Error: --host=%s is non-loopback; refusing to start without a token.\n"+
				"       Pass --token <secret> or set BB_BROWSER_TOKEN.\n", host)
		os.Exit(1)
	}

	srv := daemon.NewServer(daemon.ServerOptions{
		Host:    host,
		Port:    port,
		Token:   token,
		CDPHost: cdpHost,
		CDPPort: cdpPort,
	})

	fmt.Fprintf(os.Stderr, "bb-browser server starting on %s:%d\n", host, port)
	if token != "" {
		fmt.Fprintln(os.Stderr, "Authorization required: Authorization: Bearer <token>")
	} else {
		fmt.Fprintln(os.Stderr, "Authorization disabled (loopback bind, no token set)")
	}

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
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
			for platform, adapters := range grouped {
				fmt.Printf("\n%s:\n", platform)
				for _, a := range adapters {
					src := ""
					if a.Source == "local" {
						src = " [local]"
					}
					fmt.Printf("  %s - %s%s\n", a.Name, a.Description, src)
				}
			}
			fmt.Printf("\nTotal: %d adapters\n", len(sites))
		}

	case "search":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser site search <query>")
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
			fatal("Usage: bb-browser site info <name>")
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
			if s.Example != "" {
				fmt.Printf("Example:     %s\n", s.Example)
			}
			if len(s.Args) > 0 {
				fmt.Println("Args:")
				for name, arg := range s.Args {
					req := ""
					if arg.Required {
						req = " (required)"
					}
					fmt.Printf("  %s%s - %s\n", name, req, arg.Description)
				}
			}
		}

	case "update":
		if err := site.UpdateCommunityRepo(); err != nil {
			fatal("Update failed: " + err.Error())
		}
		fmt.Println("Community adapters updated")

	case "run":
		if len(cmdArgs) < 2 {
			fatal("Usage: bb-browser site run <name> [args...]")
		}
		handleSiteRun(cmdArgs[1], cmdArgs[2:], jsonOutput, globalTabID)

	default:
		// Try as site name: "bb-browser site twitter/search AI"
		if strings.Contains(sub, "/") {
			handleSiteRun(sub, cmdArgs[1:], jsonOutput, globalTabID)
		} else {
			fatal("Unknown site subcommand: " + sub)
		}
	}
}

func handleSiteRun(name string, cmdArgs []string, jsonOutput bool, globalTabID string) {
	meta := site.FindSite(name)
	if meta == nil {
		fmt.Fprintf(os.Stderr, "Adapter not found: %s\n", name)
		fmt.Fprintf(os.Stderr, "Run 'bb-browser site update' to pull community adapters.\n")
		os.Exit(1)
	}

	args := site.ParseAdapterArgs(meta, cmdArgs)
	evalReq, err := site.BuildEvalRequest(meta, args, globalTabID)
	if err != nil {
		fatal(err.Error())
	}

	sendAndPrint(evalReq, jsonOutput, func(resp *protocol.Response) {
		if resp.Data != nil && resp.Data.Result != nil {
			out, _ := json.MarshalIndent(resp.Data.Result, "", "  ")
			fmt.Println(string(out))
		}
	})
}

// --- Helpers ---

func sendAndPrint(req *protocol.Request, jsonOutput bool, prettyPrint func(*protocol.Response)) {
	resp, err := client.SendCommand(req)
	if err != nil {
		if jsonOutput {
			printJSON(map[string]interface{}{"success": false, "error": err.Error()})
		} else {
			fatal(err.Error())
		}
		return
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
		return
	}

	if jsonOutput {
		printJSON(resp)
		return
	}

	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if prettyPrint != nil {
		prettyPrint(resp)
	}
}

func printJSON(v interface{}) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func setTab(req *protocol.Request, tabID string) {
	if tabID != "" {
		req.TabID = tabID
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
			a == "--with-body" || a == "--clear" || a == "--json" || a == "--new" {
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
	fmt.Println(`bb-browser-go - Your browser is the API

Usage: bb-browser <command> [options]

Navigation:
  open <url>                    Open URL in new tab
  back                          Go back
  forward                       Go forward
  refresh                       Refresh page
  close                         Close current tab

Interaction:
  click <ref>                   Click element
  hover <ref>                   Hover element
  fill <ref> <text>             Clear and fill input
  type <ref> <text>             Type text (append)
  check <ref>                   Check checkbox
  uncheck <ref>                 Uncheck checkbox
  select <ref> <value>          Select option
  press <key>                   Press key
  scroll <direction> [pixels]   Scroll page
  eval <script>                 Execute JavaScript

Observation:
  snapshot [-i] [-c] [-d N]     Get accessibility tree
  screenshot [path]             Take screenshot
  get <attribute> [ref]         Get element attribute
  network [requests|clear]      Network traffic
  console [--clear]             Console messages
  errors [--clear]              JavaScript errors
  trace [start|stop|status]     Record user actions

Tab Management:
  tab                           List tabs
  tab new [url]                 Create tab
  tab <n>                       Switch to tab
  tab close [n]                 Close tab
  tab select --id <id>          Select by ID

Site Adapters:
  site list                     List adapters
  site search <query>           Search adapters
  site info <name>              Adapter details
  site update                   Pull community adapters
  site <name> [args...]         Run adapter

Utility:
  fetch <url>                   Authenticated fetch
  status                        Daemon status
  daemon                        Start daemon
  daemon shutdown               Stop daemon
  server [--host H --port P     Start remote-accessible HTTP server
          --token T]            (exposes /v1/* REST routes; binds 0.0.0.0 by
                                default, requires --token on non-loopback)
  server shutdown               Stop server
  update [--check] [--force]    Download latest release and replace self

Global Flags:
  --tab <id>                    Target tab
  --json                        JSON output
  --jq <expr>                   Filter with jq expression
  --since <seq|last_action>     Incremental query

Refs & snapshots:
  Interaction commands (click, fill, ...) take a <ref> from a prior
  accessibility snapshot. Snapshots render elements as 'button [ref=5]';
  pass "5" (or "@5") as <ref>. Refs regenerate on every snapshot — always
  re-snapshot after navigation or DOM changes.

Per-command help:
  bb-browser <command> --help   Detailed usage, flags, and examples
  bb-browser help <command>     Same, via the 'help' subcommand

Agents & automation:
  See skill.md / llm.txt in this repo for end-to-end guidance on driving
  bb-browser from an agent (MCP, CLI, and HTTP modes).`)
}
