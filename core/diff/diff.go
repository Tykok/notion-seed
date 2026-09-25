// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Change is a change shown to the user.
type Change struct {
	Class    Class
	Resource string
	Detail   string

	// Details holds the elementary changes, not flattened. The measurement
	// pass enriches them AFTER Compute: flattening them into strings here would
	// make the plan unmeasurable.
	Details []resources.Detail

	// Key is the config key, without the "database." prefix. apply needs it to
	// index the state; Resource is meant for display.
	Key string
	// Kind says whether this is a creation, an update or a destruction. apply
	// picks its write from it, without re-reading the text of the lines.
	Kind resources.ChangeKind
	// Target is what gets written: non-nil on an allowed creation or update,
	// always nil on a destruction, which has no state afterwards. The
	// permission to write is Withheld == "", never Target: see Result.Target.
	Target *state.Database
	// Withheld says why this resource will not be written, or "" if it can be
	// — Withheld == "" is the permission. See diff.Result.Withheld.
	Withheld string

	// Acknowledged names the lifecycle keys that cover this resource.
	//
	// They no longer block anything: the user answers for their database. They
	// say what the user has already acknowledged, and the rendering uses them
	// to raise or lower the tone. A missing key does not withhold the write, it
	// makes the line louder.
	Acknowledged []string
}

// Drift is a mismatch found between the state and the actual state.
type Drift struct {
	Resource string
	Lines    []string
}

// Unmanaged lists what exists in Notion without being declared.
type Unmanaged struct {
	Resource string
	Lines    []string
}

// Refreshed is the result of reading a resource from the state.
//
// Missing tells "read and absent" apart from "not read": without this
// distinction, a vanished managed resource would pass for a resource never
// applied, hence for a creation — exactly the misreading to avoid.
//
// DataSources is the number of data sources the read-back database holds, 0 if
// unknown. It has no place in state.Database, which is written to disk: it is
// a fact of the actual state, used only to say that a row count is partial.
type Refreshed struct {
	Database    state.Database
	Missing     bool
	Reason      string
	DataSources int
}

// Plan is the result of the diff pass.
type Plan struct {
	ToAdd     int
	ToChange  int
	ToDestroy int
	Changes   []Change
	Drifts    []Drift
	Unmanaged []Unmanaged

	// NotCompared names the resources the state anchors but whose actual state
	// was not read (--skip-preflight, which makes no call). CompareDatabase
	// then returns an empty result, "we know nothing, so we say nothing" — but
	// an empty result is not an observed match, and Render must be able to
	// tell the two apart.
	NotCompared []string

	// StaleState names the resources the state anchors, that the configuration
	// no longer declares, and that no longer exist in Notion: the destruction
	// already happened outside notion-seed. Removing their entry writes nothing
	// to Notion — it is a local cleanup, not a destruction.
	StaleState []string

	// Blocked NO LONGER concerns change classes — none of them blocks any
	// more. It stays true only for what makes the plan impossible to compute: a
	// resource the state anchors and that Notion no longer knows.
	Blocked bool
	// BlockedReasons names each block and its way out. "at least one change
	// rejected" does not tell the user what to do.
	BlockedReasons []string
}

// Compute compares the desired configuration, the last applied state and the
// actual state.
//
// applied can be nil (no state) and actual can be empty: this falls back
// exactly to the behaviour from before the state existed, where everything
// comes out as a creation.
func Compute(cfg *config.Config, applied *state.Snapshot, actual map[string]Refreshed) (*Plan, error) {
	p := &Plan{}
	appliedDBs := map[string]state.Database{}
	if applied != nil {
		appliedDBs = applied.Databases
	}

	allowDataLoss := setOf(cfg.Lifecycle.AllowDataLoss)
	preventDestroy := setOf(cfg.Lifecycle.PreventDestroy)

	seen := map[string]bool{}
	for _, db := range cfg.Databases {
		seen[db.Key] = true
		desired := state.FromConfig(db)

		var appliedPtr, actualPtr *state.Database
		if a, ok := appliedDBs[db.Key]; ok {
			appliedPtr = &a
		}
		if r, ok := actual[db.Key]; ok {
			if r.Missing {
				p.block(fmt.Sprintf(
					"database.%s is in the state but %s in Notion.\n"+
						"  → restore it in Notion, or remove its entry from %s to "+
						"accept a re-creation (the new database will start empty)",
					db.Key, r.Reason, state.FileName))
				continue
			}
			d := r.Database
			actualPtr = &d
		} else if appliedPtr != nil {
			// The state anchors this resource, but actual has no entry: this is
			// --skip-preflight, which never reads the actual state (see the
			// comment of CompareDatabase on this same case). The plan must not
			// suggest it verified a match it never actually read.
			p.NotCompared = append(p.NotCompared, "database."+db.Key)
		}

		p.absorb(db.Key, CompareDatabase(db.Key, &desired, appliedPtr, actualPtr),
			allowDataLoss, preventDestroy)
	}

	// The state's resources that the configuration no longer declares.
	orphans := make([]string, 0)
	for key := range appliedDBs {
		if !seen[key] {
			orphans = append(orphans, key)
		}
	}
	sort.Strings(orphans)
	for _, key := range orphans {
		a := appliedDBs[key]
		resource := "database." + key

		r, read := actual[key]
		switch {
		case !read:
			// --skip-preflight: nothing was read, so the destruction can neither
			// be planned nor concluded to have already happened. Announcing a
			// destruction here is what the previous version did: it concluded
			// without ever looking at the actual state.
			p.NotCompared = append(p.NotCompared, resource)
			continue

		case r.Missing:
			// Not found or archived: in Notion, destroying a database means
			// archiving it, so both cases count as a destruction already done.
			// prevent_destroy no longer blocks this cleanup: the stale state
			// entry is removed in every case, and the rendering of StaleState
			// carries the notice, not a refusal.
			p.StaleState = append(p.StaleState, resource)
			continue
		}

		d := r.Database
		res := CompareDatabase(key, nil, &a, &d)
		markUncountedDataSources(res.Changeset.Details, r.DataSources)
		p.absorb(key, res, allowDataLoss, preventDestroy)
	}
	return p, nil
}

// markUncountedDataSources records, on a destruction's count request, the
// data sources it will not query: the count reads only one, the trash takes
// them all. An unknown number (0) or a single data source marks nothing.
func markUncountedDataSources(details []resources.Detail, dataSources int) {
	if dataSources <= 1 {
		return
	}
	for i := range details {
		if m := details[i].Measure; m != nil && m.AllRows {
			m.UncountedDataSources = dataSources - 1
		}
	}
}

// absorb pours a resource's result into the plan, and records the lifecycle
// keys that cover it. allowDataLoss and preventDestroy no longer block
// anything here: they are acknowledgements, not safeguards.
func (p *Plan) absorb(key string, res Result, allowDataLoss, preventDestroy map[string]bool) {
	resource := "database." + key

	if len(res.Drift) > 0 {
		p.Drifts = append(p.Drifts, Drift{Resource: resource, Lines: res.Drift})
	}
	if len(res.Unmanaged) > 0 {
		p.Unmanaged = append(p.Unmanaged, Unmanaged{Resource: resource, Lines: res.Unmanaged})
	}
	if res.Changeset.Kind == resources.KindNone {
		return
	}

	switch res.Changeset.Kind {
	case resources.KindCreate:
		p.ToAdd++
	case resources.KindUpdate:
		p.ToChange++
	case resources.KindDestroy:
		p.ToDestroy++
	}

	c := Change{
		Resource: resource,
		Key:      key,
		Kind:     res.Changeset.Kind,
		Target:   res.Target,
		Withheld: res.Withheld,
		Class:    WorstClass(res.Changeset.Details),
	}
	if res.Changeset.Kind == resources.KindCreate {
		c.Detail = "(new)"
	}
	// The details go through as they are: the measurement pass, after Compute,
	// enriches them. Flattening them into strings here — what the previous
	// version did — made the plan unmeasurable, and the rendering only needs
	// Details anyway.
	c.Details = res.Changeset.Details

	// lifecycle no longer blocks anything: notion-seed no longer refuses a
	// change on the strength of its class, it MEASURES its cost and says it
	// (see Class and Details above). prevent_destroy and allow_data_loss are
	// therefore only acknowledgements, put on the line so the rendering raises
	// or lowers the tone.
	if preventDestroy[resource] {
		c.Acknowledged = append(c.Acknowledged, "prevent_destroy")
	}
	if allowDataLoss[resource] {
		c.Acknowledged = append(c.Acknowledged, "allow_data_loss")
	}
	p.Changes = append(p.Changes, c)
}

func (p *Plan) block(reason string) {
	p.Blocked = true
	for _, r := range p.BlockedReasons {
		if r == reason {
			return
		}
	}
	p.BlockedReasons = append(p.BlockedReasons, reason)
}

func setOf(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, i := range items {
		out[i] = true
	}
	return out
}
