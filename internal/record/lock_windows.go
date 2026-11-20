//go:build windows

package record

import (
	"fmt"
	"os"
)

// acquireLock holds the evaluation directory's lock file open for the life of
// the store. Windows refuses a second open of a file opened without sharing,
// and releases it when the process ends, so the handle is the lock.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		return f, nil
	}
	if !os.IsExist(err) {
		return nil, fmt.Errorf("open the evaluation's lock: %w", err)
	}
	// The file is left behind by a process that ended. Removing it fails
	// while another process still holds it open.
	if rmErr := os.Remove(path); rmErr != nil {
		return nil, ErrLocked
	}
	f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, ErrLocked
	}
	return f, nil
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}
