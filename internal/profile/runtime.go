package profile

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

// Runtime is what a profile has left on disk, independent of whether the name
// is declared in profiles.json. Undeclared names resolve to the managed
// transport, so every `--profile <name>` ever used has a runtime footprint here
// even though `profile list` cannot see it.
//
// Everything in Runtime comes from the filesystem. Deciding whether the daemon
// or browser is actually alive needs process and network probes, which belong
// to the caller — this package stays dependency-free and testable.
type Runtime struct {
	Name     string
	Declared bool

	RuntimeDir string
	BrowserDir string
	LogsDir    string

	// HasRuntimeDir is false for profiles that only ever left logs behind
	// (a transport that never launched a browser, or a purged profile).
	HasRuntimeDir bool
	HasBrowserDir bool
	HasLogsDir    bool

	// BrowserPort and BrowserID come from browser/browser.json: the exact
	// Chrome borz recorded as its own for this profile.
	BrowserPort int
	BrowserID   string

	// DaemonPID and DaemonPort come from daemon.json. A PID here does not mean
	// the process is alive; daemon.json outlives a crash.
	DaemonPID  int
	DaemonPort int

	// BrowserBytes measures browser/ only. For the default profile the runtime
	// directory is the borz home itself, so measuring it would sweep in every
	// other profile, the site adapters and the logs.
	BrowserBytes int64
	LogBytes     int64

	// LastUsed is the newest mtime among the profile's runtime and log files,
	// i.e. roughly when a command last touched this profile.
	LastUsed time.Time
}

// IsDefault reports whether this is the default profile, whose runtime state
// lives directly in the borz home rather than under profiles/.
func (r Runtime) IsDefault() bool {
	return r.Name == "default"
}

// ScanRuntime reports every profile borz knows about: declared in
// profiles.json, holding a runtime directory, or holding logs. The result is
// sorted by name with "default" first, since that is the one users get without
// asking for it.
func ScanRuntime() ([]Runtime, error) {
	registry, err := Load()
	if err != nil {
		return nil, err
	}

	names := map[string]bool{"default": true}
	for name := range registry.Profiles {
		names[Normalize(name)] = true
	}
	for _, dir := range []string{config.ProfilesDir(), filepath.Join(config.HomeDir(), "logs")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A missing profiles/ or logs/ directory just means nothing has
			// run yet; it is not an error worth failing the listing over.
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				names[entry.Name()] = true
			}
		}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		if name != "default" {
			ordered = append(ordered, name)
		}
	}
	sort.Strings(ordered)
	ordered = append([]string{"default"}, ordered...)

	out := make([]Runtime, 0, len(ordered))
	for _, name := range ordered {
		_, declared := registry.Profiles[name]
		out = append(out, inspectRuntime(name, declared))
	}
	return out, nil
}

// RuntimeFor inspects a single profile by name.
func RuntimeFor(name string) (Runtime, error) {
	registry, err := Load()
	if err != nil {
		return Runtime{}, err
	}
	name = Normalize(name)
	_, declared := registry.Profiles[name]
	return inspectRuntime(name, declared), nil
}

func inspectRuntime(name string, declared bool) Runtime {
	r := Runtime{
		Name:       name,
		Declared:   declared,
		RuntimeDir: config.RuntimeDirFor(name),
		BrowserDir: config.ManagedBrowserDirFor(name),
		LogsDir:    config.LogsDirFor(name),
	}
	// The default profile's runtime directory is the borz home, which always
	// exists once borz has run once. Reporting that as "this profile has
	// runtime state" would be meaningless, so key off browser/ instead.
	if r.IsDefault() {
		r.HasRuntimeDir = isDir(r.BrowserDir) || fileExists(config.DaemonJSONPathFor(name))
	} else {
		r.HasRuntimeDir = isDir(r.RuntimeDir)
	}
	r.HasBrowserDir = isDir(r.BrowserDir)
	r.HasLogsDir = isDir(r.LogsDir)

	r.BrowserPort, r.BrowserID = readBrowserState(config.ManagedStateFileFor(name))
	r.DaemonPID, r.DaemonPort = readDaemonState(config.DaemonJSONPathFor(name))

	r.BrowserBytes, _ = dirSize(r.BrowserDir)
	r.LogBytes, _ = dirSize(r.LogsDir)
	r.LastUsed = newestMTime(
		config.ManagedStateFileFor(name),
		config.DaemonJSONPathFor(name),
		filepath.Join(r.LogsDir, "client.jsonl"),
		filepath.Join(r.LogsDir, "daemon.jsonl"),
	)
	return r
}

func readBrowserState(path string) (port int, browserID string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	var state struct {
		Port      int    `json:"port"`
		BrowserID string `json:"browserId"`
	}
	if json.Unmarshal(data, &state) != nil {
		return 0, ""
	}
	return state.Port, state.BrowserID
}

func readDaemonState(path string) (pid int, port int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	var info struct {
		PID  int `json:"pid"`
		Port int `json:"port"`
	}
	if json.Unmarshal(data, &info) != nil {
		return 0, 0
	}
	return info.PID, info.Port
}

// dirSize sums the apparent size of every regular file under root. Symlinks
// are counted as their own (tiny) entry rather than followed, so a link out of
// the profile cannot inflate the total or walk into a cycle.
func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			// Keep walking past an unreadable subtree; a partial size is more
			// useful than none, and this runs on user-owned directories.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func newestMTime(paths ...string) time.Time {
	var newest time.Time
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	return newest
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
