//go:build !windows

package record

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// acquireLock takes an exclusive advisory lock on the evaluation directory's
// lock file without waiting. The kernel releases it when the process ends,
// however it ends, so a process that was killed leaves nothing to clear.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open the evaluation's lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock the evaluation: %w", err)
	}
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
