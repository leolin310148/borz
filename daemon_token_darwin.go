//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

func copySecretToClipboard(secret string) error {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(secret)
	return cmd.Run()
}
