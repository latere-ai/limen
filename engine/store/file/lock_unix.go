// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package file

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lock takes an exclusive flock on path. The kernel releases it when the
// file is closed or the process exits, so a crashed owner leaves no stale
// lock behind.
func lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("file store: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, path)
		}
		return nil, fmt.Errorf("file store: lock %s: %w", path, err)
	}
	return f, nil
}

// unlock releases the lock by closing the file; LOCK itself stays.
func unlock(f *os.File) error { return f.Close() }
