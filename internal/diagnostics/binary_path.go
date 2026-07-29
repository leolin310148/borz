package diagnostics

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BinaryCandidate describes one distinct borz executable found through PATH.
// Doctor hashes and reads build metadata from candidates; it never executes
// them.
type BinaryCandidate struct {
	Path           string `json:"path"`
	ResolvedPath   string `json:"resolvedPath,omitempty"`
	Version        string `json:"version,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	Current        bool   `json:"current"`
	MatchesCurrent bool   `json:"matchesCurrent"`
	Error          string `json:"error,omitempty"`
}

var (
	diagnosticExecutable = os.Executable
	diagnosticPathEnv    = func() string { return os.Getenv("PATH") }
	diagnosticVersionAt  = embeddedBinaryVersion
)

func checkBinaryPath(currentVersion string) Check {
	executable, err := diagnosticExecutable()
	if err != nil {
		return Check{Name: "Binary PATH", Status: StatusWarn, Detail: "cannot resolve current executable: " + err.Error()}
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return Check{Name: "Binary PATH", Status: StatusWarn, Detail: "cannot normalize current executable: " + err.Error()}
	}
	if !isBorzBinaryName(filepath.Base(executable)) {
		return Check{Name: "Binary PATH", Status: StatusOK, Detail: "PATH scan skipped for test executable " + executable}
	}

	currentResolved := resolveBinaryPath(executable)
	currentHash, currentHashErr := hashBinary(executable)
	candidates := findBorzOnPath(currentResolved, currentHash, currentVersion)

	currentFound := false
	for _, candidate := range candidates {
		if candidate.Current {
			currentFound = true
			break
		}
	}
	if !currentFound {
		candidates = append([]BinaryCandidate{binaryCandidate(executable, currentResolved, currentResolved, currentHash, currentVersion)}, candidates...)
	}

	if currentHashErr != nil {
		return Check{
			Name:     "Binary PATH",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("cannot fingerprint active executable %s: %v", executable, currentHashErr),
			Binaries: candidates,
		}
	}
	if !currentFound {
		return Check{
			Name:     "Binary PATH",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("active executable %s is not reachable through PATH; agents may run a different borz", executable),
			Binaries: candidates,
		}
	}
	if len(candidates) == 1 {
		return Check{
			Name:     "Binary PATH",
			Status:   StatusOK,
			Detail:   fmt.Sprintf("one canonical borz on PATH: %s (%s)", candidates[0].Path, displayBinaryVersion(candidates[0].Version)),
			Binaries: candidates,
		}
	}

	var conflicts []string
	allMatch := true
	for _, candidate := range candidates {
		if candidate.Current {
			continue
		}
		if !candidate.MatchesCurrent || candidate.Error != "" {
			allMatch = false
			conflicts = append(conflicts, fmt.Sprintf("%s (%s)", candidate.Path, displayBinaryVersion(candidate.Version)))
		}
	}
	if allMatch {
		return Check{
			Name:     "Binary PATH",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("%d separate borz copies are on PATH and currently match; replace duplicates with symlinks to %s to prevent future version drift", len(candidates), executable),
			Binaries: candidates,
		}
	}
	return Check{
		Name:     "Binary PATH",
		Status:   StatusWarn,
		Detail:   fmt.Sprintf("active %s (%s) conflicts with %s; update/remove stale installs or replace them with symlinks to the active executable", executable, currentVersion, strings.Join(conflicts, ", ")),
		Binaries: candidates,
	}
}

func findBorzOnPath(currentResolved, currentHash, currentVersion string) []BinaryCandidate {
	seen := make(map[string]bool)
	var candidates []BinaryCandidate
	for _, dir := range filepath.SplitList(diagnosticPathEnv()) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, name := range borzBinaryNames() {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || !isExecutableFile(info) {
				continue
			}
			absolute, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			resolved := resolveBinaryPath(absolute)
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			candidates = append(candidates, binaryCandidate(absolute, resolved, currentResolved, currentHash, currentVersion))
		}
	}
	return candidates
}

func binaryCandidate(path, resolved, currentResolved, currentHash, currentVersion string) BinaryCandidate {
	hash, err := hashBinary(path)
	candidate := BinaryCandidate{
		Path:           path,
		ResolvedPath:   resolved,
		SHA256:         hash,
		Current:        resolved == currentResolved,
		MatchesCurrent: hash != "" && currentHash != "" && hash == currentHash,
	}
	if candidate.Current {
		candidate.Version = currentVersion
	} else {
		candidate.Version = diagnosticVersionAt(path)
	}
	if err != nil {
		candidate.Error = err.Error()
	}
	return candidate
}

func hashBinary(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func embeddedBinaryVersion(path string) string {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "-ldflags" {
			if version := versionFromLDFlags(setting.Value); version != "" {
				return version
			}
		}
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return ""
}

func versionFromLDFlags(flags string) string {
	fields := strings.Fields(flags)
	for index, field := range fields {
		value := ""
		switch {
		case field == "-X" && index+1 < len(fields):
			value = fields[index+1]
		case strings.HasPrefix(field, "-X="):
			value = strings.TrimPrefix(field, "-X=")
		}
		if strings.HasPrefix(value, "main.version=") {
			return strings.TrimPrefix(value, "main.version=")
		}
	}
	return ""
}

func resolveBinaryPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}

func displayBinaryVersion(version string) string {
	if version == "" {
		return "version unknown"
	}
	return version
}

func isBorzBinaryName(name string) bool {
	name = strings.ToLower(name)
	return name == "borz" || name == "borz.exe"
}

func borzBinaryNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"borz.exe", "borz"}
	}
	return []string{"borz"}
}

func isExecutableFile(info os.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || info.Mode().Perm()&0o111 != 0
}
