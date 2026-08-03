//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func copySecretToClipboard(secret string) error {
	for _, candidate := range []struct {
		name string
		args []string
	}{{"wl-copy", nil}, {"xclip", []string{"-selection", "clipboard"}}} {
		path, err := exec.LookPath(candidate.name)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, candidate.args...)
		cmd.Stdin = strings.NewReader(secret)
		return cmd.Run()
	}
	return fmt.Errorf("no clipboard helper found (install wl-copy or xclip)")
}
