package extupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/selfupdate"
)

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport failed")
}

func TestHelperErrorBranches(t *testing.T) {
	if _, err := latestRelease(context.Background(), "://bad", "owner/repo", http.DefaultClient); err == nil {
		t.Fatal("latestRelease should reject bad API URL")
	}
	badJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{bad"))
	}))
	defer badJSON.Close()
	if _, err := latestRelease(context.Background(), badJSON.URL, "owner/repo", badJSON.Client()); err == nil {
		t.Fatal("latestRelease should reject invalid JSON")
	}
	rel := &selfupdate.Release{TagName: "v1", Assets: []selfupdate.Asset{{Name: "checksums.txt", DownloadURL: "://bad"}}}
	if _, err := fetchChecksums(context.Background(), rel, http.DefaultClient); err == nil {
		t.Fatal("fetchChecksums should reject bad checksum URL")
	}
	rel.Assets[0].DownloadURL = "http://example.test/checksums.txt"
	if _, err := fetchChecksums(context.Background(), rel, &http.Client{Transport: failingRoundTripper{}}); err == nil {
		t.Fatal("fetchChecksums should surface client errors")
	}
	if _, err := downloadVerified(context.Background(), http.DefaultClient, "://bad", t.TempDir(), ""); err == nil {
		t.Fatal("downloadVerified should reject bad URL")
	}
	if _, err := downloadVerified(context.Background(), &http.Client{Transport: failingRoundTripper{}}, "http://example.test/zip", t.TempDir(), ""); err == nil {
		t.Fatal("downloadVerified should surface client errors")
	}
	brokenBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
	}))
	defer brokenBody.Close()
	if _, err := downloadVerified(context.Background(), brokenBody.Client(), brokenBody.URL, t.TempDir(), strings.Repeat("0", 64)); err == nil {
		t.Fatal("downloadVerified should surface body read errors")
	}
	if err := nukeAndExtract(filepath.Join(t.TempDir(), "missing.zip"), filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("nukeAndExtract should fail on missing zip")
	}
}

func TestExtractFileDirectoryAndWriteErrors(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("dir/"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "dir.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := nukeAndExtract(zipPath, dest); err != nil {
		t.Fatalf("directory extract: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "dir")); err != nil || !fi.IsDir() {
		t.Fatalf("dir not extracted, fi=%+v err=%v", fi, err)
	}

	var fileBuf bytes.Buffer
	zw = zip.NewWriter(&fileBuf)
	w, err := zw.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "data"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipPath = filepath.Join(t.TempDir(), "file.zip")
	if err := os.WriteFile(zipPath, fileBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	blockingDest := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blockingDest, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := nukeAndExtract(zipPath, filepath.Join(blockingDest, "child")); err == nil {
		t.Fatal("expected extract failure when destination parent is a file")
	}
}
