//go:build !windows

package processlock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Lock is an advisory cross-process file lock.
type Lock struct {
	file *os.File
}

// Acquire obtains an exclusive lock, waiting up to timeout. A non-positive
// timeout performs one non-blocking attempt.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
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
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
