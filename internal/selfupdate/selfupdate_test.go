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
		{"linux", "amd64", "bb-browser-linux-amd64"},
		{"darwin", "arm64", "bb-browser-darwin-arm64"},
		{"windows", "amd64", "bb-browser-windows-amd64.exe"},
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
abc123  bb-browser-linux-amd64
def456 *bb-browser-windows-amd64.exe

   deadbeef  bb-browser-darwin-arm64
`
	got, err := ParseChecksums(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"bb-browser-linux-amd64":       "abc123",
		"bb-browser-windows-amd64.exe": "def456",
		"bb-browser-darwin-arm64":      "deadbeef",
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
		{Name: "bb-browser-linux-amd64"},
		{Name: "checksums.txt"},
	}}
	if FindAsset(rel, "bb-browser-linux-amd64") == nil {
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
	fakeExe := filepath.Join(dir, "bb-browser")
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

func TestDownloadVerifiedChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "bb-browser")
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
