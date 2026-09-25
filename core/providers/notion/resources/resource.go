// SPDX-License-Identifier: GPL-3.0-or-later

// Package resources holds the Notion resource types managed by notion-seed.
// The diff engine is written against the Resource interface: adding a type
// must not change it.
package resources

import (
	"context"

	"github.com/tykok/notion-seed/core/change"
)

// RemoteState is the actual state of a resource, read from the API.
type RemoteState interface {
	Exists() bool
}

// ChangeKind classifies a changeset at the resource level.
type ChangeKind int

const (
	KindNone ChangeKind = iota
	KindCreate
	KindUpdate
	// KindDestroy: the resource is in the state but no longer in the config.
	// Its identity no longer has a declared anchor.
	KindDestroy
)

// Measurement describes what has to be counted to know what a detail costs.
//
// The comparator EMITS it without running it: it stays pure, with no network
// and no clock, and that is what makes it coverable as a table over triples.
// A separate pass runs the requests and reclassifies.
//
// An empty Option means "count the non-empty values of the column", which is
// what a type change needs.
//
// Retyped says the option disappears because its property changes type, and
// not because it is removed from a list that stays. The fate of the rows is
// not the same: measured on 2026-09-25 on select → multi_select, the row loses
// its value for lack of an option with the same name in the payload, where a
// plain status option removal would have reassigned it. PropertyType stays the
// OLD type: it is the one that filters the rows, since counting happens before
// writing.
//
// AllRows asks to count ALL the rows of the data source, without a filter: it
// is what a database moved to the trash takes with it. Property, PropertyType
// and Option then stay empty.
type Measurement struct {
	Property     string
	PropertyType string
	Option       string
	Retyped      bool
	AllRows      bool
	// UncountedDataSources, with AllRows, is the number of the database's data
	// sources the count does not query: it counts only one, the trashing takes
	// them all. When non-zero, the count is a lower bound.
	UncountedDataSources int

	// TargetType is the NEW type, on a type change and on the removal lines
	// it brings: toward status, a value that is not redeclared is reassigned
	// to the first option instead of emptied (measured on 2026-09-25).
	TargetType string
	// Count, Bound, Except and Caveat describe a type change's count, as
	// change.TypeChangeOf gives it: which rows to filter, how the figure
	// relates to the rows really touched, which declared option names are
	// excluded, and why the figure is only a bound or cannot be taken.
	Count  change.Count
	Bound  change.Bound
	Except []string
	Caveat string
}

// Detail describes an elementary change inside a resource.
type Detail struct {
	Op     string // "+", "~", "-"
	Target string // `property "Estimate" (number)`
	Note   string // optional precision
	Class  change.Class

	// Property names the property this detail concerns. Empty on a
	// database-level detail.
	//
	// This field is what makes the WRITE SET: apply sends only the properties
	// that carry at least one detail, so what is written is exactly what is
	// shown. Without it, apply would have to re-read the text of the lines or
	// recompute a diff in parallel — a second path able to diverge from the
	// plan silently.
	Property string

	// Field names the database attribute this detail concerns: "name",
	// "description" or "icon". Empty on a property-level detail. Property and
	// Field are exclusive: a detail concerns one or the other, never both.
	Field string

	// Measure is the measurement request, nil when the detail costs nothing.
	Measure *Measurement
	// Count is the number of rows affected. -1 as long as nothing has been
	// measured: neither 0 nor a count, but "unknown".
	Count int
	// Capped says the pagination cap was reached and that Count is therefore a
	// lower bound.
	Capped bool

	// Unmeasurable says notion-seed cannot ASK the question: the property's
	// type is not filterable, and it will not become so on the next run. It
	// is an impossibility, not an outage.
	//
	// The distinction exists for the rendering, and it is substantive: a
	// measurement that simply was not made (--skip-preflight, 403, 429) is
	// fixed by rerunning, and the rendering can promise it; an impossible
	// measurement cannot be fixed, and promising it would announce a
	// corrective action that will never come.
	//
	// Only core/measure sets this field: it is the one that knows the
	// filterable types, and it alone. The rendering cannot deduce it without
	// copying that table or creating an import cycle.
	//
	// The default value, false, means "measurable" — the majority case, and
	// the CAUTIOUS one: a detail left at false by mistake falls back to "not
	// measured", which offers to rerun, at worst an empty promise. A detail
	// wrongly marked true would instead make a working remedy disappear. So
	// the default leans the right way.
	Unmeasurable bool
}

// NewDetail builds an unmeasured detail. Use it SYSTEMATICALLY: a Detail
// composed by hand carries Count = 0, hence "no rows affected", hence "safe"
// — a claim nobody verified.
//
// Exported because the comparator, in the diff package, produces most of the
// repository's details: an unexported function would have left it without a
// safeguard, the one place where one is really needed.
//
// It does NOT take Note nor the three measurement fields: a six-argument
// constructor would be less readable than the literal it replaces. Details
// that carry a measurement therefore stay literals, with Count: -1 written
// explicitly. The net that catches an oversight is not this constructor but
// TestCompareDatabaseNeverEmitsAnUnmeasuredZeroCount, in core/diff.
func NewDetail(op, target string, class change.Class) Detail {
	return Detail{Op: op, Target: target, Class: class, Count: -1}
}

// Changeset groups the changes of a resource.
type Changeset struct {
	Resource string // "database.projects"
	Kind     ChangeKind
	Details  []Detail
}

// Resource is the contract each Notion resource type fulfills.
//
// Apply and Destroy are absent in MVP 0: the read-modify-write the API
// imposes constrains their signature, and it will only be known by actually
// writing. ID(state) is absent too: it depends on the state model, out of
// MVP 0 scope.
type Resource interface {
	Type() string
	Read(ctx context.Context, id string) (RemoteState, error)
	Diff(desired any, remote RemoteState) (Changeset, error)
}
