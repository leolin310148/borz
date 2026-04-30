package config

import (
	"path/filepath"
	"testing"
)

func TestAdditionalPathHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HomeEnv, home)
	if CommunityLockPath() != filepath.Join(home, "community.lock") {
		t.Fatalf("CommunityLockPath = %s", CommunityLockPath())
	}
	if SiteTrustPath() != filepath.Join(home, "sites-trust.json") {
		t.Fatalf("SiteTrustPath = %s", SiteTrustPath())
	}
	if SitesUsagePath() != filepath.Join(home, "sites-usage.json") {
		t.Fatalf("SitesUsagePath = %s", SitesUsagePath())
	}
}
