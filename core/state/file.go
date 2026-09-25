// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/tykok/notion-seed/internal/atomicfile"
)

// Load reads the state file of a configuration directory.
//
// A missing file is not an error: it is the starting point of every project,
// and it is what makes `plan` without a state behave exactly as it did before
// this package existed.
func Load(dir string) (*Snapshot, error) {
	path := Path(dir)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Snapshot{Version: Version}, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"failed to read %s: %w\n"+
				"  → check the permissions on the file, or remove it to start over "+
				"from an empty state (the resources will have to be imported again)", path, err)
	}

	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf(
			"unreadable %s: %w\n"+
				"  → the file is truncated or badly merged. Restore it from git "+
				"(`git checkout -- %s`) rather than deleting it: deleting it would make "+
				"the databases be re-created instead of recognized",
			FileName, err, FileName)
	}
	if s.Version != Version {
		return nil, fmt.Errorf(
			"%s is at version %d, this version of notion-seed reads version %d\n"+
				"  → update notion-seed; do not edit the file by hand, "+
				"its identities would be wrong",
			FileName, s.Version, Version)
	}
	if s.Databases == nil {
		s.Databases = map[string]Database{}
	}
	return &s, nil
}

// Save writes the state atomically and deterministically.
//
// Atomic: a state truncated by an interruption is a lost identity, hence a
// database re-created as a duplicate on the first apply. A temporary file is
// written in the SAME directory (so rename does not cross file systems),
// synced, then renamed.
//
// Deterministic: json.Marshal sorts map keys, the indentation is fixed and the
// file ends with a newline. A versioned file whose order moves on every write
// is unreadable in review.
func Save(dir string, s *Snapshot) error {
	s.Version = Version

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf(
			"failed to serialize the state: %w\n"+
				"  → this is a notion-seed bug, not a configuration error: "+
				"report it", err)
	}
	body = append(body, '\n')

	// os.CreateTemp creates the file as 0600; that is not the mode of a file
	// meant to be versioned, reviewed and read by the CI, so 0644 is asked
	// explicitly.
	if err := atomicfile.Write(Path(dir), body, 0o644); err != nil {
		return fmt.Errorf(
			"failed to write %s: %w\n"+
				"  → check the write permissions on %s; the previous state is intact",
			FileName, err, dir)
	}
	return nil
}
