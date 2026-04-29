//go:build !windows

package winservice

import "fmt"

func Supported() bool { return false }

func Install(Config) error {
	return unsupported()
}

func Uninstall(string) error {
	return unsupported()
}

func Start(string) error {
	return unsupported()
}

func Stop(string) error {
	return unsupported()
}

func Status(string) (string, error) {
	return "", unsupported()
}

func Run(string, Runner) error {
	return unsupported()
}

func unsupported() error {
	return fmt.Errorf("Windows service management is only supported on Windows")
}
