// SPDX-License-Identifier: GPL-3.0-or-later

// Package planfile freezes a plan into a file, and tells whether a plan
// recomputed later is still covered by the one that was reviewed.
//
// The file is not replayed: apply always recomputes, and writes through its
// one write path. The file is the CONTRACT the recomputed plan is held to —
// same resources, same lines, no impact beyond what was reviewed. It carries
// nothing more than the plan shows: no payload, no token, no row content, so
// it can live in a CI artifact.
//
// The package has no CLI and no network: Encode and Decode are pure.
package planfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Format is the version of the file format. A file of another format is
// refused, never read on a best-effort basis: a partial read would compare the
// recomputed plan to a plan nobody reviewed.
const Format = 1

// Bound says how a line's count relates to the rows it really touches: what
// the reviewer could rely on when accepting the figure.
type Bound string

const (
	// BoundNone: the line carries no measurement request, it costs nothing.
	// No count is written.
	BoundNone Bound = ""
	// BoundExact: the rendering shows "N rows".
	BoundExact Bound = "exact"
	// BoundAtMost: "up to N rows".
	BoundAtMost Bound = "at_most"
	// BoundAtLeast: "at least N rows" — the filter can miss rows, or a
	// destruction's count covers only one of its data sources.
	BoundAtLeast Bound = "at_least"
	// BoundMoreThan: "more than N rows" — the pagination cap was reached.
	BoundMoreThan Bound = "more_than"
	// BoundUnmeasured: no figure — the count failed, was not attempted, or
	// cannot be asked. No count is written.
	BoundUnmeasured Bound = "unmeasured"
)

// Meta is what the file says about the moment it was computed. The CLI
// fills it: planfile knows neither the version nor the clock.
type Meta struct {
	// Version is the notion-seed version that computed the plan.
	Version string
	// WorkspaceID is the workspace ntn was authenticated on.
	WorkspaceID string
	// ConfigSHA256 is config.Fingerprint of the configuration planned.
	ConfigSHA256 string
	// StateSHA256 is state.Fingerprint of the state planned against.
	StateSHA256 string
	CreatedAt   time.Time
	// Rendered is the plan text as printed, for a CI to post without
	// running notion-seed again.
	Rendered string
}

// File is a decoded plan file.
type File struct {
	Format       int       `json:"format"`
	NotionSeed   string    `json:"notion_seed"`
	WorkspaceID  string    `json:"workspace_id"`
	CreatedAt    time.Time `json:"created_at"`
	ConfigSHA256 string    `json:"config_sha256"`
	StateSHA256  string    `json:"state_sha256"`
	// Blocked says the reviewed plan was blocked. Such a plan is never
	// applied: what was reviewed was not something apply would have written.
	Blocked    bool     `json:"blocked,omitempty"`
	Changes    []Change `json:"changes"`
	StaleState []string `json:"stale_state"`
	Rendered   string   `json:"rendered"`
}

// Change is one resource of the reviewed plan.
type Change struct {
	Resource string `json:"resource"`
	// Kind is "create", "update" or "destroy".
	Kind string `json:"kind"`
	// Withheld is the reason apply will not write this resource, "" if it
	// will.
	Withheld string `json:"withheld"`
	Details  []Line `json:"details"`
}

// Line is one detail line of a reviewed change: what it does, to what, what
// it costs.
type Line struct {
	Op     string `json:"op"`
	Target string `json:"target"`
	// Class is the class as the plan prints it: "safe", "destructive", …
	Class string `json:"class"`
	// Count is the figure the plan printed, nil when it printed none
	// (BoundNone, BoundUnmeasured).
	Count *int  `json:"count,omitempty"`
	Bound Bound `json:"bound,omitempty"`
	// From is the property's type before a type change, "" on every other
	// line. Note is not compared (it is free text): From is what a
	// recomputed plan is held to, on the one detail whose source type
	// matters — a type change.
	From string `json:"from,omitempty"`
}

// Encode freezes a plan. The output is deterministic for a given plan and
// Meta: indented, fields in a fixed order, ending with a newline.
func Encode(p *diff.Plan, meta Meta) ([]byte, error) {
	f := File{
		Format:       Format,
		NotionSeed:   meta.Version,
		WorkspaceID:  meta.WorkspaceID,
		CreatedAt:    meta.CreatedAt.UTC(),
		ConfigSHA256: meta.ConfigSHA256,
		StateSHA256:  meta.StateSHA256,
		Blocked:      p.Blocked,
		Changes:      changesOf(p),
		StaleState:   append([]string{}, p.StaleState...),
		Rendered:     meta.Rendered,
	}
	body, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to serialize the plan: %w\n"+
			"  → this is a notion-seed bug, not a configuration error: report it", err)
	}
	return append(body, '\n'), nil
}

// Decode reads a plan file written by Encode, and refuses one another version
// of notion-seed wrote: another version can classify or count differently,
// and the same plan would no longer mean the same thing.
//
// The format is checked BEFORE anything else is decoded: a file of an unknown
// format is named as such, not as a pile of unknown fields.
func Decode(data []byte, version string) (File, error) {
	const replan = "  → rerun `notion-seed plan --out` with this version, and have the new plan reviewed"

	var probe struct {
		Format     *int   `json:"format"`
		NotionSeed string `json:"notion_seed"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return File{}, fmt.Errorf("unreadable plan file: %v\n%s", err, replan)
	}
	if probe.Format == nil {
		return File{}, fmt.Errorf("not a notion-seed plan file: it carries no format\n%s", replan)
	}
	if *probe.Format != Format {
		return File{}, fmt.Errorf(
			"plan file format %d is unknown to notion-seed %s, which reads format %d\n%s",
			*probe.Format, version, Format, replan)
	}
	if probe.NotionSeed != version {
		return File{}, fmt.Errorf(
			"the plan was computed by notion-seed %s, this is notion-seed %s: "+
				"another version can classify or count differently\n%s",
			probe.NotionSeed, version, replan)
	}

	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("unreadable plan file: %v\n%s", err, replan)
	}
	if err := validate(f); err != nil {
		return File{}, fmt.Errorf("unreadable plan file: %v\n%s", err, replan)
	}
	return f, nil
}

// validate refuses a file Encode could not have written. Its point is the
// bound: an unknown bound, or a counted bound without its count, would leave
// Compare nothing to hold the recomputed line to.
func validate(f File) error {
	for _, c := range f.Changes {
		switch c.Kind {
		case "create", "update", "destroy":
		default:
			return fmt.Errorf("%s: unknown kind %q", c.Resource, c.Kind)
		}
		for _, l := range c.Details {
			switch l.Bound {
			case BoundNone, BoundUnmeasured:
				if l.Count != nil {
					return fmt.Errorf("%s: %s carries a count its bound %q does not allow",
						c.Resource, l.Target, l.Bound)
				}
			case BoundExact, BoundAtMost, BoundAtLeast, BoundMoreThan:
				if l.Count == nil || *l.Count < 0 {
					return fmt.Errorf("%s: %s is bound %q without a count",
						c.Resource, l.Target, l.Bound)
				}
			default:
				return fmt.Errorf("%s: %s carries an unknown bound %q", c.Resource, l.Target, l.Bound)
			}
		}
	}
	return nil
}

// changesOf is the reviewable form of a plan's changes. Encode writes it and
// Compare recomputes it: one conversion for both sides, so that a line can
// never differ only because it was converted twice.
func changesOf(p *diff.Plan) []Change {
	out := make([]Change, 0, len(p.Changes))
	for _, c := range p.Changes {
		ch := Change{
			Resource: c.Resource,
			Kind:     kindName(c.Kind),
			Withheld: c.Withheld,
			Details:  make([]Line, 0, len(c.Details)),
		}
		for _, d := range c.Details {
			ch.Details = append(ch.Details, lineOf(d))
		}
		out = append(out, ch)
	}
	return out
}

func kindName(k resources.ChangeKind) string {
	switch k {
	case resources.KindCreate:
		return "create"
	case resources.KindUpdate:
		return "update"
	case resources.KindDestroy:
		return "destroy"
	}
	return "none"
}

// lineOf converts a detail. The bound says what the figure guarantees, as the
// measurement pass knows it — the rendering words the same facts:
//   - a capped count is a floor, whatever its filter: "more than";
//   - a destruction that leaves data sources uncounted, or a filter that can
//     miss rows, is a floor too: "at least";
//   - a filter that can count rows that survive is a ceiling: "up to";
//   - otherwise the figure is exact.
//
// From carries the detail's FromType as-is: it is set by threeway on a
// type-change detail ONLY (see resources.Detail.FromType), including a safe
// pair, where Measure stays nil — so From cannot be derived from Measure.
func lineOf(d resources.Detail) Line {
	l := Line{Op: d.Op, Target: d.Target, Class: d.Class.String(), From: d.FromType}
	m := d.Measure
	switch {
	case m == nil:
		return l
	case d.Count < 0:
		l.Bound = BoundUnmeasured
		return l
	case d.Capped:
		l.Bound = BoundMoreThan
	case m.AllRows && m.UncountedDataSources > 0, m.Bound == change.BoundAtLeast:
		l.Bound = BoundAtLeast
	case m.Bound == change.BoundAtMost:
		l.Bound = BoundAtMost
	default:
		l.Bound = BoundExact
	}
	n := d.Count
	l.Count = &n
	return l
}
