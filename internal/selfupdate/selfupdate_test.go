package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "borz-linux-amd64"},
		{"darwin", "arm64", "borz-darwin-arm64"},
		{"windows", "amd64", "borz-windows-amd64.exe"},
	}
	for _, c := range cases {
		if got := AssetName(c.goos, c.goarch); got != c.want {
			t.Errorf("AssetName(%s,%s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.0", false},
		{"0.1.0", "v0.1.1", true},
		{"v0.1.0", "0.1.1", true},
		{"", "0.1.0", true},
		{"dev", "0.1.0", true},
		{"0.1.0", "0.1.0-rc1", false}, // pre-release suffix ignored
		{"1.0.0", "1.0.0.1", true},
		{"1.2", "1.2.0", false},
	}
	for _, c := range cases {
		if got := NewerVersion(c.current, c.latest); got != c.want {
			t.Errorf("NewerVersion(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseChecksums(t *testing.T) {
	input := `# comment line
abc123  borz-linux-amd64
def456 *borz-windows-amd64.exe

   deadbeef  borz-darwin-arm64
`
	got, err := ParseChecksums(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"borz-linux-amd64":       "abc123",
		"borz-windows-amd64.exe": "def456",
		"borz-darwin-arm64":      "deadbeef",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestFindAsset(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "borz-linux-amd64"},
		{Name: "checksums.txt"},
	}}
	if FindAsset(rel, "borz-linux-amd64") == nil {
		t.Error("expected to find asset")
	}
	if FindAsset(rel, "nope") != nil {
		t.Error("expected nil for missing asset")
	}
}

func TestLatestRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"tag_name":"v0.5.0","assets":[{"name":"x","browser_download_url":"u","size":1}]}`)
	}))
	defer srv.Close()

	// Build a client that redirects api.github.com to our test server.
	transport := &rewriteTransport{base: http.DefaultTransport, target: srv.URL}
	client := &http.Client{Transport: transport}

	rel, err := LatestRelease(context.Background(), "owner/repo", client)
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v0.5.0" || len(rel.Assets) != 1 {
		t.Errorf("unexpected release: %+v", rel)
	}
}

type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	u := *req.URL
	// Replace scheme+host with target.
	targetReq, err := http.NewRequest(req.Method, t.target+u.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	targetReq.Header = r2.Header
	return t.base.RoundTrip(targetReq)
}

func TestRunEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename-over-running-exe semantics differ on windows")
	}

	// Fake new binary contents + checksum.
	newBin := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(newBin)
	sumHex := hex.EncodeToString(sum[:])
	assetName := AssetName(runtime.GOOS, runtime.GOARCH)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[
				{"name":%q,"browser_download_url":%q,"size":%d},
				{"name":"checksums.txt","browser_download_url":%q,"size":1}
			]}`, assetName, srv.URL+"/bin", len(newBin), srv.URL+"/checksums.txt")
		case r.URL.Path == "/bin":
			w.Write(newBin)
		case r.URL.Path == "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", sumHex, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Create a fake "current" executable so os.Executable() path logic can be
	// exercised indirectly — we test replaceExecutable + downloadVerified
	// directly rather than go through Run(), since Run() replaces the *test*
	// binary otherwise.
	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "borz")
	if err := os.WriteFile(fakeExe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &rewriteTransport{base: http.DefaultTransport, target: srv.URL}}
	rel, err := LatestRelease(context.Background(), "owner/repo", client)
	if err != nil {
		t.Fatal(err)
	}
	checksums, err := fetchChecksums(context.Background(), rel, client)
	if err != nil {
		t.Fatal(err)
	}
	if checksums[assetName] != sumHex {
		t.Fatalf("checksum mismatch: %v", checksums)
	}

	asset := FindAsset(rel, assetName)
	if asset == nil {
		t.Fatalf("no asset %s", assetName)
	}
	tmpPath, err := downloadVerified(context.Background(), client, asset.DownloadURL, fakeExe, sumHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(fakeExe, tmpPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBin) {
		t.Errorf("replaced contents = %q, want %q", got, newBin)
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{500, "500 B"},
		{2048, "2.0 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
	}
	for _, c := range cases {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestRunUpToDate(t *testing.T) {
	srv := newFakeReleaseServer(t, "v1.0.0", "bogus", nil, "")
	defer srv.Close()

	var buf strings.Builder
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		Stderr:         &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Already up to date") {
		t.Errorf("expected up-to-date message, got %q", buf.String())
	}
}

func TestRunCheckOnly(t *testing.T) {
	srv := newFakeReleaseServer(t, "v2.0.0", "bogus", nil, "")
	defer srv.Close()

	var buf strings.Builder
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		CheckOnly:      true,
		APIBaseURL:     srv.URL,
		Stderr:         &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Update available") {
		t.Errorf("expected update-available message, got %q", buf.String())
	}
}

func TestRunReplacesExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename semantics differ on windows")
	}
	newBin := []byte("new-binary-payload")
	sum := sha256.Sum256(newBin)
	sumHex := hex.EncodeToString(sum[:])

	srv := newFakeReleaseServer(t, "v2.0.0", sumHex, newBin, AssetName(runtime.GOOS, runtime.GOARCH))
	defer srv.Close()

	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "borz")
	if err := os.WriteFile(fakeExe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf strings.Builder
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		ExecutablePath: fakeExe,
		Stderr:         &buf,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(fakeExe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBin) {
		t.Errorf("binary not replaced: got %q", got)
	}
}

func TestRunFiresOnReplaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("rename semantics differ on windows")
	}
	newBin := []byte("new-binary-payload")
	sum := sha256.Sum256(newBin)
	sumHex := hex.EncodeToString(sum[:])

	srv := newFakeReleaseServer(t, "v2.0.0", sumHex, newBin, AssetName(runtime.GOOS, runtime.GOARCH))
	defer srv.Close()

	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "borz")
	if err := os.WriteFile(fakeExe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	called := 0
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		ExecutablePath: fakeExe,
		Stderr:         io.Discard,
		OnReplaced:     func() { called++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Errorf("OnReplaced called %d times, want 1", called)
	}
}

func TestRunSkipsOnReplacedWhenUpToDate(t *testing.T) {
	srv := newFakeReleaseServer(t, "v1.0.0", "bogus", nil, "")
	defer srv.Close()

	called := 0
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		Stderr:         io.Discard,
		OnReplaced:     func() { called++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Errorf("OnReplaced fired on no-op update (called=%d)", called)
	}
}

func TestRunSkipsOnReplacedForCheckOnly(t *testing.T) {
	srv := newFakeReleaseServer(t, "v2.0.0", "bogus", nil, "")
	defer srv.Close()

	called := 0
	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		CheckOnly:      true,
		APIBaseURL:     srv.URL,
		Stderr:         io.Discard,
		OnReplaced:     func() { called++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Errorf("OnReplaced fired in --check mode (called=%d)", called)
	}
}

func TestRunMissingAsset(t *testing.T) {
	srv := newFakeReleaseServer(t, "v2.0.0", "sum", []byte("x"), "other-asset-name")
	defer srv.Close()

	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "borz")
	os.WriteFile(fakeExe, []byte("old"), 0o755)

	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		ExecutablePath: fakeExe,
		Stderr:         io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no release asset") {
		t.Fatalf("expected missing-asset error, got %v", err)
	}
}

func TestLatestReleaseFromErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limited", http.StatusForbidden)
		}))
		defer srv.Close()

		_, err := latestReleaseFrom(context.Background(), srv.URL, "owner/repo", http.DefaultClient)
		if err == nil || !strings.Contains(err.Error(), "github api 403") {
			t.Fatalf("expected 403 error, got %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`not-json`))
		}))
		defer srv.Close()

		_, err := latestReleaseFrom(context.Background(), srv.URL, "owner/repo", http.DefaultClient)
		if err == nil {
			t.Fatal("expected JSON decode error")
		}
	})
}

func TestFetchChecksumsErrors(t *testing.T) {
	t.Run("missing checksums asset", func(t *testing.T) {
		_, err := fetchChecksums(context.Background(), &Release{TagName: "v1.0.0"}, http.DefaultClient)
		if err == nil || !strings.Contains(err.Error(), "no checksums.txt") {
			t.Fatalf("expected missing checksums error, got %v", err)
		}
	})

	t.Run("http error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer srv.Close()

		rel := &Release{TagName: "v1.0.0", Assets: []Asset{{Name: "checksums.txt", DownloadURL: srv.URL}}}
		_, err := fetchChecksums(context.Background(), rel, http.DefaultClient)
		if err == nil || !strings.Contains(err.Error(), "http 502") {
			t.Fatalf("expected checksum HTTP error, got %v", err)
		}
	})
}

func TestRunMissingChecksumEntry(t *testing.T) {
	assetName := AssetName(runtime.GOOS, runtime.GOARCH)
	srv := newFakeReleaseServer(t, "v2.0.0", "abc123", []byte("payload"), "other-name")
	defer srv.Close()

	// Override the release endpoint to publish the current platform asset while
	// checksums.txt names a different file.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprintf(w, `{"tag_name":"v2.0.0","assets":[
				{"name":%q,"browser_download_url":%q,"size":7},
				{"name":"checksums.txt","browser_download_url":%q,"size":1}
			]}`, assetName, srv.URL+"/bin", srv.URL+"/checksums.txt")
		case r.URL.Path == "/bin":
			w.Write([]byte("payload"))
		case r.URL.Path == "/checksums.txt":
			fmt.Fprintf(w, "abc123  other-name\n")
		default:
			http.NotFound(w, r)
		}
	})

	err := Run(context.Background(), Options{
		CurrentVersion: "1.0.0",
		Repo:           "owner/repo",
		APIBaseURL:     srv.URL,
		ExecutablePath: filepath.Join(t.TempDir(), "borz"),
		Stderr:         io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Fatalf("expected missing checksum entry error, got %v", err)
	}
}

// newFakeReleaseServer stands up a GitHub-API-compatible server exposing one
// release with the given tag. If assetName is non-empty, it also publishes a
// platform binary + a matching checksums.txt.
func newFakeReleaseServer(t *testing.T, tag, sumHex string, binBody []byte, assetName string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			if assetName == "" {
				fmt.Fprintf(w, `{"tag_name":%q,"assets":[]}`, tag)
				return
			}
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[
				{"name":%q,"browser_download_url":%q,"size":%d},
				{"name":"checksums.txt","browser_download_url":%q,"size":1}
			]}`, tag, assetName, srv.URL+"/bin", len(binBody), srv.URL+"/checksums.txt")
		case r.URL.Path == "/bin":
			w.Write(binBody)
		case r.URL.Path == "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", sumHex, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func TestDownloadVerifiedChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "borz")
	_, err := downloadVerified(context.Background(), http.DefaultClient, srv.URL, dest, "0000")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %v", entries)
	}
}

func TestDownloadVerifiedHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := downloadVerified(context.Background(), http.DefaultClient, srv.URL, filepath.Join(t.TempDir(), "borz"), "abc")
	if err == nil || !strings.Contains(err.Error(), "http 404") {
		t.Fatalf("expected download HTTP error, got %v", err)
	}
}
