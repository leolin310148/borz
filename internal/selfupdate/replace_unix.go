//go:build !windows

package selfupdate

import "os"

func replaceExecutable(dest, src string) error {
	return os.Rename(src, dest)
}
