package site

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogSkipsMalformedMetadataQuietlyButLintCanDiagnose(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.js")
	if err := os.WriteFile(bad, []byte("/* @meta {broken} */\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	old := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(old)
	ScanSites(dir, "local")
	if output.Len() != 0 {
		t.Fatalf("catalog noise: %s", output.String())
	}
	if _, err := ParseSiteMeta(bad, "local"); err == nil || !strings.Contains(err.Error(), "invalid @meta JSON") {
		t.Fatalf("missing explicit diagnostic: %v", err)
	}
}
