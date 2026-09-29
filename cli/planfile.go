// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
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

// readReviewedPlan reads and decodes the reviewed plan file. It needs no
// network: whatever Notion holds, a file that fails here cannot be applied.
func readReviewedPlan(path string) (planfile.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return planfile.File{}, fmt.Errorf("cannot read the plan file %s, nothing was applied: %v\n"+
			"  → pass the file written by `notion-seed plan --out`, or rerun "+
			"`notion-seed plan --out` with this version and have the new plan reviewed",
			path, err)
	}
	saved, err := planfile.Decode(data, Version)
	if err != nil {
		return planfile.File{}, fmt.Errorf("%s, nothing was applied: %w", path, err)
	}
	return saved, nil
}

// checkReviewedPlan holds the recomputed plan to the reviewed one: links,
// then change by change. It returns nil when the plan is applicable, and
// otherwise a refusal naming each difference.
func checkReviewedPlan(path string, saved planfile.File, prep *prepared) error {
	drifts := planfile.CheckLinks(saved, metaOf(prep, ""))
	drifts = append(drifts, planfile.Compare(saved, prep.plan)...)
	if len(drifts) == 0 {
		return nil
	}
	return reviewedPlanRefusal(path, drifts)
}

// reviewedPlanRefusal names each difference on its own line, then the way
// out. When every difference is a count that did not succeed now, the plan
// may well still hold: rerunning apply is enough, and saying "have a new plan
// reviewed" would send the user through a review for an API incident.
func reviewedPlanRefusal(path string, drifts []planfile.Drift) error {
	var b strings.Builder
	fmt.Fprintf(&b, "the plan recomputed now is not the one reviewed in %s, nothing was applied\n", path)
	onlyCounts := true
	for _, d := range drifts {
		fmt.Fprintf(&b, "  %s\n", d)
		onlyCounts = onlyCounts && d.CountFailed
	}
	if onlyCounts {
		// Not `notion-seed apply <path>`: that would drop --dir and every other
		// flag the first run used. "the same command" says to rerun it as typed.
		fmt.Fprintf(&b, "  → a count failed (its cause is printed above): rerun "+
			"the same command to apply %s; if it keeps failing, rerun "+
			"`notion-seed plan --out` and have the new plan reviewed", path)
	} else {
		b.WriteString("  → rerun `notion-seed plan --out` and have the new plan reviewed")
	}
	return errors.New(b.String())
}
