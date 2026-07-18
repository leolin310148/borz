//go:build windows

package processlock

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// Lock is an advisory cross-process file lock.
type Lock struct {
	file       *os.File
	overlapped windows.Overlapped
}

// Acquire obtains an exclusive lock, waiting up to timeout. A non-positive
// timeout performs one non-blocking attempt.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	lock := &Lock{file: file}
	deadline := time.Now().Add(timeout)
	for {
		err = windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0, 1, 0, &lock.overlapped,
		)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			file.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			file.Close()
			return nil, fmt.Errorf("lock %s: timed out", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Release unlocks and closes the lock file.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := windows.UnlockFileEx(windows.Handle(l.file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
