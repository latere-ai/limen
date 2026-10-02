// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package file

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// lock creates path exclusively. Without flock, a crashed owner leaves the
// file behind; an operator removes it once no process uses the directory.
func lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: %s exists; remove it if no process uses the directory", ErrLocked, path)
	}
	if err != nil {
		return nil, fmt.Errorf("file store: create %s: %w", path, err)
	}
	return f, nil
}

// unlock closes and removes the lock file.
func unlock(f *os.File) error {
	return errors.Join(f.Close(), os.Remove(f.Name()))
}
