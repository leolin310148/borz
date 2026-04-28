package extupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fakeRelease wires a httptest server that serves a release JSON, the zip
// asset, and a checksums.txt — enough to exercise Run end-to-end.
func fakeRelease(t *testing.T, zipBytes []byte, checksumName string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var server *httptest.Server

	zipURL := "/download/bb-browser-extension.zip"
	checksumsURL := "/download/checksums.txt"
	checksums := fmt.Sprintf("%s  %s\n", sha256hex(zipBytes), checksumName)

	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := map[string]any{
			"tag_name": "v1.2.3",
			"name":     "v1.2.3",
			"assets": []map[string]any{
				{
					"name":                 "bb-browser-extension.zip",
					"browser_download_url": server.URL + zipURL,
					"size":                 int64(len(zipBytes)),
				},
				{
					"name":                 "checksums.txt",
					"browser_download_url": server.URL + checksumsURL,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc(zipURL, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	})
	mux.HandleFunc(checksumsURL, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, checksums)
	})

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestRunHappyPath(t *testing.T) {
	zipBytes := makeZip(t, map[string]string{
		"manifest.json":  `{"name":"x"}`,
		"background.js":  `console.log("hi");`,
		"sub/popup.html": `<html></html>`,
	})
	srv := fakeRelease(t, zipBytes, "bb-browser-extension.zip")

	dest := filepath.Join(t.TempDir(), "extension")
	// Pre-populate to confirm the nuke step erases prior contents.
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Options{
		Repo:       "owner/repo",
		DestDir:    dest,
		HTTPClient: srv.Client(),
		APIBaseURL: srv.URL,
		Stderr:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Tag != "v1.2.3" {
		t.Errorf("tag = %q", res.Tag)
	}
	if res.DestDir != dest {
		t.Errorf("destdir = %q want %q", res.DestDir, dest)
	}

	// Stale file is gone.
	if _, err := os.Stat(filepath.Join(dest, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("stale.txt should be removed: %v", err)
	}
	// New files are present.
	for _, name := range []string{"manifest.json", "background.js", "sub/popup.html"} {
		p := filepath.Join(dest, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
}

func TestRunChecksumMismatch(t *testing.T) {
	zipBytes := makeZip(t, map[string]string{"manifest.json": `{}`})
	// Server reports a checksum for a different filename → mismatch path
	// for "no checksum entry" branch.
	srv := fakeRelease(t, zipBytes, "wrong-name.zip")
	dest := filepath.Join(t.TempDir(), "extension")
	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: dest, HTTPClient: srv.Client(),
		APIBaseURL: srv.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Fatalf("want no-checksum-entry error, got %v", err)
	}
}

func TestRunChecksumActualMismatch(t *testing.T) {
	zipBytes := makeZip(t, map[string]string{"manifest.json": `{}`})

	// Custom server: lie about the sha so verification fails.
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := map[string]any{
			"tag_name": "v1.2.3",
			"assets": []map[string]any{
				{"name": "bb-browser-extension.zip", "browser_download_url": server.URL + "/zip"},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/sums"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "deadbeef  bb-browser-extension.zip\n")
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "extension")
	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: dest, HTTPClient: server.Client(),
		APIBaseURL: server.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum-mismatch error, got %v", err)
	}
}

func TestRunMissingExtensionAsset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.3",
			"assets":   []map[string]any{}, // no extension asset
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "extension")
	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: dest, HTTPClient: srv.Client(),
		APIBaseURL: srv.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no bb-browser-extension.zip asset") {
		t.Fatalf("want missing-asset error, got %v", err)
	}
}

func TestRunMissingChecksums(t *testing.T) {
	zipBytes := makeZip(t, map[string]string{"manifest.json": `{}`})
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.3",
			"assets": []map[string]any{
				{"name": "bb-browser-extension.zip", "browser_download_url": server.URL + "/zip"},
			},
		})
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	server = httptest.NewServer(mux)
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "extension")
	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: dest, HTTPClient: server.Client(),
		APIBaseURL: server.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "no checksums.txt") {
		t.Fatalf("want no-checksums error, got %v", err)
	}
}

func TestRunRequiresDestDir(t *testing.T) {
	_, err := Run(context.Background(), Options{Repo: "owner/repo"})
	if err == nil || !strings.Contains(err.Error(), "DestDir is required") {
		t.Fatalf("want DestDir error, got %v", err)
	}
}

func TestRunReleaseNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: t.TempDir(), HTTPClient: srv.Client(),
		APIBaseURL: srv.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "fetch latest release") {
		t.Fatalf("want fetch-release error, got %v", err)
	}
}

func TestExtractFileRejectsZipSlip(t *testing.T) {
	// Build a zip with a path-traversal entry and confirm extraction refuses.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("pwn"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "out")
	err = nukeAndExtract(zipPath, dest)
	if err == nil || !strings.Contains(err.Error(), "invalid zip entry") {
		t.Fatalf("want invalid-zip-entry error, got %v", err)
	}
}

func TestRunZipDownloadFails(t *testing.T) {
	// Release JSON points the zip URL at a 500 endpoint.
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.3",
			"assets": []map[string]any{
				{"name": "bb-browser-extension.zip", "browser_download_url": server.URL + "/zip"},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/sums"},
			},
		})
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "deadbeef  bb-browser-extension.zip\n")
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: t.TempDir() + "/ext", HTTPClient: server.Client(),
		APIBaseURL: server.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "http 500") {
		t.Fatalf("want http 500 from zip download, got %v", err)
	}
}

func TestRunChecksumsDownloadFails(t *testing.T) {
	zipBytes := makeZip(t, map[string]string{"manifest.json": `{}`})
	mux := http.NewServeMux()
	var server *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.3",
			"assets": []map[string]any{
				{"name": "bb-browser-extension.zip", "browser_download_url": server.URL + "/zip"},
				{"name": "checksums.txt", "browser_download_url": server.URL + "/sums"},
			},
		})
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(zipBytes) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	server = httptest.NewServer(mux)
	defer server.Close()

	_, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: t.TempDir() + "/ext", HTTPClient: server.Client(),
		APIBaseURL: server.URL, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "fetch checksums") {
		t.Fatalf("want fetch-checksums error, got %v", err)
	}
}

func TestRunHandlesZipDirEntries(t *testing.T) {
	// Build a zip that includes an explicit directory entry to cover the
	// IsDir() branch in extractFile.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("subdir/"); err != nil {
		t.Fatal(err)
	}
	w, _ := zw.Create("subdir/file.txt")
	_, _ = w.Write([]byte("hi"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := buf.Bytes()
	srv := fakeRelease(t, zipBytes, "bb-browser-extension.zip")
	dest := t.TempDir() + "/ext"
	if _, err := Run(context.Background(), Options{
		Repo: "owner/repo", DestDir: dest, HTTPClient: srv.Client(),
		APIBaseURL: srv.URL, Stderr: io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "subdir", "file.txt")); err != nil {
		t.Errorf("expected file: %v", err)
	}
}

func TestLatestReleaseNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := latestRelease(context.Background(), srv.URL, "owner/repo", srv.Client())
	if err == nil || !strings.Contains(err.Error(), "github api 403") {
		t.Fatalf("want github api 403 error, got %v", err)
	}
}

func TestRunDefaultRepo(t *testing.T) {
	// Cover the "Repo == ''" default branch — pointed at a stub server that
	// returns 404 so we don't actually hit GitHub. We just want to exercise
	// the assignment path, then bail out on the network error.
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/leolin310148/bb-browser-go/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "stub", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := Run(context.Background(), Options{
		DestDir: t.TempDir(), HTTPClient: srv.Client(),
		APIBaseURL: srv.URL, Stderr: io.Discard,
	})
	if err == nil {
		t.Fatal("expected error from stub release endpoint")
	}
}
