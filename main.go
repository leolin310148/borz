package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon"
	"github.com/leolin310148/borz/internal/jq"
	"github.com/leolin310148/borz/internal/jseval"
	mcpserver "github.com/leolin310148/borz/internal/mcp"
	borzprofile "github.com/leolin310148/borz/internal/profile"
	"github.com/leolin310148/borz/internal/protocol"
	"github.com/leolin310148/borz/internal/selfupdate"
	"github.com/leolin310148/borz/internal/site"
	"github.com/leolin310148/borz/internal/winservice"
)

var version = "0.1.0"

const daemonNotRunningMessage = "Daemon is not running. This is normal in on-demand mode; it will start automatically with the next browser command."

var jqExpression string
var exitFunc = os.Exit
var randomRead = rand.Read

// Pre/post delay are CLI-global: any command can carry them and the daemon
// will sleep accordingly before / after the action. Resolved in main() from
// --pre-delay / --post-delay and propagated by sendPrepareAndPrint.
var (
	globalPreDelayMs  *int
	globalPostDelayMs *int
)

var cliValueFlags = []string{
	"-d", "--depth", "-s", "--selector", "--filter", "--method", "--status", "--id",
	"--header", "--body",
	"--profile", "--tab", "--jq", "--port", "--since", "--host", "--token", "--url",
	"--cdp-host", "--cdp-port", "--daemon-port", "--daemon-token", "--idle-tab-timeout", "--max-tabs", "--file", "--wait-for",
	"--timeout", "--pre-delay", "--post-delay",
	"--modifiers", "--commands",
	"--lines",
	"--json-arg", "--interval", "--limit", "--title", "--parent", "--cdp", "--as",
	"--filename", "--state", "--name", "--display-name", "--description", "--out",
	"--mode", "--audio", "--viewport", "--dpr", "--mask-selectors", "--max-size",
	"--preset", "--annotations", "--trim", "--speed", "--watermark", "--format", "--role",
	"--fps", "--width", "--height", "--ffmpeg", "--chapters", "--rect", "--ref", "--scope",
	"--annotate",
	"--protocol", "--transport", "--has-resident-key", "--has-user-verification",
	"--is-user-verified", "--automatic-presence",
}

var cliValueFlagSet = makeFlagSet(cliValueFlags)

var cliBoolFlags = []string{
	"-i", "-c",
	"--all", "--all-profiles", "--baked", "--check", "--clear", "--close-owned-browser", "--compact", "--copy", "--diff", "--ensure-browser", "--focused",
	"--force", "--help", "--interactive", "--json", "--lossless", "--managed",
	"--mask-by-default", "--mobile", "--new", "--no-auto-await", "--no-check",
	"--no-touch", "--paste", "--recover", "--recursive", "--remote", "--reset", "--save-as",
	"--smooth", "--tail", "--text", "--text-only", "--touch", "--unwrap",
	"--version", "--with-body", "--hide-refs", "--show-refs",
}

var cliBoolFlagSet = makeFlagSet(cliBoolFlags)

type daemonRunner interface {
	Run() error
}

var newDaemonServer = func(opts daemon.ServerOptions) daemonRunner {
	return daemon.NewServer(opts)
}

var restartLocalDaemon = client.RestartDaemonPreservingBrowser

func main() {
	client.SetLocalVersion(version)

	args := os.Args[1:]
	if len(args) == 0 {
		printHelp()
		exitFunc(0)
	}

	// Parse global flags.
	// 'borz profile ...' reuses --remote as a value flag (profile add <name>
	// --remote <url>), so the deprecated routing alias below must not fire
	// and stripFlags must consume the flag's value.
	profileCommand := firstPositionalArg(args) == "profile"

	profileName, profileFlagSet := getArgValueOK(args, "--profile")
	if profileFlagSet && strings.TrimSpace(profileName) == "" {
		fatal("--profile requires a non-empty value")
	}
	if !profileFlagSet {
		profileName = config.Env("BORZ_PROFILE", "BB_BROWSER_PROFILE")
	}
	if err := config.SetProfile(profileName); err != nil {
		fatal(err.Error())
	}
	client.SetRequestContext("cli", cliSessionID(
		os.Getenv("BORZ_SESSION_ID"), os.Getenv("TMUX_PANE"), os.Getenv("TERM_SESSION_ID"), os.Getppid(),
	))
	remoteFlag := !profileCommand && hasFlag(args, "--remote")
	client.SetLegacyRemoteFlag(remoteFlag)
	if remoteFlag {
		if profileFlagSet {
			fatal("--remote and --profile are mutually exclusive; --profile selects the transport")
		}
		fmt.Fprintf(os.Stderr, "Warning: '--remote' is deprecated; use '--profile %s' (or BORZ_PROFILE=%s) instead\n",
			borzprofile.LegacyRemoteName, borzprofile.LegacyRemoteName)
		// Deprecated alias: bare --remote selects the 'remote' profile that
		// client.json migrates into.
		if err := config.SetProfile(borzprofile.LegacyRemoteName); err != nil {
			fatal(err.Error())
		}
	}
	globalTabID := getArgValue(args, "--tab")
	jqExpression = getArgValue(args, "--jq")
	jsonOutput := hasFlag(args, "--json") || jqExpression != ""
	unwrap := hasFlag(args, "--unwrap")
	globalSince := getArgValue(args, "--since")
	globalPreDelayMs = parseGlobalDelayFlag(args, "--pre-delay")
	globalPostDelayMs = parseGlobalDelayFlag(args, "--post-delay")

	// Strip global flags from args for command parsing
	var extraValueFlags []string
	if profileCommand {
		extraValueFlags = []string{"--remote"}
	}
	cleanArgs := stripFlags(args, extraValueFlags, nil)

	if len(cleanArgs) == 0 {
		if hasFlag(args, "--version") {
			fmt.Println("borz", version)
			return
		}
		printHelp()
		exitFunc(0)
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
		applyCLIViewport(req, args)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				if resp.Data.Reused {
					fmt.Printf("Reused existing tab (not reloaded): %s (tab: %s; run 'borz refresh' to reload)\n", resp.Data.URL, resp.Data.Tab)
					return
				}
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
		if hasFlag(args, "--diff") {
			req.Diff = true
		}
		showRefsFlag := hasFlag(args, "--show-refs")
		hideRefsFlag := hasFlag(args, "--hide-refs")
		if showRefsFlag && hideRefsFlag {
			fatal("--show-refs and --hide-refs are mutually exclusive")
		}
		if showRefsFlag || hideRefsFlag {
			showRefs := showRefsFlag
			req.ShowRefs = &showRefs
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
		if v := getArgValue(args, "--role"); v != "" {
			req.Role = v
		}
		if globalTabID != "" {
			req.TabID = globalTabID
		}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data == nil {
				return
			}
			// --diff prefers the structured diff text. The first call after
			// navigation (or the very first ever) is a baseline reset — print
			// a header so the agent knows it isn't really a "diff".
			if req.Diff && resp.Data.SnapshotDiffData != nil {
				dd := resp.Data.SnapshotDiffData
				if dd.BaselineReset {
					fmt.Println("(baseline reset — full snapshot follows)")
				}
				if dd.Diff != "" {
					fmt.Println(dd.Diff)
				} else {
					fmt.Println("(no changes since last snapshot)")
				}
				return
			}
			if resp.Data.SnapshotData != nil {
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

	case "upload":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz upload <ref> <file> [file...]")
		}
		ref := normalizeRef(cmdArgs[0])
		files := append([]string{}, cmdArgs[1:]...)
		req := &protocol.Request{ID: newID(), Action: protocol.ActionUpload, Ref: ref, Files: files}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Uploaded %d file(s)\n", len(files))
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
		jsonArgs, err := jseval.ParseJSONArgs(getAllArgValues(args, "--json-arg"))
		if err != nil {
			fatal(err.Error())
		}
		script = jseval.PrepareCLI(script, jseval.PrefixJSONArgs(jsonArgs), !hasFlag(args, "--no-auto-await"))
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

	case "clear-refs":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionClearRefs}
		if globalTabID != "" {
			req.TabID = globalTabID
		}
		sendAndPrint(req, jsonOutput, func(*protocol.Response) {
			fmt.Println("Cleared snapshot ref overlay")
		})

	case "screenshot":
		var path string
		if len(cmdArgs) > 0 {
			path = cmdArgs[0]
		}
		annotations, err := parseScreenshotAnnotations(getAllArgValues(args, "--annotate"))
		if err != nil {
			fatal(err.Error())
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionScreenshot, Annotations: annotations}
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

	case "viewport":
		handleViewport(cmdArgs, jsonOutput, globalTabID, args)

	case "webauthn":
		handleWebAuthn(cmdArgs, args, jsonOutput, globalTabID)

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
		modifiers, err := parsePressModifiers(getArgValue(args, "--modifiers"))
		if err != nil {
			fatal(err.Error())
		}
		commands := parsePressCommands(getArgValue(args, "--commands"))
		req := &protocol.Request{ID: newID(), Action: protocol.ActionPress, Key: cmdArgs[0], Modifiers: modifiers, Commands: commands}
		setTab(req, globalTabID)
		applyCLIWaitFor(req, args)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Pressed: %s\n", cmdArgs[0])
		})

	case "clipboard-write":
		filePath := getArgValue(args, "--file")
		var text string
		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fatal(fmt.Sprintf("--file: %v", err))
			}
			text = string(data)
			if len(cmdArgs) > 0 {
				fatal("clipboard-write: --file and inline text are mutually exclusive")
			}
		} else {
			if len(cmdArgs) == 0 {
				fatal("Usage: borz clipboard-write <text> | --file <path> [--paste]")
			}
			text = strings.Join(cmdArgs, " ")
		}
		paste := hasFlag(args, "--paste")
		req := &protocol.Request{ID: newID(), Action: protocol.ActionClipboardWrite, Text: text, Paste: paste}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if paste {
				fmt.Println("Clipboard written and pasted (Ctrl+Shift+V)")
			} else {
				fmt.Println("Clipboard written")
			}
		})

	case "term-text":
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTermText}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data != nil {
				fmt.Println(resp.Data.Value)
			}
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
			if m, err := strconv.Atoi(strings.TrimSpace(cmdArgs[0])); err == nil && m >= 0 {
				ms = m
			} else {
				fatal("wait requires a non-negative integer (ms)")
			}
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionWait, Ms: &ms}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			fmt.Printf("Waited %d ms\n", ms)
		})

	// --- Tab ---
	case "tab", "tabs":
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

	// --- Page (tab-level emulation) ---
	case "page":
		handlePage(cmdArgs, jsonOutput, globalTabID)

	// --- File chooser (native file-picker dialog) ---
	case "filechooser":
		handleFileChooser(cmdArgs, jsonOutput, globalTabID)

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
		handleDialog(cmdArgs, jsonOutput, globalTabID)

	// --- Network ---
	case "network":
		handleNetwork(cmdArgs, jsonOutput, globalTabID, globalSince, args)

	// --- Console ---
	case "console":
		handleConsole(jsonOutput, globalTabID, globalSince, args)

	// --- Errors ---
	case "errors":
		handleErrors(jsonOutput, globalTabID, globalSince, args)

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

	// --- Client (remote server mode, deprecated) ---
	case "client":
		handleClient(cmdArgs, args, jsonOutput)

	// --- Profiles (declarative browser targets) ---
	case "profile":
		handleProfile(cmdArgs, args, jsonOutput)

	// --- Status ---
	case "status":
		raw, err := client.GetDaemonStatus()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "daemon is not running") {
				fmt.Println(daemonNotRunningMessage)
				return
			}
			fatal(err.Error())
		}
		var pretty json.RawMessage
		json.Unmarshal(raw, &pretty)
		out, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(out))

	// --- Doctor ---
	case "doctor":
		runDoctor(jsonOutput)

	// --- Managed browser identity ---
	case "browser":
		handleBrowser(cmdArgs, args, jsonOutput)

	// --- Local operational logs ---
	case "logs":
		handleLogs(cmdArgs, args, globalSince, jsonOutput)

	// --- Agent feedback ---
	case "feedback":
		handleFeedback(cmdArgs, args, jsonOutput)

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
		handleExtension(cmdArgs, jsonOutput, args)

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
			if hint := commandUsageHint(command); hint != "" {
				fmt.Fprintln(os.Stderr, hint)
			}
			if suggestions := suggestCommands(command, 3); len(suggestions) > 0 {
				fmt.Fprintf(os.Stderr, "Did you mean: %s?\n", strings.Join(formatCommandSuggestions("", suggestions), ", "))
			}
			fmt.Fprintln(os.Stderr, "Run 'borz help' for the full command list.")
			exitFunc(1)
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
		applyCLIViewport(req, rawArgs)
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
	case "front":
		tabID := getArgValue(rawArgs, "--id")
		if tabID == "" && len(cmdArgs) > 1 {
			tabID = cmdArgs[1]
		}
		if tabID == "" && globalTabID != "" {
			tabID = globalTabID
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionTabFront}
		if tabID != "" {
			req.TabID = tabID
		}
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data == nil {
				return
			}
			visibility := ""
			if result, ok := resp.Data.Result.(map[string]interface{}); ok {
				visibility, _ = result["visibilityState"].(string)
			}
			if visibility != "" {
				fmt.Printf("Brought to front: %s (visibilityState: %s)\n", resp.Data.URL, visibility)
			} else {
				fmt.Printf("Brought to front: %s\n", resp.Data.URL)
			}
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

// --- Page handling (tab-level emulation) ---

func handlePage(cmdArgs []string, jsonOutput bool, globalTabID string) {
	if len(cmdArgs) == 0 {
		fatal("Usage: borz page visibility [visible|hidden|reset]")
	}
	switch cmdArgs[0] {
	case "visibility":
		state := ""
		if len(cmdArgs) > 1 {
			state = strings.ToLower(strings.TrimSpace(cmdArgs[1]))
			switch state {
			case "visible", "hidden", "reset":
			default:
				fatal("page visibility state must be visible, hidden, or reset")
			}
		}
		req := &protocol.Request{ID: newID(), Action: protocol.ActionPageVisibility, Visibility: state}
		setTab(req, globalTabID)
		sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
			if resp.Data == nil {
				return
			}
			result, _ := resp.Data.Result.(map[string]interface{})
			current, _ := result["visibilityState"].(string)
			override, _ := result["override"].(string)
			switch {
			case state == "" && override != "":
				fmt.Printf("visibilityState: %s (overridden: %s)\n", current, override)
			case state == "":
				fmt.Printf("visibilityState: %s\n", current)
			case state == "reset":
				fmt.Printf("Visibility override removed (visibilityState: %s)\n", current)
			default:
				fmt.Printf("Visibility override set: %s\n", state)
			}
		})
	default:
		fatal(unknownSubcommandHint("page", cmdArgs[0]))
	}
}

// --- Dialog handling (native alert/confirm/prompt/beforeunload) ---

func handleDialog(cmdArgs []string, jsonOutput bool, globalTabID string) {
	subCmd := "accept"
	if len(cmdArgs) > 0 {
		subCmd = cmdArgs[0]
	}
	var promptText string
	switch subCmd {
	case "accept", "dismiss":
		if len(cmdArgs) > 1 {
			promptText = cmdArgs[1]
		}
	case "disarm", "status":
	default:
		fatal(unknownSubcommandHint("dialog", subCmd))
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionDialog, DialogResponse: subCmd, PromptText: promptText}
	setTab(req, globalTabID)
	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data == nil {
			fmt.Printf("Dialog handler armed: %s\n", subCmd)
			return
		}
		info, _ := resp.Data.DialogInfo.(map[string]interface{})
		message, _ := info["message"].(string)
		switch infoType, _ := info["type"].(string); infoType {
		case "handled":
			fmt.Println(message)
		case "disarmed":
			fmt.Println("Dialog handler disarmed")
		case "status":
			printDialogStatus(info)
		default:
			fmt.Printf("Dialog handler armed: %s\n", subCmd)
		}
	})
}

func printDialogStatus(info map[string]interface{}) {
	if pending, ok := info["pending"].(map[string]interface{}); ok {
		kind, _ := pending["type"].(string)
		message, _ := pending["message"].(string)
		fmt.Printf("Open dialog: %s — %q\n", kind, message)
		if blocked, _ := info["blocked"].(bool); blocked {
			fmt.Println("  BLOCKING the page. Run 'borz dialog accept' or 'borz dialog dismiss' to release it.")
		}
		if def, _ := pending["defaultPrompt"].(string); def != "" {
			fmt.Printf("  Default prompt text: %q\n", def)
		}
	} else {
		fmt.Println("Open dialog: none")
	}
	if armed, _ := info["armed"].(bool); armed {
		action, _ := info["action"].(string)
		line := fmt.Sprintf("Armed handler: %s", action)
		if text, _ := info["promptText"].(string); text != "" {
			line += fmt.Sprintf(" (prompt text %q)", text)
		}
		fmt.Println(line)
	} else {
		fmt.Println("Armed handler: none")
	}
	history, _ := info["history"].([]interface{})
	if len(history) == 0 {
		return
	}
	fmt.Printf("Recent dialogs (%d):\n", len(history))
	for _, item := range history {
		ev, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		kind, _ := ev["type"].(string)
		message, _ := ev["message"].(string)
		outcome := "resolved outside borz"
		if auto, _ := ev["autoHandled"].(bool); auto {
			handledAs, _ := ev["handledAs"].(string)
			outcome = "borz " + handledAs
		}
		fmt.Printf("  %s %q — %s\n", kind, message, outcome)
	}
}

// --- File chooser handling (native file-picker dialog) ---

func handleFileChooser(cmdArgs []string, jsonOutput bool, globalTabID string) {
	subCmd := "status"
	if len(cmdArgs) > 0 {
		subCmd = cmdArgs[0]
	}
	req := &protocol.Request{ID: newID(), Action: protocol.ActionFileChooser, FileChooserCommand: subCmd}
	switch subCmd {
	case "accept":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz filechooser accept <file> [file...]")
		}
		req.Files = append([]string{}, cmdArgs[1:]...)
	case "cancel", "disarm", "status":
	default:
		fatal(unknownSubcommandHint("filechooser", subCmd))
	}
	setTab(req, globalTabID)
	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		if resp.Data == nil {
			return
		}
		result, _ := resp.Data.Result.(map[string]interface{})
		armed, _ := result["armed"].(bool)
		action, _ := result["action"].(string)
		switch subCmd {
		case "accept":
			fmt.Printf("File chooser armed: next dialog receives %d file(s)\n", len(req.Files))
		case "cancel":
			fmt.Println("File chooser armed: next dialog will be cancelled")
		case "disarm":
			fmt.Println("File chooser disarmed")
		default:
			if armed {
				fmt.Printf("File chooser armed: %s\n", action)
			} else {
				fmt.Println("File chooser not armed")
			}
		}
	})
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
		if rawLimit, ok := getArgValueOK(rawArgs, "--limit"); ok {
			limit, err := strconv.Atoi(strings.TrimSpace(rawLimit))
			if err != nil || limit <= 0 {
				fatal("--limit must be a positive integer")
			}
			req.Limit = &limit
		}
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

func handleConsole(jsonOutput bool, globalTabID, globalSince string, rawArgs []string) {
	clear := hasFlag(rawArgs, "--clear")
	req := &protocol.Request{ID: newID(), Action: protocol.ActionConsole}
	if clear {
		req.ConsoleCommand = "clear"
	} else {
		req.ConsoleCommand = "get"
	}
	req.Filter = getArgValue(rawArgs, "--filter")
	setSince(req, globalSince)
	setTab(req, globalTabID)

	if !clear && hasFlag(rawArgs, "--tail") {
		runTail(req, jsonOutput, parseTailInterval(rawArgs), emitConsoleTail)
		return
	}

	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		emitConsoleTail(resp, false)
	})
}

func handleErrors(jsonOutput bool, globalTabID, globalSince string, rawArgs []string) {
	clear := hasFlag(rawArgs, "--clear")
	req := &protocol.Request{ID: newID(), Action: protocol.ActionErrors}
	if clear {
		req.ErrorsCommand = "clear"
	} else {
		req.ErrorsCommand = "get"
	}
	req.Filter = getArgValue(rawArgs, "--filter")
	setSince(req, globalSince)
	setTab(req, globalTabID)

	if !clear && hasFlag(rawArgs, "--tail") {
		runTail(req, jsonOutput, parseTailInterval(rawArgs), emitErrorsTail)
		return
	}

	sendAndPrint(req, jsonOutput, func(resp *protocol.Response) {
		emitErrorsTail(resp, false)
	})
}

// --- Fetch handling ---

func handleFetch(cmdArgs []string, jsonOutput bool, globalTabID string, rawArgs []string) {
	url := cmdArgs[0]
	method := "GET"
	if v := getArgValue(rawArgs, "--method"); v != "" {
		method = strings.ToUpper(strings.TrimSpace(v))
	}

	headers := make([][2]string, 0)
	for _, raw := range getAllArgValues(rawArgs, "--header") {
		name, value, ok := strings.Cut(raw, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			fatal("--header must use 'Name: value' format")
			return
		}
		headers = append(headers, [2]string{name, strings.TrimSpace(value)})
	}
	body, bodySet := getArgValueOK(rawArgs, "--body")

	urlJSON, _ := json.Marshal(url)
	methodJSON, _ := json.Marshal(method)
	headersJSON, _ := json.Marshal(headers)
	bodyJSON, _ := json.Marshal(body)
	bodyOption := ""
	if bodySet {
		bodyOption = ", body: " + string(bodyJSON)
	}

	// Build fetch script from JSON-encoded values so URLs, headers, and bodies
	// cannot alter the JavaScript expression.
	script := fmt.Sprintf(`(async () => {
		try {
			const resp = await fetch(%s, { method: %s, credentials: 'include', headers: %s%s });
			const contentType = resp.headers.get('content-type') || '';
			const isJson = /\bapplication\/(?:[\w.-]+\+)?json\b/i.test(contentType);
			const text = await resp.text();
			return {
				status: resp.status,
				statusText: resp.statusText,
				contentType: contentType,
				body: isJson ? (text.trim() === '' ? null : JSON.parse(text)) : text
			};
		} catch(e) {
			return { error: e.message };
		}
	})()`, urlJSON, methodJSON, headersJSON, bodyOption)

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
// Precedence: --idle-tab-timeout flag > BORZ_TAB_IDLE_TIMEOUT env > the
// active profile's idleTabTimeout > config.DefaultIdleTabCloseMinutes.
// 0 disables the reaper. Negative flag/env values are clamped to 0.
// Non-numeric inputs fall back to the next source.
func resolveIdleTabTimeout(rawArgs []string) int {
	if v := getArgValue(rawArgs, "--idle-tab-timeout"); v != "" {
		if n, ok := parseIdleTabTimeout(v); ok {
			return n
		}
	}
	if v := config.Env("BORZ_TAB_IDLE_TIMEOUT", "BB_BROWSER_TAB_IDLE_TIMEOUT"); v != "" {
		if n, ok := parseIdleTabTimeout(v); ok {
			return n
		}
	}
	if target, err := client.ActiveTarget(); err == nil && target.IdleTabTimeout != nil {
		return *target.IdleTabTimeout
	}
	return config.DefaultIdleTabCloseMinutes
}

func parseIdleTabTimeout(v string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	return n, true
}

// resolveMaxTabs returns the maximum number of page tabs retained by the
// daemon. Precedence: --max-tabs flag > BORZ_MAX_TABS env > the active
// profile's maxTabs > config.DefaultMaxTabs. 0 disables the cap.
func resolveMaxTabs(rawArgs []string) int {
	if v := getArgValue(rawArgs, "--max-tabs"); v != "" {
		if n, ok := parseMaxTabs(v); ok {
			return n
		}
	}
	if v := config.Env("BORZ_MAX_TABS", "BB_BROWSER_MAX_TABS"); v != "" {
		if n, ok := parseMaxTabs(v); ok {
			return n
		}
	}
	if target, err := client.ActiveTarget(); err == nil && target.MaxTabs != nil {
		return *target.MaxTabs
	}
	return config.DefaultMaxTabs
}

func parseMaxTabs(v string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	if n < 0 {
		n = 0
	}
	return n, true
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
		if url, isRemote := remoteProfileURL(); isRemote {
			fmt.Println(remoteProfileLifecycleNote("daemon", url))
			return
		}
		raw, err := client.GetLocalDaemonStatus()
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "daemon is not running") {
				fmt.Println(daemonNotRunningMessage)
				return
			}
			fatal(fmt.Sprintf("check daemon status: %v", err))
		}
		out, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
		fmt.Println(string(out))
	case "token":
		handleDaemonToken(rawArgs)
	case "shutdown", "stop":
		if url, isRemote := remoteProfileURL(); isRemote {
			fatal(remoteProfileLifecycleNote("daemon", url))
			return
		}
		if err := client.StopDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			exitFunc(1)
		}
		fmt.Println("Daemon stopped")
	case "restart":
		if url, isRemote := remoteProfileURL(); isRemote {
			fatal(remoteProfileLifecycleNote("daemon", url))
			return
		}
		if len(cmdArgs) != 1 {
			fatal("Usage: borz daemon restart [--json]")
			return
		}
		result, err := restartLocalDaemon()
		if err != nil {
			if hasFlag(rawArgs, "--json") {
				printJSON(map[string]interface{}{"success": false, "error": err.Error()})
				exitFunc(1)
				return
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			exitFunc(1)
			return
		}
		if hasFlag(rawArgs, "--json") {
			printJSON(result)
			return
		}
		if result.PreviousPID > 0 {
			fmt.Printf("Daemon restarted: PID %d -> %d (managed browser preserved)\n", result.PreviousPID, result.NewPID)
		} else {
			fmt.Printf("Daemon started: PID %d (managed browser preserved)\n", result.NewPID)
		}
	default:
		startDaemonForeground(rawArgs)
	}
}

// remoteProfileURL reports whether the active profile declares the remote
// transport, so lifecycle commands can say so instead of pretending a local
// daemon exists.
func remoteProfileURL() (string, bool) {
	target, err := client.ActiveTarget()
	if err != nil || target.Kind != borzprofile.TransportRemote {
		return "", false
	}
	return target.Remote.URL, true
}

func remoteProfileLifecycleNote(kind, url string) string {
	name := borzprofile.Normalize(config.Profile())
	return fmt.Sprintf("profile %q is a remote profile (%s); there is no local %s to manage.\nUse 'borz --profile %s status' for the remote server's status.", name, url, kind, name)
}

// stopDaemonAfterUpdate shuts down a running daemon following a self-update.
// The running daemon is still the *old* binary's process and will silently
// ignore any new request fields the upgraded CLI sends; stopping it lets the
// next CLI call respawn the daemon from the new binary on disk. Best-effort:
// no-op if no daemon is running, and shutdown errors are reported but do not
// fail the update.
//
// Even when /shutdown returns OK, the daemon can stay alive — long-running
// streaming requests (tab events, console tail, recordings) keep
// httpSrv.Shutdown blocked, and an os.Exit happens only after that drain.
// If the pid is still alive a couple of seconds after /shutdown, escalate to
// SIGKILL so the next CLI command can respawn from the new binary on disk
// instead of talking to a wedged old process.
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
	stopErr := client.StopDaemon()
	if client.WaitForProcessExit(info.PID, 2*time.Second) {
		if stopErr != nil {
			fmt.Fprintf(os.Stderr, "Note: /shutdown errored (%v) but daemon (pid %d) exited.\n", stopErr, info.PID)
		}
		fmt.Fprintf(os.Stderr, "Stopped running daemon (pid %d); next command will relaunch it from the new binary.\n", info.PID)
		return
	}
	if stopErr != nil {
		fmt.Fprintf(os.Stderr, "Note: graceful shutdown of daemon (pid %d) failed: %v — forcing kill.\n", info.PID, stopErr)
	} else {
		fmt.Fprintf(os.Stderr, "Note: daemon (pid %d) did not exit after /shutdown — forcing kill.\n", info.PID)
	}
	if kerr := client.KillDaemon(info.PID); kerr != nil {
		fmt.Fprintf(os.Stderr, "Note: could not kill stuck daemon (pid %d): %v\n", info.PID, kerr)
		fmt.Fprintln(os.Stderr, "      Restart it manually so the new binary is in effect without closing managed Chrome: borz daemon restart")
		return
	}
	fmt.Fprintf(os.Stderr, "Killed stuck daemon (pid %d); next command will relaunch it from the new binary.\n", info.PID)
}

func startDaemonForeground(rawArgs []string) {
	cdpHost := getArgValue(rawArgs, "--cdp-host")
	cdpHost = strings.TrimSpace(cdpHost)
	if cdpHost == "" {
		cdpHost = "127.0.0.1"
	}
	cdpPort, _, err := parseTCPPortFlag(rawArgs, "--cdp-port", 19825)
	if err != nil {
		fatal(err.Error())
	}
	host := getArgValue(rawArgs, "--host")
	if host == "" {
		host = "127.0.0.1"
	}
	port, portFlagSet, err := parseTCPPortFlag(rawArgs, "--port", config.DaemonPort)
	if err != nil {
		fatal(err.Error())
	}

	// If a healthy daemon is already running, don't try to bind (which would
	// clobber daemon.json and then fail with "address already in use").
	if existing, err := client.ReadDaemonJSON(); err == nil && existing != nil && (!portFlagSet || existing.Port == port) && existing.Host == host {
		if client.IsProcessAlive(existing.PID) {
			if _, err := client.GetLocalDaemonStatus(); err == nil {
				fmt.Fprintf(os.Stderr, "borz daemon already running on %s:%d (pid %d)\n", existing.Host, existing.Port, existing.PID)
				return
			}
		}
	}
	if !portFlagSet {
		if p, err := client.DaemonPortForProfile(); err == nil {
			port = p
		}
	}

	token := ""
	if target, targetErr := client.ActiveTarget(); targetErr == nil {
		token = strings.TrimSpace(target.DaemonToken)
	}
	if token == "" {
		token, err = randomHex(16)
		if err != nil {
			fatal(fmt.Sprintf("generate daemon auth token: %v", err))
		}
	}

	srv := newDaemonServer(daemon.ServerOptions{
		Host:                host,
		Port:                port,
		Token:               token,
		CDPHost:             cdpHost,
		CDPPort:             cdpPort,
		Profile:             borzprofile.Normalize(config.Profile()),
		CloseOwnedBrowser:   hasFlag(rawArgs, "--close-owned-browser"),
		IdleTabCloseMinutes: resolveIdleTabTimeout(rawArgs),
		MaxTabs:             resolveMaxTabs(rawArgs),
		Version:             version,
	})

	if err := srv.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Daemon error: %v\n", err)
		exitFunc(1)
	}
}

// --- Server handling ---

func handleServer(cmdArgs []string, rawArgs []string) {
	if len(cmdArgs) > 0 {
		switch cmdArgs[0] {
		case "status":
			if url, isRemote := remoteProfileURL(); isRemote {
				fmt.Println(remoteProfileLifecycleNote("server", url))
				return
			}
			raw, err := client.GetLocalDaemonStatus()
			if err != nil {
				fmt.Println("Server is not running")
				return
			}
			out, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
			fmt.Println(string(out))
			return
		case "shutdown", "stop":
			if url, isRemote := remoteProfileURL(); isRemote {
				fatal(remoteProfileLifecycleNote("server", url))
				return
			}
			if err := client.StopDaemon(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				exitFunc(1)
			}
			fmt.Println("Server stopped")
			return
		}
	}

	opts, err := serverOptionsFromArgs(rawArgs, "0.0.0.0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		exitFunc(1)
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
		exitFunc(1)
	}
}

func serverOptionsFromArgs(rawArgs []string, defaultHost string) (daemon.ServerOptions, error) {
	cdpHost := getArgValue(rawArgs, "--cdp-host")
	cdpHost = strings.TrimSpace(cdpHost)
	if cdpHost == "" {
		cdpHost = "127.0.0.1"
	}
	cdpPort, _, err := parseTCPPortFlag(rawArgs, "--cdp-port", 19825)
	if err != nil {
		return daemon.ServerOptions{}, err
	}

	host := getArgValue(rawArgs, "--host")
	host = strings.TrimSpace(host)
	if host == "" {
		host = strings.TrimSpace(config.Env("BORZ_SERVER_HOST", "BB_BROWSER_SERVER_HOST"))
	}
	if host == "" {
		host = defaultHost
	}

	port := 19824
	if p, ok, err := parseTCPPortFlag(rawArgs, "--port", 19824); err != nil {
		return daemon.ServerOptions{}, err
	} else if ok {
		port = p
	} else if v, name := config.EnvWithName("BORZ_SERVER_PORT", "BB_BROWSER_SERVER_PORT"); v != "" {
		p, err := parseTCPPort(name, v, 19824)
		if err != nil {
			return daemon.ServerOptions{}, err
		}
		port = p
	}

	token := getArgValue(rawArgs, "--token")
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(config.Env("BORZ_TOKEN", "BB_BROWSER_TOKEN"))
	}

	if isRemoteBind(host) && token == "" {
		return daemon.ServerOptions{}, fmt.Errorf("--host=%s is non-loopback; refusing to start without a token. Pass --token <secret> or set BORZ_TOKEN", host)
	}

	var ensureBrowser func() error
	var browserOwned func() bool
	if hasFlag(rawArgs, "--ensure-browser") {
		if isRemoteBind(cdpHost) {
			return daemon.ServerOptions{}, fmt.Errorf("--ensure-browser only manages a local browser, but --cdp-host=%s is not loopback. Drop the flag or point --cdp-host at 127.0.0.1", cdpHost)
		}
		launchPort := cdpPort
		var owned atomic.Bool
		ensureBrowser = func() error {
			if err := client.LaunchManagedBrowser(launchPort); err != nil {
				return err
			}
			owned.Store(true)
			return nil
		}
		browserOwned = owned.Load
	}

	return daemon.ServerOptions{
		Host:                host,
		Port:                port,
		Token:               token,
		CDPHost:             cdpHost,
		CDPPort:             cdpPort,
		Profile:             borzprofile.Normalize(config.Profile()),
		IdleTabCloseMinutes: resolveIdleTabTimeout(rawArgs),
		MaxTabs:             resolveMaxTabs(rawArgs),
		Version:             version,
		EnsureBrowser:       ensureBrowser,
		BrowserOwned:        browserOwned,
	}, nil
}

func parseTCPPort(name, raw string, fallback int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if fallback <= 0 {
			return 0, fmt.Errorf("%s must be a TCP port between 1 and 65535", name)
		}
		return fallback, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be a TCP port between 1 and 65535", name)
	}
	return port, nil
}

func parseTCPPortFlag(args []string, flag string, fallback int) (int, bool, error) {
	raw, ok := getArgValueOK(args, flag)
	if !ok {
		return fallback, false, nil
	}
	port, err := parseTCPPort(flag, raw, 0)
	if err != nil {
		return 0, true, err
	}
	return port, true, nil
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
		fmt.Printf("Windows service %q installed or updated\n", name)
		fmt.Printf("Run 'borz service start --name %s' to start it, or restart it if it is already running.\n", name)
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
		"--max-tabs", strconv.Itoa(opts.MaxTabs),
	}
	if opts.EnsureBrowser != nil {
		args = append(args, "--ensure-browser")
	}
	if config.Profile() != "" {
		args = append(args, "--profile", config.Profile())
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
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	if i := strings.LastIndex(host, "%"); i >= 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// --- Site handling ---

func handleSite(cmdArgs []string, jsonOutput bool, globalTabID string) {
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"list"}
	}
	scope, err := resolveSiteScope(os.Args[1:])
	if err != nil {
		fatal(err.Error())
	}

	sub := cmdArgs[0]
	switch sub {
	case "list":
		var sites []*site.SiteMeta
		if scope == siteScopeServer {
			sites, err = loadServerSites()
		} else {
			sites = site.AllSites()
		}
		if err != nil {
			fatal(err.Error())
		}
		printSiteList(sites, jsonOutput)

	case "search":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site search <query>")
		}
		query := strings.Join(cmdArgs[1:], " ")
		var results []*site.SiteMeta
		if scope == siteScopeServer {
			sites, loadErr := loadServerSites()
			if loadErr != nil {
				fatal(loadErr.Error())
			}
			results = site.SearchSiteList(sites, query)
		} else {
			results = site.SearchSites(query)
		}
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
		var adapter *site.SiteMeta
		if scope == siteScopeServer {
			adapter, err = loadServerSite(cmdArgs[1])
		} else {
			adapter = site.FindSite(cmdArgs[1])
			if adapter == nil {
				err = fmt.Errorf("adapter not found: %s", cmdArgs[1])
			}
		}
		if err != nil {
			fatal(err.Error())
		}
		printSiteInfo(adapter, jsonOutput)

	case "update":
		if err := requireClientSiteScope(scope, sub); err != nil {
			fatal(err.Error())
		}
		if err := site.UpdateCommunityRepo(getArgValue(os.Args[1:], "--ref")); err != nil {
			fatal("Update failed: " + err.Error())
		}
		fmt.Println("Community adapters updated")

	case "new":
		if err := requireClientSiteScope(scope, sub); err != nil {
			fatal(err.Error())
		}
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site new <platform/name>")
		}
		path, err := site.NewAdapterScaffold(cmdArgs[1])
		if err != nil {
			fatal(err.Error())
		}
		fmt.Println(path)

	case "lint":
		if err := requireClientSiteScope(scope, sub); err != nil {
			fatal(err.Error())
		}
		if len(cmdArgs) < 2 {
			fatal("Usage: borz site lint <name-or-path>")
		}
		handleSiteLint(cmdArgs[1])

	case "trust":
		if err := requireClientSiteScope(scope, sub); err != nil {
			fatal(err.Error())
		}
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
		handleSiteRunWithScope(cmdArgs[1], cmdArgs[2:], jsonOutput, globalTabID, scope)

	default:
		// Try as site name: "borz site twitter/search AI"
		if strings.Contains(sub, "/") {
			handleSiteRunWithScope(sub, cmdArgs[1:], jsonOutput, globalTabID, scope)
		} else {
			fatal(unknownSubcommandHint("site", sub))
		}
	}
}

func handleSiteRun(name string, cmdArgs []string, jsonOutput bool, globalTabID string) {
	scope, err := resolveSiteScope(os.Args[1:])
	if err != nil {
		fatal(err.Error())
	}
	handleSiteRunWithScope(name, cmdArgs, jsonOutput, globalTabID, scope)
}

func handleSiteRunWithScope(name string, cmdArgs []string, jsonOutput bool, globalTabID string, scope siteScope) {
	var meta *site.SiteMeta
	var err error
	if scope == siteScopeServer {
		meta, err = loadServerSite(name)
	} else {
		meta = site.FindSite(name)
	}
	if err != nil {
		fatal(err.Error())
	}
	if meta == nil {
		fmt.Fprintf(os.Stderr, "Adapter not found: %s\n", name)
		if scope == siteScopeClient {
			fmt.Fprintf(os.Stderr, "Run 'borz site update' to pull community adapters.\n")
		}
		exitFunc(1)
	}

	adapterArgs, err := site.ParseAdapterArgs(meta, cmdArgs)
	if err != nil {
		fatal(err.Error())
	}
	rawArgs := os.Args[1:]
	force := hasFlag(rawArgs, "--force")
	if scope == siteScopeClient && !force {
		if err := confirmCommunityAdapter(meta); err != nil {
			fatal(err.Error())
		}
	}
	timeoutMs, err := parseOptionalPositiveIntFlag(rawArgs, "--timeout")
	if err != nil {
		fatal(err.Error())
	}
	if scope == siteScopeServer {
		resp, runErr := runServerSite(meta.Name, adapterArgs, globalTabID, force, timeoutMs)
		if runErr != nil {
			fatal(runErr.Error())
		}
		printEvalResponse(resp, jsonOutput, hasFlag(rawArgs, "--unwrap"))
		return
	}
	evalReq, err := site.BuildEvalRequestWithOptions(meta, adapterArgs, globalTabID, site.EvalOptions{
		Force:     force,
		TimeoutMs: timeoutMs,
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
		exitFunc(1)
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

func parsePositiveIntArg(name, raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

func parseOptionalPositiveIntFlag(args []string, flag string) (int, error) {
	raw, ok := getArgValueOK(args, flag)
	if !ok {
		return 0, nil
	}
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("%s must be a positive integer", flag)
	}
	return parsePositiveIntArg(flag, raw)
}

// --- Helpers ---

func sendAndPrint(req *protocol.Request, jsonOutput bool, prettyPrint func(*protocol.Response)) bool {
	return sendPrepareAndPrint(req, jsonOutput, nil, prettyPrint)
}

// parseGlobalDelayFlag reads --pre-delay / --post-delay off the raw args.
// Returns nil when the flag is absent; fatals on negative or non-integer.
func parseGlobalDelayFlag(args []string, name string) *int {
	v, ok := getArgValueOK(args, name)
	if !ok {
		return nil
	}
	v = strings.TrimSpace(v)
	ms, err := strconv.Atoi(v)
	if err != nil || ms < 0 {
		fatal(name + " must be a non-negative integer (ms)")
	}
	out := ms
	return &out
}

// applyGlobalDelays stamps the parsed --pre-delay/--post-delay onto req
// unless a per-command setter already populated them.
func applyGlobalDelays(req *protocol.Request) {
	if req == nil {
		return
	}
	if req.PreDelayMs == nil && globalPreDelayMs != nil {
		v := *globalPreDelayMs
		req.PreDelayMs = &v
	}
	if req.PostDelayMs == nil && globalPostDelayMs != nil {
		v := *globalPostDelayMs
		req.PostDelayMs = &v
	}
}

func sendPrepareAndPrint(req *protocol.Request, jsonOutput bool, prepare func(*protocol.Response) error, prettyPrint func(*protocol.Response)) bool {
	applyGlobalDelays(req)
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
		results := applyJQExpression(resp, jqExpression)
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
		exitFunc(1)
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

func applyJQExpression(resp *protocol.Response, expression string) []interface{} {
	if resp == nil {
		return nil
	}
	if resp.Data != nil {
		results := applyJQTo(resp.Data, expression)
		if len(results) > 0 {
			return results
		}
	}
	return applyJQTo(resp, expression)
}

func applyJQTo(target interface{}, expression string) []interface{} {
	raw, err := json.Marshal(target)
	if err != nil {
		return nil
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil
	}
	return jq.Apply(generic, expression)
}

// printEval handles eval (and adapter run) output: --jq > --json > --unwrap >
// pretty default. Unwrap prints resp.Data.Result raw — strings without quotes,
// other shapes as JSON.
func printEval(req *protocol.Request, jsonOutput, unwrap bool) bool {
	if jqExpression != "" || jsonOutput {
		return sendAndPrint(req, jsonOutput, nil)
	}
	applyGlobalDelays(req)
	resp, err := client.SendCommand(req)
	if err != nil {
		fatal(err.Error())
	}
	if !resp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		exitFunc(1)
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

// printEvalResponse applies the same output precedence as printEval to a
// response that was obtained through a non-/command endpoint, such as
// /v1/sites/run for server-scoped adapters.
func printEvalResponse(resp *protocol.Response, jsonOutput, unwrap bool) bool {
	if resp == nil {
		fatal("site run returned an empty response")
	}
	if jqExpression != "" {
		for _, result := range applyJQExpression(resp, jqExpression) {
			if value, ok := result.(string); ok {
				fmt.Println(value)
			} else {
				out, _ := json.Marshal(result)
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
		exitFunc(1)
	}
	if resp.Data == nil || resp.Data.Result == nil {
		return true
	}
	if unwrap {
		switch value := resp.Data.Result.(type) {
		case string:
			fmt.Println(value)
		default:
			out, _ := json.MarshalIndent(value, "", "  ")
			fmt.Println(string(out))
		}
		return true
	}
	out, _ := json.MarshalIndent(resp.Data.Result, "", "  ")
	fmt.Println(string(out))
	return true
}

func setTab(req *protocol.Request, tabID string) {
	tabID = strings.TrimSpace(tabID)
	if tabID != "" {
		req.TabID = tabID
	}
}

// applyCLIWaitFor pulls --wait-for / --timeout out of rawArgs and onto req.
// Called by every action that benefits from waiting for a post-action DOM
// change (click, fill, press, ..., open). Read-only commands like snapshot
// and get don't bother.
func applyCLIWaitFor(req *protocol.Request, rawArgs []string) {
	if waitFor, ok := getArgValueOK(rawArgs, "--wait-for"); ok {
		waitFor = strings.TrimSpace(waitFor)
		if waitFor == "" {
			fatal("--wait-for requires a selector")
		}
		req.WaitFor = waitFor
	}
	if v, ok := getArgValueOK(rawArgs, "--timeout"); ok {
		ms, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || ms < 0 {
			fatal("--timeout must be a non-negative integer (ms)")
		}
		req.TimeoutMs = &ms
	}
}

func setSince(req *protocol.Request, since string) {
	since = strings.TrimSpace(since)
	if since == "" {
		return
	}
	if strings.EqualFold(since, "last_action") {
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
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ref), "@"))
}

func parseScreenshotAnnotations(values []string) ([]protocol.ScreenshotAnnotation, error) {
	annotations := make([]protocol.ScreenshotAnnotation, 0, len(values))
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--annotate must use REF=TEXT (got %q)", value)
		}
		ref := normalizeRef(parts[0])
		if ref == "" {
			return nil, fmt.Errorf("--annotate requires a ref before '='")
		}
		if strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("--annotate requires text after '=' for ref %s", ref)
		}
		annotations = append(annotations, protocol.ScreenshotAnnotation{Ref: ref, Text: parts[1]})
	}
	return annotations, nil
}

func newID() string {
	id, err := randomHex(8)
	if err != nil {
		fatal(fmt.Sprintf("generate request id: %v", err))
	}
	return id
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := randomRead(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func fatal(msg string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	exitFunc(1)
}

// firstPositionalArg returns the first token that stripFlags would keep,
// so global-flag handling can special-case a command before flag stripping.
func firstPositionalArg(args []string) string {
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if cliValueFlagSet[a] {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				skip = true
			}
			continue
		}
		if hasInlineValueFlag(a, cliValueFlagSet) || cliBoolFlagSet[a] {
			continue
		}
		return a
	}
	return ""
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
	value, _ := getArgValueOK(args, flag)
	return value
}

func getArgValueOK(args []string, flag string) (string, bool) {
	for i, a := range args {
		if value, ok := inlineArgValue(a, flag); ok {
			return value, true
		}
		if a == flag {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				return args[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

func parsePressModifiers(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var modifiers []string
	for _, part := range strings.Split(raw, ",") {
		modifier := strings.ToLower(strings.TrimSpace(part))
		switch modifier {
		case "alt", "ctrl", "meta", "shift":
			modifiers = append(modifiers, modifier)
		default:
			return nil, fmt.Errorf("--modifiers contains unsupported modifier %q", part)
		}
	}
	return modifiers, nil
}

// parsePressCommands splits a comma-separated --commands value into CDP
// editing command names (e.g. "selectAll,copy"). Values are passed through
// verbatim — the daemon forwards them to Input.dispatchKeyEvent.
func parsePressCommands(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var commands []string
	for _, part := range strings.Split(raw, ",") {
		if cmd := strings.TrimSpace(part); cmd != "" {
			commands = append(commands, cmd)
		}
	}
	return commands
}

// getAllArgValues collects every value of a repeatable flag, preserving the
// order they appeared on the command line. Missing values are preserved as
// empty strings so command-specific validation can report a useful error.
func getAllArgValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if value, ok := inlineArgValue(a, flag); ok {
			out = append(out, value)
			continue
		}
		if a == flag {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				out = append(out, args[i+1])
			} else {
				out = append(out, "")
			}
		}
	}
	return out
}

func inlineArgValue(arg, flag string) (string, bool) {
	prefix := flag + "="
	if strings.HasPrefix(arg, prefix) {
		return strings.TrimPrefix(arg, prefix), true
	}
	return "", false
}

func hasInlineValueFlag(arg string, flags map[string]bool) bool {
	for flag := range flags {
		if _, ok := inlineArgValue(arg, flag); ok {
			return true
		}
	}
	return false
}

func stripFlags(args []string, valueFlags, boolFlags []string) []string {
	valueFlagSet := makeFlagSet(cliValueFlags)
	for _, f := range valueFlags {
		valueFlagSet[f] = true
	}
	boolFlagSet := make(map[string]bool, len(cliBoolFlagSet)+len(boolFlags))
	for f := range cliBoolFlagSet {
		boolFlagSet[f] = true
	}
	for _, f := range boolFlags {
		boolFlagSet[f] = true
	}

	var result []string
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if valueFlagSet[a] {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				skip = true
			}
			continue
		}
		if hasInlineValueFlag(a, valueFlagSet) {
			continue
		}
		if boolFlagSet[a] {
			continue
		}
		result = append(result, a)
	}
	return result
}

func makeFlagSet(flags []string) map[string]bool {
	set := make(map[string]bool, len(flags))
	for _, f := range flags {
		set[f] = true
	}
	return set
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
  upload <ref> <file...>        Attach local files to a file input or label
  filechooser accept <file...>  Arm the next native file picker before click
  press <key>                   Press key (Enter, Tab, ArrowDown, ...)
  clipboard-write <text> [--paste]
                                Set clipboard; --paste fires Ctrl+Shift+V to
                                paste atomically into a focused terminal
  scroll <direction> [pixels]   Scroll page
  eval <script> [--unwrap] [--file <path>] [--no-auto-await] [--json-arg name=value]...
                                Execute JavaScript (top-level await
                                auto-wraps in async IIFE; --unwrap prints
                                the result raw; --file reads from disk;
                                --json-arg injects JSON values as top-level
                                consts, repeatable)

Observation:
  snapshot [-i] [-c] [-d N] [-s <sel>] [--role <role>] [--show-refs|--hide-refs] [--text-only] [--diff]
                                Get accessibility tree (or reader-mode
                                plain text with --text-only; --diff shows
                                changes; ref visibility is configurable)
  clear-refs                    Remove snapshot ref overlays; keep refs usable
  screenshot [path]             Take screenshot; --annotate ref=text adds callouts
  viewport [status|current|mobile|tablet|desktop|WxH|reset]
                                Inspect or emulate viewport for responsive UI
  get <attribute> [ref]         Get element attribute
  term-text                     Read xterm.js terminal text incl. scrollback
                                (replaces screenshot OCR; reads same-origin
                                iframes like JumpServer Luna)
  network [requests|clear] [--tail]
                                Network traffic; --tail streams new
                                requests live (Ctrl+C to stop)
  console [--clear] [--tail]    Console messages; --tail streams new
                                messages live
  errors [--clear] [--tail]     JavaScript errors; --tail streams new
                                errors live
  trace [start|stop|status]     Record user actions
  record start|stop|render      Capture browser flows into .borzrec bundles

Browser testing:
  webauthn enable/add/...       Passkey-ready virtual authenticators scoped
                                to a selected tab's Chrome CDP session

Tab Management:
  tab                           List tabs
  tab new [url]                 Create tab
  tab <n>                       Switch to tab
  tab close [n]                 Close tab
  tab select --id <id>          Select by ID
  tab events [--tail]           Browser-level tab events (extension required)
  frame <selector> / frame main Switch interactions and eval into/out of iframe

Browser-level (Chrome extension):
  extension status|ping         Inspect or verify selected profile extension
  extension status --all-profiles
                                Audit extension connections without starting browsers
  cookies all [domain]          Cookies across every domain
  bookmarks tree/search/...     Browser bookmarks
  browser-history search        Browser history (Chrome-level)
  downloads list/search/...     Browser download manager
  window list/new/focus/close   Browser windows
  tab events [--tail]           Browser event stream (tabs, windows, etc.)

Site Adapters:
  site list / search / info [--scope S]  Discover client/server adapters
  site run <name> [args] [--scope S]     Run a client/server adapter
  site update / new / lint / trust       Manage client-side adapters
  <platform>/<adapter> [args]            Shorthand for 'site run'

Utility:
  fetch <url>                   Authenticated fetch via page session
  status                        Daemon status
  doctor [--json]               Run diagnostic checks on the full stack
  logs path|tail|stats          Inspect local operational logs and failures
  feedback <message>            Record usage feedback (friction, missing
                                features, ideas) to ~/.borz/feedback.jsonl
  feedback list|path            Review recorded feedback
  daemon [status|token|restart|shutdown]
                                Inspect or control the selected local daemon
  browser status|adopt          Inspect or repair which Chrome borz owns
  server --host H --port P --token T [shutdown]
                                Start remote-accessible HTTP server
                                (--token required on non-loopback binds)
  service install|start|stop    Install/control Windows service mode
  profile list|show|add|set|rm  Manage named browser targets (managed browser,
                                CDP endpoint, or remote server) in profiles.json
  client setup <url> [--token T]
                                (deprecated) Writes the 'remote' profile
  update [--check] [--force]    Download latest release and replace self

Global Flags:
  --profile <name>              Select a profile; its transport comes from
                                ~/.borz/profiles.json (undeclared = managed)
  --remote                      (deprecated) Alias for --profile remote
  --tab <id>                    Target tab
  --json                        JSON output
  --jq <expr>                   Filter with jq expression (implies --json)
  --unwrap                      For 'eval'/site adapters: print result raw
  --since <seq|last_action>     Incremental query (network/console/errors)
  --pre-delay <ms>              Sleep before any action (replaces 'sleep N && borz ...')
  --post-delay <ms>             Sleep after a successful action

Refs & snapshots:
  Interaction commands (click, fill, ...) take a <ref> from a prior
  accessibility snapshot. Snapshots render elements as 'button [ref=5]';
  pass "5" (or "@5") as <ref>. Refs regenerate on every snapshot — always
  re-snapshot after navigation or DOM changes.

Tips:
  - Prefer '--wait-for <selector>' over '--post-delay <ms>' (or 'wait <ms>')
    for any SPA-driven DOM change. Works on open, click, fill, eval, and
    other actions.
  - Use '--pre-delay <ms>' / '--post-delay <ms>' to absorb the
    'sleep N && borz ...' shell pattern into a single daemon call. Both
    are global — they work on every command, and post-delay only fires
    on success.
  - Use 'eval --unwrap' to skip the {success,data,result,...} envelope.
  - Use '--since last_action' on network/console/errors for incremental reads.

Agents & automation:
  See skill.md / llm.txt in this repo for end-to-end guidance on driving
  borz from an agent (MCP, CLI, and HTTP modes).`)
}
