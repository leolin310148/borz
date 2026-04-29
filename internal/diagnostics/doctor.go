// Package diagnostics runs end-to-end health checks across the borz
// stack (binary, daemon, CDP, tabs) so CLI / MCP / REST surfaces can share
// one implementation.
package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/protocol"
)

// Check is one row of doctor output.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "warn", "fail"
	Detail string `json:"detail,omitempty"`
}

// Run executes the full set of client-side checks (binary → daemon → CDP →
// tabs) and returns the rows plus an ok flag (false when any check failed).
// Warn rows do not flip ok.
func Run(version string) ([]Check, bool) {
	checks := []Check{
		{Name: "Binary", Status: "ok", Detail: fmt.Sprintf("borz %s", version)},
		checkHomeDir(),
	}

	if remoteCfg, enabled, err := client.EnabledRemoteConfig(); err != nil {
		checks = append(checks, Check{Name: "Remote client", Status: "fail", Detail: err.Error()})
		return checks, false
	} else if enabled {
		checks = append(checks, Check{Name: "Remote client", Status: "ok", Detail: remoteCfg.URL + " enabled"})
		statusRaw, statusCheck := checkDaemonHTTP()
		statusCheck.Name = "Remote HTTP"
		checks = append(checks, statusCheck)
		if statusRaw != nil {
			checks = append(checks, checkCDPConnected(statusRaw))
			checks = append(checks, checkTabs())
		}
		return checks, checksOK(checks)
	}

	info, infoCheck := checkDaemonJSON()
	checks = append(checks, infoCheck)
	if info != nil {
		checks = append(checks, checkDaemonProcess(info))
		statusRaw, statusCheck := checkDaemonHTTP()
		checks = append(checks, statusCheck)
		if statusRaw != nil {
			checks = append(checks, checkCDPConnected(statusRaw))
			checks = append(checks, checkTabs())
		}
	}
	checks = append(checks, checkCDPDiscovery())

	return checks, checksOK(checks)
}

func checksOK(checks []Check) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return false
		}
	}
	return true
}

func checkHomeDir() Check {
	dir := config.HomeDir()
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: "Home dir", Status: "warn", Detail: dir + " (does not exist yet)"}
		}
		return Check{Name: "Home dir", Status: "fail", Detail: err.Error()}
	}
	if !st.IsDir() {
		return Check{Name: "Home dir", Status: "fail", Detail: dir + " is not a directory"}
	}
	return Check{Name: "Home dir", Status: "ok", Detail: dir}
}

func checkDaemonJSON() (*protocol.DaemonInfo, Check) {
	info, err := client.ReadDaemonJSON()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Check{Name: "Daemon JSON", Status: "warn", Detail: "daemon not started (no daemon.json)"}
		}
		return nil, Check{Name: "Daemon JSON", Status: "fail", Detail: err.Error()}
	}
	return info, Check{
		Name:   "Daemon JSON",
		Status: "ok",
		Detail: fmt.Sprintf("%s:%d (pid %d)", info.Host, info.Port, info.PID),
	}
}

func checkDaemonProcess(info *protocol.DaemonInfo) Check {
	if !client.IsProcessAlive(info.PID) {
		return Check{
			Name:   "Daemon process",
			Status: "fail",
			Detail: fmt.Sprintf("pid %d is gone — stale daemon.json (run 'borz daemon stop' or delete %s)", info.PID, config.DaemonJSONPath()),
		}
	}
	return Check{Name: "Daemon process", Status: "ok", Detail: fmt.Sprintf("pid %d alive", info.PID)}
}

func checkDaemonHTTP() (json.RawMessage, Check) {
	raw, err := client.GetDaemonStatus()
	if err != nil {
		return nil, Check{Name: "Daemon HTTP", Status: "fail", Detail: err.Error()}
	}
	return raw, Check{Name: "Daemon HTTP", Status: "ok", Detail: "/status responsive"}
}

func checkCDPConnected(raw json.RawMessage) Check {
	var st protocol.DaemonStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return Check{Name: "CDP connected", Status: "warn", Detail: "could not parse /status payload"}
	}
	if !st.CDPConnected {
		return Check{
			Name:   "CDP connected",
			Status: "fail",
			Detail: "daemon is up but not attached to Chrome — start the browser or check BORZ_CDP_URL",
		}
	}
	return Check{Name: "CDP connected", Status: "ok", Detail: "daemon attached to Chrome"}
}

func checkCDPDiscovery() Check {
	ep, err := client.DiscoverCDPPort()
	if err != nil {
		return Check{
			Name:   "CDP discovery",
			Status: "warn",
			Detail: "no Chrome reachable from this CLI (" + err.Error() + ")",
		}
	}
	return Check{
		Name:   "CDP discovery",
		Status: "ok",
		Detail: fmt.Sprintf("%s:%d reachable", ep.Host, ep.Port),
	}
}

func checkTabs() Check {
	req := &protocol.Request{ID: newID(), Action: protocol.ActionTabList}
	resp, err := client.SendCommand(req)
	if err != nil {
		return Check{Name: "Tabs", Status: "warn", Detail: err.Error()}
	}
	if !resp.Success {
		return Check{Name: "Tabs", Status: "warn", Detail: resp.Error}
	}
	if resp.Data == nil || len(resp.Data.Tabs) == 0 {
		return Check{Name: "Tabs", Status: "warn", Detail: "no open tabs (open one with 'borz open <url>')"}
	}
	return Check{Name: "Tabs", Status: "ok", Detail: fmt.Sprintf("%d open", len(resp.Data.Tabs))}
}

// RenderText writes a human-readable doctor report to w.
func RenderText(checks []Check) string {
	maxName := 0
	for _, c := range checks {
		if len(c.Name) > maxName {
			maxName = len(c.Name)
		}
	}
	var b strings.Builder
	failed, warned := 0, 0
	for _, c := range checks {
		marker := "[OK]"
		switch c.Status {
		case "fail":
			marker = "[FAIL]"
			failed++
		case "warn":
			marker = "[WARN]"
			warned++
		}
		pad := strings.Repeat(" ", maxName-len(c.Name))
		if c.Detail != "" {
			fmt.Fprintf(&b, "  %s%s  %-6s %s\n", c.Name, pad, marker, c.Detail)
		} else {
			fmt.Fprintf(&b, "  %s%s  %s\n", c.Name, pad, marker)
		}
	}
	b.WriteString("\n")
	switch {
	case failed > 0:
		fmt.Fprintf(&b, "%d failed, %d warning(s) — see above.\n", failed, warned)
	case warned > 0:
		fmt.Fprintf(&b, "All required checks passed (%d warning(s)).\n", warned)
	default:
		b.WriteString("All checks passed.\n")
	}
	return b.String()
}

// RenderJSON returns the {ok, checks} JSON envelope.
func RenderJSON(checks []Check) string {
	failed := false
	for _, c := range checks {
		if c.Status == "fail" {
			failed = true
			break
		}
	}
	out := struct {
		OK     bool    `json:"ok"`
		Checks []Check `json:"checks"`
	}{OK: !failed, Checks: checks}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
