//go:build windows

package main

import (
	"os/exec"
	"strings"
)

func copySecretToClipboard(secret string) error {
	cmd := exec.Command("cmd.exe", "/c", "clip.exe")
	cmd.Stdin = strings.NewReader(secret)
	return cmd.Run()
}
