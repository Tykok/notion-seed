// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Fingerprint is the SHA-256 of the snapshot AS UNDERSTOOD, in hex.
//
// It hashes the same canonical form Save writes — map keys sorted by
// encoding/json, options in the order the API returns them — at the current
// file version, and without the indentation: a state reformatted by hand, or
// by a merge tool, keeps its fingerprint as long as it holds the same
// identities. A missing file and an empty state are the same starting point
// (Load returns one for the other), and hash the same.
//
// It never modifies the snapshot: Save sets Version on the snapshot it
// writes, Fingerprint works on a copy.
//
// A plan file carries this fingerprint: apply refuses to write a plan
// computed against a state that has since changed — another apply went
// through in between, and the plan describes a starting point that no longer
// exists.
func Fingerprint(s *Snapshot) string {
	c := Snapshot{Version: Version}
	if s != nil {
		c.WorkspaceID = s.WorkspaceID
		c.Databases = s.Databases
	}
	// json.Marshal cannot fail on Snapshot: strings, ints, slices and maps
	// with string keys only.
	body, _ := json.Marshal(c)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
