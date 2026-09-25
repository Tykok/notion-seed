// SPDX-License-Identifier: GPL-3.0-or-later

package planfile

import (
	"fmt"

	"github.com/tykok/notion-seed/internal/atomicfile"
)

// WriteFile writes a plan file atomically, the way state.Save writes the
// state (see internal/atomicfile): a temporary file in the SAME directory,
// synced, then renamed over the target. An interrupted write leaves the
// previous file, or none — never a truncated plan a CI would then try to
// apply.
func WriteFile(path string, data []byte) error {
	// os.CreateTemp creates the file as 0600; a plan file is meant to be read
	// by the CI and uploaded as an artifact.
	if err := atomicfile.Write(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write the plan file %s: %w\n"+
			"  → check that the directory exists and is writable; the previous file, if any, is intact",
			path, err)
	}
	return nil
}
