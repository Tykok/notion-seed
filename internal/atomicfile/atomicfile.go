// SPDX-License-Identifier: GPL-3.0-or-later

// Package atomicfile writes a file the way a reader never observes a partial
// write: a temporary file in the SAME directory as the target (so the rename
// that follows never crosses a file system boundary), synced to disk, then
// renamed over the target. An interruption at any point leaves the previous
// file, or none — never a truncated one.
//
// state.Save and planfile.WriteFile both need exactly this sequence; this
// package is the one place it is written, so the two callers cannot drift
// apart on it.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write replaces path with data atomically, at the given permission.
//
// os.CreateTemp creates its file as 0600: Write always chmods it explicitly,
// since a file meant to be read back — by another process, by git, by a CI —
// must carry the permission its caller asks for, not the temporary file's
// default.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("set permissions on %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}
