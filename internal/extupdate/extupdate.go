// Package extupdate downloads the borz Chrome extension zip from the
// latest GitHub release and extracts it into a local directory the user can
// load via chrome://extensions → "Load unpacked".
package extupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/selfupdate"
)

const ExtensionAssetName = "borz-extension.zip"

type Options struct {
	Repo       string
	DestDir    string
	HTTPClient *http.Client
	Stderr     io.Writer

	// Test seams.
	APIBaseURL string
}

type Result struct {
	Tag     string
	DestDir string
}

func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Repo == "" {
		opts.Repo = selfupdate.DefaultRepo
	}
	if opts.DestDir == "" {
		return nil, errors.New("DestDir is required")
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	fmt.Fprintf(opts.Stderr, "Checking latest release from %s...\n", opts.Repo)
	rel, err := latestRelease(ctx, opts.APIBaseURL, opts.Repo, opts.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}

	asset := selfupdate.FindAsset(rel, ExtensionAssetName)
	if asset == nil {
		return nil, fmt.Errorf("release %s has no %s asset", rel.TagName, ExtensionAssetName)
	}

	checksums, err := fetchChecksums(ctx, rel, opts.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("fetch checksums: %w", err)
	}
	expected, ok := checksums[ExtensionAssetName]
	if !ok {
		return nil, fmt.Errorf("no checksum entry for %s", ExtensionAssetName)
	}

	fmt.Fprintf(opts.Stderr, "Downloading %s...\n", asset.Name)
	zipPath, err := downloadVerified(ctx, opts.HTTPClient, asset.DownloadURL, opts.DestDir, expected)
	if err != nil {
		return nil, err
	}
	defer os.Remove(zipPath)

	if err := nukeAndExtract(zipPath, opts.DestDir); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	fmt.Fprintf(opts.Stderr, "Extracted extension %s to %s\n", rel.TagName, opts.DestDir)
	return &Result{Tag: rel.TagName, DestDir: opts.DestDir}, nil
}

func latestRelease(ctx context.Context, baseURL, repo string, client *http.Client) (*selfupdate.Release, error) {
	if baseURL == "" {
		return selfupdate.LatestRelease(ctx, repo, client)
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", baseURL, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel selfupdate.Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func fetchChecksums(ctx context.Context, rel *selfupdate.Release, client *http.Client) (map[string]string, error) {
	asset := selfupdate.FindAsset(rel, "checksums.txt")
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
	return selfupdate.ParseChecksums(resp.Body)
}

func downloadVerified(ctx context.Context, client *http.Client, url, destDir, expectedSHA256 string) (string, error) {
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
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destDir), ".borz-extension-*.zip")
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
	return tmpPath, nil
}

// nukeAndExtract removes destDir if it exists and extracts the zip into it.
// User confirmed the install dir is owned by us — overwrite without prompt.
func nukeAndExtract(zipPath, destDir string) error {
	if err := os.RemoveAll(destDir); err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if err := extractFile(destDir, f); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(destDir string, f *zip.File) error {
	// Reject zip slips: ensure the cleaned path stays under destDir.
	cleaned := filepath.Clean(f.Name)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return fmt.Errorf("invalid zip entry %q", f.Name)
	}
	target := filepath.Join(destDir, cleaned)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("invalid zip entry %q", f.Name)
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
