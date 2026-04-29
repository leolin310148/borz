// Package winservice installs and controls borz as a Windows service.
package winservice

import "context"

const (
	DefaultName        = "borz"
	DefaultDisplayName = "borz browser automation server"
	DefaultDescription = "Runs the borz REST server in the background."
)

// Config describes a Windows service registration.
type Config struct {
	Name           string
	DisplayName    string
	Description    string
	ExecutablePath string
	Args           []string
}

// Runner is invoked by the Windows service entry point.
type Runner func(context.Context) error
