package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/leolin310148/bb-browser-go/internal/client"
)

// handleCookies implements `bb-browser cookies <subcmd>`. The "all" subcommand
// returns cookies across every domain the extension can see — a capability CDP
// cannot provide, since CDP cookie domains are scoped to the active page.
func handleCookies(cmdArgs []string, jsonOutput bool) {
	sub := "all"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "all":
		raw, err := client.GetJSON("/v1/cookies/all", 15*time.Second)
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

// handleTabEvents implements `bb-browser tab events [--tail] [--since N]`.
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
