package selfupdate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport failed")
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestAdditionalErrorBranches(t *testing.T) {
	if _, err := latestReleaseFrom(context.Background(), "://bad", "owner/repo", http.DefaultClient); err == nil {
		t.Fatal("latestReleaseFrom should reject a bad URL")
	}
	if _, err := latestReleaseFrom(context.Background(), "http://example.test", "owner/repo", &http.Client{Transport: failingTransport{}}); err == nil {
		t.Fatal("latestReleaseFrom should surface transport errors")
	}
	if _, err := ParseChecksums(failingReader{}); err == nil {
		t.Fatal("ParseChecksums should surface reader errors")
	}
	rel := &Release{TagName: "v1", Assets: []Asset{{Name: "checksums.txt", DownloadURL: "://bad"}}}
	if _, err := fetchChecksums(context.Background(), rel, http.DefaultClient); err == nil {
		t.Fatal("fetchChecksums should reject a bad URL")
	}
	rel.Assets[0].DownloadURL = "http://example.test/checksums.txt"
	if _, err := fetchChecksums(context.Background(), rel, &http.Client{Transport: failingTransport{}}); err == nil {
		t.Fatal("fetchChecksums should surface transport errors")
	}
	if _, err := downloadVerified(context.Background(), http.DefaultClient, "://bad", filepath.Join(t.TempDir(), "borz"), ""); err == nil {
		t.Fatal("downloadVerified should reject a bad URL")
	}
	if _, err := downloadVerified(context.Background(), &http.Client{Transport: failingTransport{}}, "http://example.test/bin", filepath.Join(t.TempDir(), "borz"), ""); err == nil {
		t.Fatal("downloadVerified should surface transport errors")
	}
	if got := humanSize(5 * 1024 * 1024 * 1024); !strings.Contains(got, "GB") {
		t.Fatalf("humanSize GB = %q", got)
	}
	if _, err := io.ReadAll(failingReader{}); err == nil {
		t.Fatal("failingReader sanity")
	}
}
