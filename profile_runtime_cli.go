package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

// Runtime status names, ordered from most to least alive. statusBrowserOnly is
// the interesting one: a managed Chrome is running but borz has no daemon
// record for it. That is normal for a moment after a daemon exits (the next
// command re-attaches), and a genuine leak once the profile is out of use.
const (
	statusLive         = "live"
	statusDaemonOnly   = "daemon only"
	statusBrowserOnly  = "browser only"
	statusIdle         = "idle"
	statusLogsOnly     = "logs only"
	statusNoLocalState = "-"
)

// livenessProbeTimeout bounds each managed-browser check. The listing probes
// every profile, so a dead port must fail fast rather than stall the table.
const livenessProbeTimeout = 500 * time.Millisecond

// runtimeView is one row of `profile list --all`: what is on disk, plus
// whether it is actually running right now.
type runtimeView struct {
	borzprofile.Runtime
	Transport    string
	DaemonAlive  bool
	BrowserAlive bool
	Status       string
}

func inspectRuntimeView(r borzprofile.Runtime, registry *borzprofile.File) runtimeView {
	view := runtimeView{Runtime: r, Transport: string(borzprofile.TransportManaged)}
	if entry, ok := registry.Profiles[r.Name]; ok && strings.TrimSpace(entry.Transport) != "" {
		view.Transport = entry.Transport
	}
	view.DaemonAlive = r.DaemonPID > 0 && client.IsProcessAlive(r.DaemonPID)
	if r.BrowserPort > 0 {
		view.BrowserAlive = client.CheckCDPEndpoint("127.0.0.1", r.BrowserPort, livenessProbeTimeout) == nil
	}
	view.Status = runtimeStatus(view)
	return view
}

func runtimeStatus(view runtimeView) string {
	switch {
	case view.DaemonAlive && view.BrowserAlive:
		return statusLive
	case view.DaemonAlive:
		return statusDaemonOnly
	case view.BrowserAlive:
		return statusBrowserOnly
	case view.HasRuntimeDir:
		return statusIdle
	case view.HasLogsDir:
		return statusLogsOnly
	default:
		return statusNoLocalState
	}
}

func handleProfileListAll(jsonOutput bool) {
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	runtimes, err := borzprofile.ScanRuntime()
	if err != nil {
		fatal(err.Error())
	}
	views := make([]runtimeView, 0, len(runtimes))
	for _, r := range runtimes {
		views = append(views, inspectRuntimeView(r, registry))
	}

	if jsonOutput {
		entries := make([]map[string]interface{}, 0, len(views))
		for _, v := range views {
			entries = append(entries, runtimePayload(v))
		}
		printJSON(map[string]interface{}{
			"path":     config.ProfilesJSONPath(),
			"profiles": entries,
		})
		return
	}
	printRuntimeTable(views)
}

func runtimePayload(v runtimeView) map[string]interface{} {
	payload := map[string]interface{}{
		"name":         v.Name,
		"declared":     v.Declared,
		"transport":    v.Transport,
		"status":       v.Status,
		"daemonAlive":  v.DaemonAlive,
		"browserAlive": v.BrowserAlive,
		"runtimeDir":   v.RuntimeDir,
		"browserBytes": v.BrowserBytes,
		"logBytes":     v.LogBytes,
	}
	if v.DaemonPID > 0 {
		payload["daemonPid"] = v.DaemonPID
	}
	if v.BrowserPort > 0 {
		payload["browserPort"] = v.BrowserPort
	}
	if !v.LastUsed.IsZero() {
		payload["lastUsed"] = v.LastUsed.Format(time.RFC3339)
	}
	return payload
}

func printRuntimeTable(views []runtimeView) {
	nameWidth, transportWidth, statusWidth := len("NAME"), len("TRANSPORT"), len("STATUS")
	for _, v := range views {
		nameWidth = max(nameWidth, len(v.Name))
		transportWidth = max(transportWidth, len(v.Transport))
		statusWidth = max(statusWidth, len(v.Status))
	}
	fmt.Printf("%-*s  %-*s  %-8s  %-*s  %8s  %s\n",
		nameWidth, "NAME", transportWidth, "TRANSPORT", "DECLARED", statusWidth, "STATUS", "SIZE", "LAST USED")

	var totalBytes int64
	browserOnly, undeclared := 0, 0
	for _, v := range views {
		declared := "no"
		switch {
		case v.Declared:
			declared = "yes"
		case v.IsDefault():
			// "default" is a built-in name, not a stray one somebody typed
			// once. Listing it as undeclared would put the profile everyone
			// uses at the top of a warning about profiles nobody meant to make.
			declared = "built-in"
		default:
			undeclared++
		}
		if v.Status == statusBrowserOnly {
			browserOnly++
		}
		size := v.BrowserBytes + v.LogBytes
		totalBytes += size
		fmt.Printf("%-*s  %-*s  %-8s  %-*s  %8s  %s\n",
			nameWidth, v.Name, transportWidth, v.Transport, declared,
			statusWidth, v.Status, humanBytes(size), humanAge(v.LastUsed))
	}

	fmt.Printf("\nTotal on disk: %s (managed browser data + logs)\n", humanBytes(totalBytes))
	if undeclared > 0 {
		fmt.Printf("%d profile(s) are undeclared: any '--profile <name>' creates runtime state, whether or not\n", undeclared)
		fmt.Println("the name is in profiles.json. They are invisible to plain 'profile list'.")
	}
	if browserOnly > 0 {
		fmt.Printf("%d managed browser(s) are running with no daemon record ('%s'). That is normal\n", browserOnly, statusBrowserOnly)
		fmt.Println("right after a daemon exits — the next command for that profile re-attaches. Check")
		fmt.Println("LAST USED: a browser held by a profile you stopped using is just leaked memory.")
	}
	if undeclared > 0 || browserOnly > 0 {
		fmt.Println("Reclaim one with 'borz profile purge <name>' (previews first; --force to act).")
	}
}

// handleProfilePurge reclaims everything one profile left behind: its daemon,
// the Chrome that daemon owns, and its runtime directory. It previews by
// default because the browser data directory holds real sessions and cookies.
func handleProfilePurge(name string, rawArgs []string, jsonOutput bool) {
	if err := config.ValidateProfileName(name); err != nil {
		fatal(err.Error())
	}
	name = borzprofile.Normalize(name)
	// The guard is on the resolved path, not the spelling: config folds case
	// when mapping a name to a runtime directory, so "DEFAULT" resolves to the
	// borz home just as "default" does. Anything that lands on the home
	// directory would take profiles.json, the site adapters and every other
	// profile with it.
	if strings.EqualFold(name, borzprofile.DefaultName) || config.RuntimeDirFor(name) == config.HomeDir() {
		fatal("refusing to purge the default profile: its runtime directory is the borz home itself " +
			"(" + config.HomeDir() + "), which also holds profiles.json, site adapters and every other profile.\n" +
			"To reset only its browser, stop the daemon and remove " + config.ManagedBrowserDirFor("default") + " by hand.")
	}
	if strings.EqualFold(name, borzprofile.Normalize(config.Profile())) {
		fatal(fmt.Sprintf("refusing to purge %q while it is the profile this command is running as; "+
			"re-run without '--profile %s'", name, name))
	}

	runtime, err := borzprofile.RuntimeFor(name)
	if err != nil {
		fatal(err.Error())
	}
	registry, err := borzprofile.Load()
	if err != nil {
		fatal(err.Error())
	}
	view := inspectRuntimeView(runtime, registry)
	withLogs := hasFlag(rawArgs, "--logs")
	force := hasFlag(rawArgs, "--force")

	targets := purgeTargets(view, withLogs)
	if len(targets) == 0 {
		if jsonOutput {
			printJSON(map[string]interface{}{"name": name, "purged": false, "reason": "nothing to purge"})
			return
		}
		fmt.Printf("Profile %q has no local runtime state to purge.\n", name)
		if view.Declared {
			fmt.Printf("It is still declared in %s; remove the declaration with 'borz profile rm %s'.\n",
				config.ProfilesJSONPath(), name)
		}
		return
	}

	if !force {
		if jsonOutput {
			printJSON(map[string]interface{}{
				"name": name, "purged": false, "dryRun": true,
				"wouldRemove": targets, "status": view.Status,
				"bytes": purgeBytes(view, withLogs),
			})
			return
		}
		fmt.Printf("Would purge profile %q (%s):\n", name, view.Status)
		for _, t := range targets {
			fmt.Printf("  - %s\n", t)
		}
		fmt.Printf("Frees roughly %s.\n", humanBytes(purgeBytes(view, withLogs)))
		if !withLogs && view.HasLogsDir {
			fmt.Printf("Logs in %s are kept; add --logs to remove them too.\n", view.LogsDir)
		}
		fmt.Println("\nNothing was deleted. Re-run with --force to purge.")
		return
	}

	steps := performPurge(view, withLogs)
	if jsonOutput {
		printJSON(map[string]interface{}{"name": name, "purged": true, "steps": steps})
		return
	}
	fmt.Printf("Purged profile %q:\n", name)
	for _, step := range steps {
		fmt.Printf("  %s\n", step)
	}
	if view.Declared {
		fmt.Printf("\nStill declared in %s; 'borz profile rm %s' removes the declaration too.\n",
			config.ProfilesJSONPath(), name)
	}
}

// purgeTargets lists, in human terms, everything a purge would act on.
func purgeTargets(view runtimeView, withLogs bool) []string {
	var targets []string
	if view.DaemonAlive {
		targets = append(targets, fmt.Sprintf("stop daemon (pid %d)", view.DaemonPID))
	}
	if view.BrowserAlive {
		targets = append(targets, fmt.Sprintf("close managed browser on port %d", view.BrowserPort))
	}
	if view.HasRuntimeDir {
		targets = append(targets, fmt.Sprintf("delete %s (%s)", view.RuntimeDir, humanBytes(view.BrowserBytes)))
	}
	if withLogs && view.HasLogsDir {
		targets = append(targets, fmt.Sprintf("delete %s (%s)", view.LogsDir, humanBytes(view.LogBytes)))
	}
	return targets
}

func purgeBytes(view runtimeView, withLogs bool) int64 {
	total := view.BrowserBytes
	if withLogs {
		total += view.LogBytes
	}
	return total
}

// performPurge executes the plan in dependency order: the daemon first (a
// daemon started with --close-owned-browser closes its Chrome on the way out),
// then any browser that outlived it, and only then the files.
func performPurge(view runtimeView, withLogs bool) []string {
	var steps []string

	if view.DaemonAlive {
		info := client.ReadDaemonJSONFor(view.Name)
		err := client.StopDaemonAt(info)
		if err == nil && client.WaitForProcessExit(view.DaemonPID, 5*time.Second) {
			steps = append(steps, fmt.Sprintf("stopped daemon (pid %d)", view.DaemonPID))
		} else {
			// A wedged daemon must not block the purge: the point of this
			// command is to reclaim state that is already misbehaving.
			if proc, ferr := os.FindProcess(view.DaemonPID); ferr == nil {
				_ = proc.Kill()
				client.WaitForProcessExit(view.DaemonPID, 2*time.Second)
			}
			steps = append(steps, fmt.Sprintf("killed daemon (pid %d, clean shutdown failed)", view.DaemonPID))
		}
		// Stopping the daemon may have taken the browser with it.
		view.BrowserAlive = view.BrowserPort > 0 &&
			client.CheckCDPEndpoint("127.0.0.1", view.BrowserPort, livenessProbeTimeout) == nil
	}

	if view.BrowserAlive {
		if err := closeManagedBrowser(view); err != nil {
			steps = append(steps, fmt.Sprintf("could not close managed browser on port %d: %v", view.BrowserPort, err))
		} else {
			steps = append(steps, fmt.Sprintf("closed managed browser on port %d", view.BrowserPort))
		}
	}

	if view.HasRuntimeDir {
		if err := os.RemoveAll(view.RuntimeDir); err != nil {
			steps = append(steps, fmt.Sprintf("could not delete %s: %v", view.RuntimeDir, err))
		} else {
			steps = append(steps, fmt.Sprintf("deleted %s (%s)", view.RuntimeDir, humanBytes(view.BrowserBytes)))
		}
	}
	if withLogs && view.HasLogsDir {
		if err := os.RemoveAll(view.LogsDir); err != nil {
			steps = append(steps, fmt.Sprintf("could not delete %s: %v", view.LogsDir, err))
		} else {
			steps = append(steps, fmt.Sprintf("deleted %s (%s)", view.LogsDir, humanBytes(view.LogBytes)))
		}
	}
	return steps
}

// closeManagedBrowser asks Chrome to quit over CDP, but only after confirming
// it is the exact instance borz recorded for this profile. Without the identity
// check a stale port could point at the user's own browser.
var closeManagedBrowser = func(view runtimeView) error {
	if view.BrowserID != "" {
		liveID, err := client.ReadCDPBrowserID("127.0.0.1", view.BrowserPort, livenessProbeTimeout)
		if err != nil {
			return err
		}
		if liveID != view.BrowserID {
			return fmt.Errorf("the browser on port %d is not the one borz recorded for this profile; "+
				"leaving it alone", view.BrowserPort)
		}
	}
	conn := daemon.NewCdpConnection("127.0.0.1", view.BrowserPort, daemon.NewTabStateManager())
	if err := conn.Connect(); err != nil {
		return err
	}
	defer conn.Disconnect()
	_, err := conn.BrowserCommandWithTimeout("Browser.close", nil, 3*time.Second)
	return err
}

// humanBytes renders a byte count the way `du -h` would.
func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"K", "M", "G", "T"}
	value := float64(n) / 1024
	for _, unit := range units {
		if value < 1024 || unit == "T" {
			if value < 10 {
				return fmt.Sprintf("%.1f%s", value, unit)
			}
			return fmt.Sprintf("%.0f%s", value, unit)
		}
		value /= 1024
	}
	return fmt.Sprintf("%dB", n)
}

// humanAge renders how long ago a profile was last touched.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
