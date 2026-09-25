// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"time"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/planfile"
	"github.com/tykok/notion-seed/core/state"
)

// metaOf is what ties a plan to its starting point: the version that computed
// it, the workspace, and the fingerprints of the configuration and the state
// it was computed from. plan --out writes it; apply <file> recomputes it and
// compares.
//
// It MUST be computed from what preparePlan loaded, before anything modifies
// the snapshot: apply stamps the workspace on it just before writing.
func metaOf(prep *prepared, rendered string) planfile.Meta {
	return planfile.Meta{
		Version:      Version,
		WorkspaceID:  prep.workspaceID,
		ConfigSHA256: config.Fingerprint(prep.cfg),
		StateSHA256:  state.Fingerprint(prep.snap),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		Rendered:     rendered,
	}
}

// writePlanFile freezes the plan into path, atomically.
func writePlanFile(path string, prep *prepared, rendered string) error {
	data, err := planfile.Encode(prep.plan, metaOf(prep, rendered))
	if err != nil {
		return err
	}
	return planfile.WriteFile(path, data)
}
