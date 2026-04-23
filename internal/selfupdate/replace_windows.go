//go:build windows

package selfupdate

import "os"

// replaceExecutable on Windows can't delete a running .exe, but it can rename
// it. Move the current exe aside, then move the new one into place. The old
// copy is scheduled for removal on a best-effort basis.
func replaceExecutable(dest, src string) error {
	old := dest + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dest, old); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err != nil {
		_ = os.Rename(old, dest)
		return err
	}
	_ = os.Remove(old)
	return nil
}
