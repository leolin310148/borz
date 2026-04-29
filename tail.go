package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/protocol"
)

// defaultTailInterval is how often the CLI re-polls the daemon for new
// network/console/error events. Picked to feel "live" without flooding the
// daemon — most CDP events arrive in clusters anyway.
const defaultTailInterval = 500 * time.Millisecond

// tailEmitter prints one polled response, returning how many items it printed
// (0 means "nothing new this tick"). It is responsible for human or JSONL
// formatting depending on jsonOutput.
type tailEmitter func(resp *protocol.Response, jsonOutput bool) int

// runTail polls req on a fixed interval, advancing req.Since via the response
// cursor each time so we never re-print events we've already shown. Stops on
// SIGINT or SIGTERM. jsonOutput switches output to JSONL (one object/line).
func runTail(req *protocol.Request, jsonOutput bool, interval time.Duration, emit tailEmitter) {
	if interval <= 0 {
		interval = defaultTailInterval
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Prime the cursor so the first poll only returns events newer than the
	// most recent action — otherwise the user gets a flood of historical
	// requests they didn't ask for.
	if req.Since == nil || req.Since == "" {
		req.Since = "last_action"
	}

	for {
		resp, err := client.SendCommand(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tail: %v\n", err)
		} else if !resp.Success {
			fmt.Fprintf(os.Stderr, "tail: %s\n", resp.Error)
		} else {
			emit(resp, jsonOutput)
			if resp.Data != nil && resp.Data.Cursor != nil {
				req.Since = *resp.Data.Cursor
			}
			req.ID = newID()
		}

		select {
		case <-sigCh:
			return
		case <-ticker.C:
		}
	}
}

// emitNetworkTail prints one line per newly-observed network request. The
// human format mirrors the one used by 'borz network requests' so a
// user piping output can grep without reformatting. JSON mode emits one
// JSON object per line (JSONL) for easy `jq -c` consumption.
func emitNetworkTail(resp *protocol.Response, jsonOutput bool) int {
	if resp.Data == nil {
		return 0
	}
	for _, nr := range resp.Data.NetworkRequests {
		if jsonOutput {
			b, _ := json.Marshal(nr)
			fmt.Println(string(b))
			continue
		}
		status := "-"
		if nr.Status != nil {
			status = strconv.Itoa(*nr.Status)
		}
		fmt.Printf("[%s] %s %s %s\n", status, nr.Method, nr.URL, nr.Type)
	}
	return len(resp.Data.NetworkRequests)
}

// parseTailInterval reads --interval <ms>. Returns defaultTailInterval when
// the flag is absent or invalid (we'd rather keep tailing than fatal on a
// typo'd interval).
func parseTailInterval(args []string) time.Duration {
	v := getArgValue(args, "--interval")
	if v == "" {
		return defaultTailInterval
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		fmt.Fprintf(os.Stderr, "tail: ignoring --interval %q (want a positive integer ms)\n", v)
		return defaultTailInterval
	}
	return time.Duration(n) * time.Millisecond
}
