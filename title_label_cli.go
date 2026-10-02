package main

import (
	"os"
	"strings"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/daemon"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

// managedTitleLabel returns the "[profile] " page-title prefix for a daemon or
// server that drives a Chrome borz launched itself (browserOwned) for a
// managed profile. Attached cdp browsers belong to someone else, so their
// titles are never touched. BORZ_NO_TITLE_LABEL=1 turns the label off.
func managedTitleLabel(browserOwned bool) string {
	if !browserOwned || titleLabelDisabled() {
		return ""
	}
	name := borzprofile.Normalize(config.Profile())
	target, err := borzprofile.ResolveTarget(name)
	if err != nil || target.Kind != borzprofile.TransportManaged {
		return ""
	}
	return daemon.TitleLabelPrefix(name)
}

func titleLabelDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BORZ_NO_TITLE_LABEL"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
