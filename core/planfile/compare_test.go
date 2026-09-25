// SPDX-License-Identifier: GPL-3.0-or-later

package planfile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// reviewed freezes a plan the way plan --out does, then reads it back the way
// apply does: Compare is tested on what really comes out of a file.
func reviewed(t *testing.T, p *diff.Plan) File {
	t.Helper()
	data, err := Encode(p, testMeta())
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(data, testVersion)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func messages(drifts []Drift) string {
	lines := make([]string, 0, len(drifts))
	for _, d := range drifts {
		lines = append(lines, d.String())
	}
	return strings.Join(lines, "\n")
}

func assertApplicable(t *testing.T, saved File, fresh *diff.Plan) {
	t.Helper()
	if drifts := Compare(saved, fresh); len(drifts) != 0 {
		t.Errorf("Compare() =\n%s\nwant no drift", messages(drifts))
	}
}

func assertDrift(t *testing.T, saved File, fresh *diff.Plan, want string) {
	t.Helper()
	drifts := Compare(saved, fresh)
	if len(drifts) == 0 {
		t.Fatalf("Compare() = no drift, want %q", want)
	}
	if got := messages(drifts); !strings.Contains(got, want) {
		t.Errorf("Compare() =\n%s\nwant %q", got, want)
	}
}

func TestCompareAcceptsTheSamePlan(t *testing.T) {
	assertApplicable(t, reviewed(t, tasksPlan(12)), tasksPlan(12))
}

// P4: a lower impact is covered by what was accepted.
func TestCompareAcceptsALowerCount(t *testing.T) {
	assertApplicable(t, reviewed(t, tasksPlan(12)), tasksPlan(9))
}

// A count that falls to zero downgrades its line to safe: the class changes,
// the impact only goes down.
func TestCompareAcceptsACountThatFellToZero(t *testing.T) {
	fresh := tasksPlan(0)
	fresh.Changes[0].Details[1].Class = change.ClassSafe
	assertApplicable(t, reviewed(t, tasksPlan(12)), fresh)
}

func TestCompareRefusesAHigherCount(t *testing.T) {
	assertDrift(t, reviewed(t, tasksPlan(12)), tasksPlan(15),
		`database.tasks: option "High" (property "Prio") — 12 rows reviewed, 15 rows now`)
}

// P5: an exact line that comes back as a lower bound can no longer be
// guaranteed not to exceed what was reviewed, even with a lower figure.
func TestCompareRefusesADegradedBound(t *testing.T) {
	fresh := tasksPlan(10)
	fresh.Changes[0].Details[1].Measure.Bound = change.BoundAtLeast
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		`12 rows reviewed, at least 10 rows now`)
}

// Review Focus #2: a count that fails at apply time is a degraded bound, and
// the drift says the count is the cause, so the caller can say that
// rerunning may be enough.
func TestCompareNamesACountThatFailedNow(t *testing.T) {
	fresh := tasksPlan(-1)
	fresh.Changes[0].Details[1].Class = change.ClassUnknownImpact
	fresh.Changes[0].Details[1].CountFailed = true
	drifts := Compare(reviewed(t, tasksPlan(12)), fresh)
	if len(drifts) != 1 {
		t.Fatalf("Compare() =\n%s\nwant exactly one drift", messages(drifts))
	}
	if !drifts[0].CountFailed {
		t.Errorf("CountFailed = false on %q, want true", drifts[0])
	}
	if !strings.Contains(drifts[0].String(), "12 rows reviewed, no figure now: its count did not succeed") {
		t.Errorf("drift = %q", drifts[0])
	}
}

// A drift that is not a failed count is never marked retryable: rerunning
// would not change it.
func TestCompareMarksOnlyAFailedCountAsRetryable(t *testing.T) {
	drifts := Compare(reviewed(t, tasksPlan(12)), tasksPlan(15))
	if len(drifts) == 0 {
		t.Fatal("Compare() = no drift, want the higher count")
	}
	for _, d := range drifts {
		if d.CountFailed {
			t.Errorf("CountFailed = true on %q", d)
		}
	}
}

// Ruling m3: a line with no figure now, whose count did NOT fail, is a
// drift that rerunning will not clear — the live type is no longer
// filterable, or nothing said which data source to query. It asks for a new
// plan, never for a rerun.
func TestCompareDoesNotMarkAMissingFigureAsRetryableUnlessTheCountFailed(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mark       func(d *resources.Detail)
	}{
		{"unmeasurable now", "12 rows reviewed, no figure now: it can no longer be counted",
			func(d *resources.Detail) { d.Unmeasurable = true }},
		{"not counted now", "12 rows reviewed, no figure now: it was not counted",
			func(*resources.Detail) {}},
	} {
		fresh := tasksPlan(-1)
		fresh.Changes[0].Details[1].Class = change.ClassUnknownImpact
		tc.mark(&fresh.Changes[0].Details[1])
		drifts := Compare(reviewed(t, tasksPlan(12)), fresh)
		if len(drifts) != 1 {
			t.Fatalf("%s: Compare() =\n%s\nwant exactly one drift", tc.name, messages(drifts))
		}
		if drifts[0].CountFailed {
			t.Errorf("%s: CountFailed = true on %q: rerunning will not give a figure", tc.name, drifts[0])
		}
		if !strings.Contains(drifts[0].String(), tc.want) {
			t.Errorf("%s: drift = %q, want %q", tc.name, drifts[0], tc.want)
		}
	}
}

// typeChangePlan is a measured type change of "Prio" from the given source
// type, with its count.
func typeChangePlan(from string, count int) *diff.Plan {
	d := measured("~", `property "Prio"`, change.ClassDestructive,
		resources.Measurement{Property: "Prio", PropertyType: from, TargetType: "number"},
		count, false)
	d.FromType = from
	return &diff.Plan{ToChange: 1, Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{d},
	}}}
}

// Ruling M1: the source type is part of what was reviewed. The same line,
// the same class and a lower count from another source type is another
// change: what the rows lose depends on the type they leave.
func TestCompareRefusesAChangedSourceType(t *testing.T) {
	assertApplicable(t, reviewed(t, typeChangePlan("select", 12)), typeChangePlan("select", 12))
	assertDrift(t, reviewed(t, typeChangePlan("select", 12)), typeChangePlan("multi_select", 10),
		`database.tasks: property "Prio" — type change reviewed from select, now from multi_select`)
}

// Ruling m10: a destruction's line targets the resource itself; the drift
// names it once.
func TestCompareNamesADestroyedResourceOnce(t *testing.T) {
	destroy := func(count int) *diff.Plan {
		return &diff.Plan{ToDestroy: 1, Changes: []diff.Change{{
			Resource: "database.tasks", Key: "tasks", Kind: resources.KindDestroy,
			Details: []resources.Detail{measured("-", "database.tasks", change.ClassDestructive,
				resources.Measurement{AllRows: true}, count, false)},
		}}}
	}
	drifts := Compare(reviewed(t, destroy(12)), destroy(15))
	if got, want := messages(drifts), "database.tasks: 12 rows reviewed, 15 rows now"; got != want {
		t.Errorf("Compare() =\n%s\nwant %q", got, want)
	}
}

func TestCompareRefusesAnAddedLine(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Changes[0].Details = append(fresh.Changes[0].Details,
		resources.NewDetail("+", `property "Due" (date)`, change.ClassSafe))
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		`database.tasks: line added: + property "Due" (date)`)
}

func TestCompareRefusesARemovedLine(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Changes[0].Details = fresh.Changes[0].Details[1:]
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		`database.tasks: line gone: + property "Estimate" (number)`)
}

// Two identical lines are two lines: losing one of them is a drift.
func TestCompareCountsIdenticalLinesOneByOne(t *testing.T) {
	twice := tasksPlan(12)
	twice.Changes[0].Details = append(twice.Changes[0].Details, twice.Changes[0].Details[0])
	assertDrift(t, reviewed(t, twice), tasksPlan(12),
		`database.tasks: line gone: + property "Estimate" (number)`)
}

func TestCompareRefusesAChangedClass(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Changes[0].Details[1].Class = change.ClassSilentRewrite
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		`option "High" (property "Prio") — destructive reviewed, silent rewrite now`)
}

// A line that costs nothing has no count to hold it to: its class is the
// only thing that can move, and it must not.
func TestCompareRefusesAChangedClassOnAnUncountedLine(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Changes[0].Details[0].Class = change.ClassUnknownImpact
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		`property "Estimate" (number) — safe reviewed, unknown impact now`)
}

func TestCompareRefusesAChangedWithheld(t *testing.T) {
	withheld := func(reason string) *diff.Plan {
		p := tasksPlan(12)
		p.Changes[0].Withheld = reason
		return p
	}
	assertDrift(t, reviewed(t, tasksPlan(12)), withheld("an option must be migrated by hand\n  details"),
		"database.tasks: was going to be written, is now withheld: an option must be migrated by hand")
	assertDrift(t, reviewed(t, withheld("an option must be migrated by hand")), tasksPlan(12),
		"database.tasks: was withheld, would now be written")
	assertDrift(t, reviewed(t, withheld("an option must be migrated by hand")),
		withheld("a property is refused by the API"),
		"database.tasks: withheld for another reason now: a property is refused by the API")
}

// Review Focus #3: a resource withheld in both plans, for the same reason,
// stays withheld — it is not a drift.
func TestCompareAcceptsAResourceWithheldInBothPlans(t *testing.T) {
	p := func() *diff.Plan {
		p := tasksPlan(12)
		p.Changes[0].Withheld = "an option must be migrated by hand"
		p.Changes[0].Target = nil
		return p
	}
	assertApplicable(t, reviewed(t, p()), p())
}

func TestCompareRefusesAChangedKind(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Changes[0].Kind = resources.KindDestroy
	assertDrift(t, reviewed(t, tasksPlan(12)), fresh,
		"database.tasks: kind changed: update reviewed, destroy now")
}

func TestCompareRefusesAnAddedOrAGoneChange(t *testing.T) {
	more := tasksPlan(12)
	more.Changes = append(more.Changes, diff.Change{
		Resource: "database.projects", Key: "projects", Kind: resources.KindCreate,
		Details: []resources.Detail{resources.NewDetail("+", `name "Projects"`, change.ClassSafe)},
	})
	assertDrift(t, reviewed(t, tasksPlan(12)), more, "database.projects: change added (create)")
	assertDrift(t, reviewed(t, more), tasksPlan(12), "database.projects: change gone (create)")
}

func TestCompareRefusesAChangedStaleState(t *testing.T) {
	more := tasksPlan(12)
	more.StaleState = append(more.StaleState, "database.archive")
	assertDrift(t, reviewed(t, tasksPlan(12)), more, "database.archive: stale state entry added")
	assertDrift(t, reviewed(t, more), tasksPlan(12), "database.archive: stale state entry gone")
}

// A blocked plan is never applied, and the reason of a block that appeared
// since is named.
func TestCompareRefusesABlockedPlan(t *testing.T) {
	blocked := tasksPlan(12)
	blocked.Blocked = true
	blocked.BlockedReasons = []string{"database.x is in the state but not found in Notion.\n  → restore it"}
	assertDrift(t, reviewed(t, blocked), tasksPlan(12), "the reviewed plan was blocked")
	assertDrift(t, reviewed(t, tasksPlan(12)), blocked,
		"the plan is blocked now: database.x is in the state but not found in Notion.")
}

// Spec §6: Notion changed without the plan changing — an undeclared property
// appeared, someone edited by hand. Nothing reviewed is affected: drift and
// unmanaged lines are shown, never written, and are not compared.
func TestCompareIgnoresWhatApplyDoesNotWrite(t *testing.T) {
	fresh := tasksPlan(12)
	fresh.Unmanaged = []diff.Unmanaged{{Resource: "database.tasks",
		Lines: []string{`property "Notes" (rich_text)`}}}
	fresh.Drifts = []diff.Drift{{Resource: "database.tasks",
		Lines: []string{`~ name "Tasks" → "Todo" outside notion-seed`}}}
	assertApplicable(t, reviewed(t, tasksPlan(12)), fresh)
}

// Review Focus #4: an empty plan is covered by an empty plan.
func TestCompareAcceptsAnEmptyPlanAgainstAnEmptyPlan(t *testing.T) {
	assertApplicable(t, reviewed(t, &diff.Plan{}), &diff.Plan{})
}

// boundDetail builds the measurement that makes the rendering print a bound.
func boundDetail(b Bound, count int) resources.Detail {
	m := resources.Measurement{Property: "Notes", PropertyType: "rich_text"}
	capped := false
	switch b {
	case BoundAtMost:
		m.Bound = change.BoundAtMost
	case BoundAtLeast:
		m.Bound = change.BoundAtLeast
	case BoundMoreThan:
		capped = true
	case BoundUnmeasured:
		count = -1
	}
	class := change.ClassDestructive
	if count < 0 {
		class = change.ClassUnknownImpact
	}
	return measured("~", `property "Notes" (rich_text → number)`, class, m, count, capped)
}

func boundPlan(b Bound, count int) *diff.Plan {
	return &diff.Plan{ToChange: 1, Changes: []diff.Change{{
		Resource: "database.tasks", Key: "tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{boundDetail(b, count)},
	}}}
}

// Spec §4, every cell of the bound table, each with a recomputed count
// below, equal to and above the reviewed one. The expected verdicts are
// written out cell by cell, not derived from the code's rank: the table is
// the spec, and the test must not share the implementation's reasoning.
//
//	"cmp":    applicable iff M ≤ n
//	"refuse": refused whatever M (degraded bound)
//	"accept": applicable whatever M
func TestCompareFollowsTheBoundTable(t *testing.T) {
	bounds := []Bound{BoundExact, BoundAtMost, BoundAtLeast, BoundMoreThan, BoundUnmeasured}
	table := map[Bound]map[Bound]string{
		BoundExact: {
			BoundExact: "cmp", BoundAtMost: "refuse", BoundAtLeast: "refuse",
			BoundMoreThan: "refuse", BoundUnmeasured: "refuse",
		},
		BoundAtMost: {
			BoundExact: "cmp", BoundAtMost: "cmp", BoundAtLeast: "refuse",
			BoundMoreThan: "refuse", BoundUnmeasured: "refuse",
		},
		BoundAtLeast: {
			BoundExact: "cmp", BoundAtMost: "cmp", BoundAtLeast: "cmp",
			BoundMoreThan: "cmp", BoundUnmeasured: "refuse",
		},
		BoundMoreThan: {
			BoundExact: "cmp", BoundAtMost: "cmp", BoundAtLeast: "cmp",
			BoundMoreThan: "cmp", BoundUnmeasured: "refuse",
		},
		BoundUnmeasured: {
			BoundExact: "accept", BoundAtMost: "accept", BoundAtLeast: "accept",
			BoundMoreThan: "accept", BoundUnmeasured: "accept",
		},
	}
	const n = 12
	for _, r := range bounds {
		for _, nb := range bounds {
			for _, m := range []int{n - 1, n, n + 1} {
				t.Run(fmt.Sprintf("%s %d / %s %d", r, n, nb, m), func(t *testing.T) {
					verdict := table[r][nb]
					want := verdict == "accept" || verdict == "cmp" && m <= n
					drifts := Compare(reviewed(t, boundPlan(r, n)), boundPlan(nb, m))
					if got := len(drifts) == 0; got != want {
						t.Errorf("applicable = %v, want %v (cell %q)\n%s", got, want, verdict, messages(drifts))
					}
				})
			}
		}
	}
}

func TestCheckLinksNamesEachLinkThatChanged(t *testing.T) {
	saved := reviewed(t, tasksPlan(12))
	if drifts := CheckLinks(saved, testMeta()); len(drifts) != 0 {
		t.Errorf("CheckLinks() =\n%s\nwant no drift", messages(drifts))
	}

	now := testMeta()
	now.WorkspaceID = "99999999-9999-4999-8999-999999999999"
	now.ConfigSHA256 = "other-config"
	now.StateSHA256 = "other-state"
	got := messages(CheckLinks(saved, now))
	for _, want := range []string{
		"workspace changed since the plan: planned on 33333333-3333-4333-8333-333333333333, " +
			"ntn is now authenticated on 99999999-9999-4999-8999-999999999999",
		"configuration changed since the plan: the branch being applied is not the one " +
			"that was planned — plan again on the merged branch",
		"state changed since the plan: another apply went through in between",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CheckLinks() =\n%s\nwant %q", got, want)
		}
	}
}

// The creation date and the rendered text are not links: a plan applied an
// hour later, or whose text a CI reflowed, is the same plan.
func TestCheckLinksIgnoresTheDateAndTheText(t *testing.T) {
	now := testMeta()
	now.CreatedAt = now.CreatedAt.Add(3 * time.Hour)
	now.Rendered = "something else"
	if drifts := CheckLinks(reviewed(t, tasksPlan(12)), now); len(drifts) != 0 {
		t.Errorf("CheckLinks() =\n%s\nwant no drift", messages(drifts))
	}
}
