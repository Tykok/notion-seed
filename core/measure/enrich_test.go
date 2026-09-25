// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

type counterFunc func(ctx context.Context, r Request) (Result, error)

func (f counterFunc) Count(ctx context.Context, r Request) (Result, error) { return f(ctx, r) }

func planWithRemoval(propType, option string) *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Details: []resources.Detail{{
			Op: "-", Target: `option "` + option + `"`,
			Class: change.ClassUnknownImpact, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: propType, Option: option,
			},
		}},
	}}}
}

// 0 rows affected: the removal costs nothing, and the line becomes safe. That
// is the whole point of measuring rather than refusing.
func TestEnrichMakesARemovalSafeWhenNoRowUsesTheOption(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 0}, nil })

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Fatalf("failures = %v, want none", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Count != 0 || d.Class != change.ClassSafe {
		t.Errorf("Detail = {Count:%d Class:%v}, want {0 safe}", d.Count, d.Class)
	}
}

// 47 rows on a status: silent rewrite, with the number.
func TestEnrichClassifiesAStatusRemovalWithRowsAsSilentRewrite(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 47}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	d := p.Changes[0].Details[0]
	if d.Count != 47 || d.Class != change.ClassSilentRewrite {
		t.Errorf("Detail = {Count:%d Class:%v}, want {47 silent rewrite}", d.Count, d.Class)
	}
}

// A failed measurement does not fail the plan: the line stays unknown, the
// cause is returned, and the rest of the plan is rendered anyway.
func TestEnrichKeepsGoingWhenAMeasurementFails(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, errors.New("403 restricted_resource")
	})

	fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if len(fails) != 1 || !strings.Contains(fails[0], "403") {
		t.Errorf("failures = %v, want the cause", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Count != -1 || d.Class != change.ClassUnknownImpact {
		t.Errorf("Detail = {Count:%d Class:%v}, want unknown", d.Count, d.Class)
	}
}

// A non-filterable type is not a failure to report as an outage: it is a
// question notion-seed cannot ask. The line stays unknown.
func TestEnrichLeavesUnfilterableTypesUnknown(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, ErrUnsupportedFilter
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact", d.Class)
	}
}

// ... and it produces NO failure line: nothing is down. Adding them to the
// same list as the 403s and the timeouts would make "count failed" show up on
// every plan carrying a non-filterable type, and would teach users to ignore a
// line that does report real incidents.
func TestEnrichReportsNoFailureForAnUnfilterableType(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		// Wrapped, as filterFor actually wraps it: errors.Is must decide, not an
		// equality comparison.
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedFilter, "people")
	})

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Errorf("failures = %v, want none: not knowing how to ask the question is not an outage", fails)
	}
	// The line's behavior, however, does not change: it is still unknown.
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact || d.Count != -1 {
		t.Errorf("Detail = {Count:%d Class:%v}, want unknown", d.Count, d.Class)
	}
}

// The measurement pass is the one that KNOWS which types it can filter, so it
// is the one that marks the line. The rendering cannot deduce it: the list of
// filterable types lives here, and core/measure imports core/diff — the
// reverse would create a cycle, and copying the table would make it diverge.
//
// Without this mark, the rendering promises "rerun online to get it" on a line
// that will never be counted.
func TestEnrichMarksAnUnfilterableTypeAsUnmeasurable(t *testing.T) {
	p := planWithRemoval("people", "Quelqu'un")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsupportedFilter, "people")
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; !d.Unmeasurable {
		t.Error("Unmeasurable = false: a non-filterable type will not become filterable on the next run")
	}
}

// An outage, on the other hand, IS NOT an impossibility: a 403 can be fixed,
// and the line must stay merely unmeasured so the rendering keeps its remedy.
// Confusing the two would make "rerun" disappear where rerunning works.
func TestEnrichDoesNotMarkAFailedMeasurementAsUnmeasurable(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		return Result{}, errors.New("403 restricted_resource")
	})

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Unmeasurable {
		t.Error("Unmeasurable = true on an outage: a 403 is fixed by rerunning")
	}
}

// No measurement request: no call. Not paying for calls for nothing is a
// property, not an optimization.
func TestEnrichEmitsNoCallWhenNothingNeedsMeasuring(t *testing.T) {
	p := &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Details: []resources.Detail{{Op: "+", Target: `property "X"`, Count: -1}},
	}}}
	c := counterFunc(func(context.Context, Request) (Result, error) {
		t.Error("no measurement should have been requested")
		return Result{}, nil
	})
	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
}

// Without a data source id (resource never imported), no measurement is
// possible: the line stays unknown rather than triggering a call on an empty
// id.
func TestEnrichSkipsResourcesWithoutADataSourceID(t *testing.T) {
	p := planWithRemoval("status", "Annulé")
	c := counterFunc(func(context.Context, Request) (Result, error) {
		t.Error("no call should have been made without a data source id")
		return Result{}, nil
	})
	Enrich(context.Background(), c, map[string]string{}, p)
	if d := p.Changes[0].Details[0]; d.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact", d.Class)
	}
}

// planWithOptionMigration builds an option rename or color change: the class
// is ClassMigration BEFORE any measurement, and the line still carries a
// measurement request — the one for the cost of the remedy (removing the old
// option).
func planWithOptionMigration() *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Class: change.ClassMigration,
		Details: []resources.Detail{{
			Op: "~", Target: `option "Fait" → "Terminé" (property "Statut")`,
			Class: change.ClassMigration, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Fait",
			},
		}},
	}}}
}

// An option rename or color change is already not expressible BEFORE any
// measurement: ClassifyOptionRemoval must therefore never reclassify a
// ClassMigration line, whatever the count — only the COST of the remedy must
// be filled in. Without this guard, a rename on a property holding rows
// (non-zero count) fell back to ClassDestructive/ClassSilentRewrite, and a
// rename on an empty column (zero count) fell back to ClassSafe: in both
// cases, `--fail-on=migration` stopped triggering.
func TestEnrichKeepsMigrationClassAndFillsCount(t *testing.T) {
	p := planWithOptionMigration()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 12}, nil })

	if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
		t.Fatalf("failures = %v, want none", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Class != change.ClassMigration {
		t.Errorf("Class = %v, want ClassMigration: the measurement does not reclassify a line that is already not expressible", d.Class)
	}
	if d.Count != 12 {
		t.Errorf("Count = %d, want 12: the cost of the remedy must be filled in", d.Count)
	}
	if got := p.Changes[0].Class; got != change.ClassMigration {
		t.Errorf("header Class = %v, want ClassMigration", got)
	}
}

// planWithTypeChange builds a type change: the measurement covers the whole
// column (empty Option), not an option.
func planWithTypeChange() *diff.Plan {
	return &diff.Plan{Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks",
		Class: change.ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Tags" — multi_select → select`,
			Class: change.ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select"},
		}},
	}}}
}

// An EMPTY column that changes type costs nothing, exactly like an option
// nobody holds. Without this downgrade, the line contradicts itself — "silent
// rewrite" followed by "0 rows affected" — and `plan --fail-on=silent-rewrite`
// exits non-zero on a column with no data at all to lose.
func TestEnrichMakesATypeChangeSafeWhenTheColumnIsEmpty(t *testing.T) {
	p := planWithTypeChange()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 0}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Count != 0 || d.Class != change.ClassSafe {
		t.Errorf("Detail = {Count:%d Class:%v}, want {0 safe}", d.Count, d.Class)
	}
	if got := p.Changes[0].Class; got != change.ClassSafe {
		t.Errorf("header Class = %v, want safe", got)
	}
}

// A NON-zero count, on the other hand, leaves the type change's class intact:
// the count gives the scale, the measured table gives the nature. Nothing in
// "12 non-empty rows" makes a silent rewrite any less silent.
func TestEnrichKeepsTheTableClassWhenATypeChangeHasRows(t *testing.T) {
	p := planWithTypeChange()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 12}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Count != 12 || d.Class != change.ClassSilentRewrite {
		t.Errorf("Detail = {Count:%d Class:%v}, want {12 silent rewrite}", d.Count, d.Class)
	}
}

// Under a type change, a status option that is not redeclared disappears and
// its rows lose their value: it is a loss, not a reassignment. The measurement
// reclassifies it as destructive, whatever the old type.
func TestEnrichClassifiesARetypedStatusOptionAsDestructive(t *testing.T) {
	p := planWithRemoval("status", "Fait")
	p.Changes[0].Details[0].Measure.Retyped = true
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 2}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	d := p.Changes[0].Details[0]
	if d.Count != 2 || d.Class != change.ClassDestructive {
		t.Errorf("Detail = {Count:%d Class:%v}, want {2 destructive}", d.Count, d.Class)
	}
}

func planWithDestroy() *diff.Plan {
	return &diff.Plan{ToDestroy: 1, Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks", Kind: resources.KindDestroy,
		Class: change.ClassDestructive,
		Details: []resources.Detail{{
			Op: "-", Target: "database.tasks", Class: change.ClassDestructive, Count: -1,
			Measure: &resources.Measurement{AllRows: true},
		}},
	}}}
}

// A destruction stays destructive whatever its count: 0 rows does not make it
// safe (the database goes anyway), and a count does not change its nature. The
// count only says what goes with it.
func TestEnrichCountsTheRowsOfADestroyWithoutReclassifyingIt(t *testing.T) {
	for _, n := range []int{0, 5} {
		p := planWithDestroy()
		var got Request
		c := counterFunc(func(_ context.Context, r Request) (Result, error) {
			got = r
			return Result{Count: n}, nil
		})
		if fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p); len(fails) != 0 {
			t.Fatalf("failures = %v", fails)
		}
		if !got.AllRows || got.DataSourceID != "ds-1" {
			t.Errorf("request = %+v, want AllRows on ds-1", got)
		}
		d := p.Changes[0].Details[0]
		if d.Count != n || d.Class != change.ClassDestructive || p.Changes[0].Class != change.ClassDestructive {
			t.Errorf("n=%d: Detail = {Count:%d Class:%v}, resource %v, want destructive",
				n, d.Count, d.Class, p.Changes[0].Class)
		}
	}
}

// A failed count leaves the count unknown, the class intact, and its cause
// returned.
func TestEnrichKeepsADestroyDestructiveWhenCountingFails(t *testing.T) {
	p := planWithDestroy()
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{}, errors.New("403") })
	fails := Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if len(fails) != 1 {
		t.Errorf("failures = %v, want one", fails)
	}
	d := p.Changes[0].Details[0]
	if d.Count != -1 || d.Class != change.ClassDestructive {
		t.Errorf("Detail = {Count:%d Class:%v}, want {-1 destructive}", d.Count, d.Class)
	}
}

// A lower bound of zero proves nothing: text made only of spaces escapes
// is_not_empty and is lost too. Downgrading to safe would announce "nothing
// to lose" on a count that cannot say so — the worst bug this product can
// have.
func TestEnrichNeverMakesALowerBoundSafe(t *testing.T) {
	p := planWithTypeChange()
	d := &p.Changes[0].Details[0]
	d.Class = change.ClassDestructive
	d.Measure = &resources.Measurement{
		Property: "Notes", PropertyType: "rich_text", Bound: change.BoundAtLeast,
	}
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 0}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if got := p.Changes[0].Details[0]; got.Count != 0 || got.Class != change.ClassDestructive {
		t.Errorf("Detail = {Count:%d Class:%v}, want {0 destructive}", got.Count, got.Class)
	}
}

// The request carries the type change's filter as the comparator built it.
func TestEnrichPassesTheTypeChangeFilterThrough(t *testing.T) {
	p := planWithTypeChange()
	p.Changes[0].Details[0].Measure = &resources.Measurement{
		Property: "Notes", PropertyType: "rich_text", TargetType: "status",
		Count: change.CountEveryRow, Except: []string{"Un"},
	}
	var got Request
	c := counterFunc(func(_ context.Context, r Request) (Result, error) {
		got = r
		return Result{Count: 3}, nil
	})
	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if got.Count != change.CountEveryRow || strings.Join(got.Except, ",") != "Un" {
		t.Errorf("request = %+v, want every row except Un", got)
	}
}

// Toward status, an option that is not redeclared does not empty its rows:
// measured on 2026-09-25, they get the first declared option. It is a silent
// rewrite, whatever the old type.
func TestEnrichClassifiesAnOptionRetypedToStatusAsSilentRewrite(t *testing.T) {
	p := planWithRemoval("select", "Moyenne")
	p.Changes[0].Details[0].Measure.Retyped = true
	p.Changes[0].Details[0].Measure.TargetType = "status"
	c := counterFunc(func(context.Context, Request) (Result, error) { return Result{Count: 2}, nil })

	Enrich(context.Background(), c, map[string]string{"tasks": "ds-1"}, p)
	if d := p.Changes[0].Details[0]; d.Class != change.ClassSilentRewrite {
		t.Errorf("Class = %v, want silent rewrite", d.Class)
	}
}
