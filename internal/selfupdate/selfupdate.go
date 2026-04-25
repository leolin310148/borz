// Package selfupdate downloads the latest GitHub release binary and replaces
// the running executable.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const DefaultRepo = "leolin310148/bb-browser-go"

type Release struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

type Options struct {
	CurrentVersion string
	Repo           string
	Force          bool
	CheckOnly      bool
	GOOS           string
	GOARCH         string
	HTTPClient     *http.Client
	Stderr         io.Writer

	// OnReplaced fires after the executable on disk has been swapped for the
	// new release. The currently-running daemon is from the old binary and
	// will silently ignore any new request fields, so callers use this hook
	// to shut it down — the next CLI invocation will respawn from the new
	// binary. Not called for --check or "already up to date" (without --force).
	OnReplaced func()

	// Test seams. Zero values use production defaults.
	ExecutablePath string // overrides os.Executable() resolution
	APIBaseURL     string // overrides https://api.github.com
}

func Run(ctx context.Context, opts Options) error {
	if opts.Repo == "" {
		opts.Repo = DefaultRepo
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	fmt.Fprintf(opts.Stderr, "Checking latest release from %s...\n", opts.Repo)
	rel, err := latestReleaseFrom(ctx, opts.APIBaseURL, opts.Repo, opts.HTTPClient)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(opts.CurrentVersion, "v")
	fmt.Fprintf(opts.Stderr, "Current: %s  Latest: %s\n", current, latest)

	if !opts.Force && !NewerVersion(current, latest) {
		fmt.Fprintln(opts.Stderr, "Already up to date.")
		return nil
	}

	if opts.CheckOnly {
		fmt.Fprintf(opts.Stderr, "Update available: %s -> %s\n", current, latest)
		return nil
	}

	assetName := AssetName(opts.GOOS, opts.GOARCH)
	asset := FindAsset(rel, assetName)
	if asset == nil {
		return fmt.Errorf("no release asset for %s/%s (expected %q)", opts.GOOS, opts.GOARCH, assetName)
	}

	checksums, err := fetchChecksums(ctx, rel, opts.HTTPClient)
	if err != nil {
		return fmt.Errorf("fetch checksums: %w", err)
	}
	expected, ok := checksums[asset.Name]
	if !ok {
		return fmt.Errorf("no checksum entry for %s", asset.Name)
	}

	exePath := opts.ExecutablePath
	if exePath == "" {
		p, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate current executable: %w", err)
		}
		exePath = p
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	fmt.Fprintf(opts.Stderr, "Downloading %s (%s)...\n", asset.Name, humanSize(asset.Size))
	tmpPath, err := downloadVerified(ctx, opts.HTTPClient, asset.DownloadURL, exePath, expected)
	if err != nil {
		return err
	}

	if err := replaceExecutable(exePath, tmpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replace executable: %w", err)
	}

	fmt.Fprintf(opts.Stderr, "Updated to %s\n", latest)
	if opts.OnReplaced != nil {
		opts.OnReplaced()
	}
	return nil
}

func LatestRelease(ctx context.Context, repo string, client *http.Client) (*Release, error) {
	return latestReleaseFrom(ctx, "", repo, client)
}

func latestReleaseFrom(ctx context.Context, baseURL, repo string, client *http.Client) (*Release, error) {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", baseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("bb-browser-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func FindAsset(rel *Release, name string) *Asset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// NewerVersion reports whether latest is strictly greater than current using
// semver-ish numeric component comparison. Non-numeric components compare as 0,
// so unknown or dev versions ("dev", "") are treated as older than any release.
func NewerVersion(current, latest string) bool {
	if current == latest {
		return false
	}
	c := splitVersion(current)
	l := splitVersion(latest)
	n := len(c)
	if len(l) > n {
		n = len(l)
	}
	for i := 0; i < n; i++ {
		var cv, lv int
		if i < len(c) {
			cv = c[i]
		}
		if i < len(l) {
			lv = l[i]
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

func splitVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if v == "" || v == "dev" {
		return []int{0}
	}
	// Strip any pre-release/build suffix like -rc1 or +meta.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out[i] = n
	}
	return out
}

// ParseChecksums parses a sha256sum-formatted file: "<hex>  <filename>" per
// line. Leading "*" on the filename (binary mode) is stripped.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum := fields[0]
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = sum
	}
	return out, nil
}

func fetchChecksums(ctx context.Context, rel *Release, client *http.Client) (map[string]string, error) {
	var asset *Asset
	for i := range rel.Assets {
		if rel.Assets[i].Name == "checksums.txt" {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		return nil, fmt.Errorf("release %s has no checksums.txt", rel.TagName)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download checksums.txt: http %d", resp.StatusCode)
	}
	return ParseChecksums(resp.Body)
}

func downloadVerified(ctx context.Context, client *http.Client, url, destPath, expectedSHA256 string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: http %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".bb-browser-update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectedSHA256) {
		os.Remove(tmpPath)
		return "", fmt.Errorf("checksum mismatch: got %s, expected %s", got, expectedSHA256)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
