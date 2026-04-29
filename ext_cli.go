package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leolin310148/borz/internal/client"
)

type extBookmark struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	URL      string        `json:"url"`
	Children []extBookmark `json:"children"`
}

type extHistoryItem struct {
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	LastVisitTime float64 `json:"lastVisitTime"`
	VisitCount    int     `json:"visitCount"`
}

type extDownloadItem struct {
	ID               int    `json:"id"`
	URL              string `json:"url"`
	Filename         string `json:"filename"`
	State            string `json:"state"`
	BytesReceived    int64  `json:"bytesReceived"`
	TotalBytes       int64  `json:"totalBytes"`
	Error            string `json:"error"`
	Exists           bool   `json:"exists"`
	Paused           bool   `json:"paused"`
	Danger           string `json:"danger"`
	EstimatedEndTime string `json:"estimatedEndTime"`
}

type extWindow struct {
	ID      int    `json:"id"`
	Type    string `json:"type"`
	State   string `json:"state"`
	Focused bool   `json:"focused"`
	Top     int    `json:"top"`
	Left    int    `json:"left"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Tabs    []struct {
		ID     int    `json:"id"`
		Index  int    `json:"index"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		Active bool   `json:"active"`
	} `json:"tabs"`
}

// handleCookies implements `borz cookies <subcmd>`. The "all" subcommand
// returns cookies across every domain the extension can see — a capability CDP
// cannot provide, since CDP cookie domains are scoped to the active page.
func handleCookies(cmdArgs []string, jsonOutput bool) {
	sub := "all"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "all":
		path := "/v1/cookies/all"
		if len(cmdArgs) > 1 {
			q := url.Values{}
			q.Set("domain", cmdArgs[1])
			path += "?" + q.Encode()
		}
		raw, err := client.GetJSON(path, 15*time.Second)
		if err != nil {
			fatal(err.Error())
		}
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var cookies []extCookie
		if err := json.Unmarshal(raw, &cookies); err != nil {
			fmt.Println(string(raw))
			return
		}
		fmt.Printf("Cookies (%d total):\n", len(cookies))
		for _, c := range cookies {
			scope := c.Domain
			if c.Path != "" && c.Path != "/" {
				scope = scope + c.Path
			}
			fmt.Printf("  %s  %s = %s\n", scope, c.Name, truncate(c.Value, 64))
		}
	default:
		fatal("Unknown cookies subcommand: " + sub + " (try: all)")
	}
}

type extCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// handleTabEvents implements `borz tab events [--tail] [--since N]`.
// Without --tail, prints all currently-buffered events and exits. With --tail,
// polls the daemon, streaming new events until interrupted.
func handleTabEvents(rawArgs []string, jsonOutput bool) {
	tail := hasFlag(rawArgs, "--tail")
	since := uint64(0)
	if v := getArgValue(rawArgs, "--since"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			since = n
		}
	}

	if !tail {
		evs, _, err := fetchTabEvents(since)
		if err != nil {
			fatal(err.Error())
		}
		emitTabEvents(evs, jsonOutput)
		return
	}

	// Prime cursor so --tail only shows new events.
	_, latest, err := fetchTabEvents(^uint64(0))
	if err != nil {
		fatal(err.Error())
	}
	cursor := latest

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(parseTailInterval(rawArgs))
	defer ticker.Stop()

	for {
		evs, latest, err := fetchTabEvents(cursor)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tail: %v\n", err)
		} else {
			emitTabEvents(evs, jsonOutput)
			if latest > cursor {
				cursor = latest
			}
		}
		select {
		case <-sigCh:
			return
		case <-ticker.C:
		}
	}
}

type tabEvent struct {
	Seq  uint64          `json:"seq"`
	Time time.Time       `json:"time"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

type tabEventsResponse struct {
	Events    []tabEvent `json:"events"`
	LatestSeq uint64     `json:"latest_seq"`
	Connected bool       `json:"connected"`
}

func fetchTabEvents(since uint64) ([]tabEvent, uint64, error) {
	q := url.Values{}
	if since > 0 {
		q.Set("since", strconv.FormatUint(since, 10))
	}
	path := "/v1/tabs/events"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	raw, err := client.GetJSON(path, 5*time.Second)
	if err != nil {
		return nil, 0, err
	}
	var resp tabEventsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Events, resp.LatestSeq, nil
}

func emitTabEvents(evs []tabEvent, jsonOutput bool) {
	for _, ev := range evs {
		if jsonOutput {
			b, _ := json.Marshal(ev)
			fmt.Println(string(b))
			continue
		}
		fmt.Printf("[%d] %s %s %s\n", ev.Seq, ev.Time.Format("15:04:05.000"), ev.Name, string(ev.Data))
	}
}

func handleBookmarks(cmdArgs []string, jsonOutput bool, rawArgs []string) {
	sub := "tree"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "tree":
		raw := extGetJSON("/v1/bookmarks/tree")
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var roots []extBookmark
		if err := json.Unmarshal(raw, &roots); err != nil {
			fmt.Println(string(raw))
			return
		}
		for _, root := range roots {
			printBookmarkTree(root, 0)
		}
	case "search":
		query := strings.Join(cmdArgs[1:], " ")
		q := url.Values{}
		q.Set("q", query)
		raw := extGetJSON("/v1/bookmarks/search?" + q.Encode())
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var items []extBookmark
		_ = json.Unmarshal(raw, &items)
		fmt.Printf("Bookmarks (%d results):\n", len(items))
		for _, b := range items {
			fmt.Printf("  [%s] %s %s\n", b.ID, nonEmpty(b.Title, "(untitled)"), b.URL)
		}
	case "create":
		if len(cmdArgs) < 3 {
			fatal("Usage: borz bookmarks create <url> <title> [--parent <id>]")
		}
		body := map[string]any{"url": cmdArgs[1], "title": strings.Join(cmdArgs[2:], " ")}
		if parent := getArgValue(rawArgs, "--parent"); parent != "" {
			body["parentId"] = parent
		}
		raw := extPostJSON("/v1/bookmarks/create", body)
		emitRawOrMessage(raw, jsonOutput, "Bookmark created")
	case "update":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz bookmarks update <id> [--title <title>] [--url <url>]")
		}
		changes := map[string]any{}
		if title := getArgValue(rawArgs, "--title"); title != "" {
			changes["title"] = title
		}
		if u := getArgValue(rawArgs, "--url"); u != "" {
			changes["url"] = u
		}
		if len(changes) == 0 {
			fatal("bookmarks update requires --title and/or --url")
		}
		raw := extPostJSON("/v1/bookmarks/update", map[string]any{"id": cmdArgs[1], "changes": changes})
		emitRawOrMessage(raw, jsonOutput, "Bookmark updated")
	case "remove":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz bookmarks remove <id> [--recursive]")
		}
		raw := extPostJSON("/v1/bookmarks/remove", map[string]any{"id": cmdArgs[1], "recursive": hasFlag(rawArgs, "--recursive")})
		emitRawOrMessage(raw, jsonOutput, "Bookmark removed")
	default:
		fatal("Unknown bookmarks subcommand: " + sub + " (try: tree, search, create, update, remove)")
	}
}

func handleBrowserHistory(cmdArgs []string, jsonOutput bool, rawArgs []string) {
	sub := "search"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "search":
		query := strings.Join(cmdArgs[1:], " ")
		q := url.Values{}
		q.Set("q", query)
		if limit := getArgValue(rawArgs, "--limit"); limit != "" {
			q.Set("maxResults", limit)
		}
		raw := extGetJSON("/v1/browser-history/search?" + q.Encode())
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var items []extHistoryItem
		_ = json.Unmarshal(raw, &items)
		fmt.Printf("Browser history (%d results):\n", len(items))
		for _, item := range items {
			fmt.Printf("  %s  %s (%d visits)\n", item.URL, nonEmpty(item.Title, "(untitled)"), item.VisitCount)
		}
	case "delete-url":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz browser-history delete-url <url>")
		}
		raw := extPostJSON("/v1/browser-history/delete-url", map[string]any{"url": cmdArgs[1]})
		emitRawOrMessage(raw, jsonOutput, "History URL deleted")
	default:
		fatal("Unknown browser-history subcommand: " + sub + " (try: search, delete-url)")
	}
}

func handleDownloads(cmdArgs []string, jsonOutput bool, rawArgs []string) {
	sub := "list"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "list", "search":
		q := url.Values{}
		if sub == "search" && len(cmdArgs) > 1 {
			q.Set("q", strings.Join(cmdArgs[1:], " "))
		}
		if limit := getArgValue(rawArgs, "--limit"); limit != "" {
			q.Set("limit", limit)
		}
		if state := getArgValue(rawArgs, "--state"); state != "" {
			q.Set("state", state)
		}
		path := "/v1/downloads/search"
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		raw := extGetJSON(path)
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var items []extDownloadItem
		_ = json.Unmarshal(raw, &items)
		fmt.Printf("Downloads (%d results):\n", len(items))
		for _, item := range items {
			fmt.Printf("  [%d] %s %s %d/%d %s\n", item.ID, item.State, item.Filename, item.BytesReceived, item.TotalBytes, item.URL)
		}
	case "start":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz downloads start <url> [--filename <path>] [--save-as]")
		}
		body := map[string]any{"url": cmdArgs[1], "saveAs": hasFlag(rawArgs, "--save-as")}
		if filename := getArgValue(rawArgs, "--filename"); filename != "" {
			body["filename"] = filename
		}
		raw := extPostJSON("/v1/downloads/download", body)
		emitRawOrMessage(raw, jsonOutput, "Download started")
	case "erase":
		body := map[string]any{}
		if id := getArgValue(rawArgs, "--id"); id != "" {
			if n, err := strconv.Atoi(id); err == nil {
				body["id"] = n
			}
		} else if len(cmdArgs) > 1 {
			body["q"] = strings.Join(cmdArgs[1:], " ")
		}
		raw := extPostJSON("/v1/downloads/erase", body)
		emitRawOrMessage(raw, jsonOutput, "Download records erased")
	case "cancel", "pause", "resume", "show":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz downloads " + sub + " <id>")
		}
		id, err := strconv.Atoi(cmdArgs[1])
		if err != nil {
			fatal("download id must be a number")
		}
		raw := extPostJSON("/v1/downloads/"+sub, map[string]any{"id": id})
		emitRawOrMessage(raw, jsonOutput, "Download "+sub+" requested")
	case "show-folder":
		raw := extPostJSON("/v1/downloads/show-default-folder", map[string]any{})
		emitRawOrMessage(raw, jsonOutput, "Download folder shown")
	default:
		fatal("Unknown downloads subcommand: " + sub + " (try: list, search, start, erase, cancel, pause, resume, show, show-folder)")
	}
}

func handleWindows(cmdArgs []string, jsonOutput bool, rawArgs []string) {
	sub := "list"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "list":
		raw := extGetJSON("/v1/windows")
		if jsonOutput {
			fmt.Println(string(raw))
			return
		}
		var windows []extWindow
		_ = json.Unmarshal(raw, &windows)
		fmt.Printf("Windows (%d total):\n", len(windows))
		for _, win := range windows {
			mark := " "
			if win.Focused {
				mark = "*"
			}
			fmt.Printf("%s [%d] %s %s %dx%d+%d+%d tabs=%d\n", mark, win.ID, win.Type, win.State, win.Width, win.Height, win.Left, win.Top, len(win.Tabs))
		}
	case "focus":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz window focus <id>")
		}
		id := mustAtoi(cmdArgs[1], "window id")
		raw := extPostJSON("/v1/windows/update", map[string]any{"id": id, "updateInfo": map[string]any{"focused": true}})
		emitRawOrMessage(raw, jsonOutput, "Window focused")
	case "close":
		if len(cmdArgs) < 2 {
			fatal("Usage: borz window close <id>")
		}
		id := mustAtoi(cmdArgs[1], "window id")
		raw := extPostJSON("/v1/windows/close", map[string]any{"id": id})
		emitRawOrMessage(raw, jsonOutput, "Window closed")
	case "new":
		body := map[string]any{"focused": hasFlag(rawArgs, "--focused")}
		if len(cmdArgs) > 1 {
			body["url"] = cmdArgs[1]
		}
		raw := extPostJSON("/v1/windows/create", body)
		emitRawOrMessage(raw, jsonOutput, "Window created")
	default:
		fatal("Unknown window subcommand: " + sub + " (try: list, new, focus, close)")
	}
}

func extGetJSON(path string) json.RawMessage {
	raw, err := client.GetJSON(path, 15*time.Second)
	if err != nil {
		fatal(err.Error())
	}
	return raw
}

func extPostJSON(path string, body any) json.RawMessage {
	raw, err := client.PostJSON(path, body, 15*time.Second)
	if err != nil {
		fatal(err.Error())
	}
	return raw
}

func emitRawOrMessage(raw json.RawMessage, jsonOutput bool, msg string) {
	if jsonOutput {
		fmt.Println(string(raw))
		return
	}
	fmt.Println(msg)
}

func printBookmarkTree(b extBookmark, depth int) {
	indent := strings.Repeat("  ", depth)
	label := nonEmpty(b.Title, "(root)")
	if b.URL != "" {
		fmt.Printf("%s- [%s] %s %s\n", indent, b.ID, label, b.URL)
	} else {
		fmt.Printf("%s+ [%s] %s\n", indent, b.ID, label)
	}
	for _, child := range b.Children {
		printBookmarkTree(child, depth+1)
	}
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func mustAtoi(v, name string) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		fatal(name + " must be a number")
	}
	return n
}
