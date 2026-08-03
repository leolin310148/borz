//go:build !darwin && !linux && !windows

package main

import "fmt"

func copySecretToClipboard(string) error {
	return fmt.Errorf("clipboard copy is not supported on this platform")
}
