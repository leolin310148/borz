package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// cliSessionID returns privacy-safe correlation metadata that stays stable for
// commands launched from the same terminal or agent pane. An explicit value
// always wins; otherwise raw terminal identifiers are reduced to a pane number
// or a short one-way digest before they can reach operational logs.
func cliSessionID(explicit, tmuxPane, terminalSession string, parentPID int) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	if pane := strings.TrimPrefix(strings.TrimSpace(tmuxPane), "%"); isASCIIDigits(pane) {
		return "tmux-" + pane
	}
	if terminalSession = strings.TrimSpace(terminalSession); terminalSession != "" {
		sum := sha256.Sum256([]byte(terminalSession))
		return "terminal-" + hex.EncodeToString(sum[:6])
	}
	if parentPID > 0 {
		return fmt.Sprintf("cli-%d", parentPID)
	}
	return "cli"
}

func isASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
