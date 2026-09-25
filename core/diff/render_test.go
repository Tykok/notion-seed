// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

func TestRenderEmptyPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, &Plan{}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "No changes") {
		t.Errorf("output = %q, want an explicit mention that nothing changes", got)
	}
}

// C1: a managed database that is not found or archived stacks a BlockedReason
// without ever touching Changes or Unmanaged. Before the fix, Render printed
// "No changes. The configuration matches the actual state." here on stdout,
// together with an error code — stdout asserted the opposite of stderr.
func TestRenderBlockedPlanNeverSaysNoChange(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.tasks is in the state but not found (404) in Notion.\n" +
				"  → restore it in Notion, or remove its entry from notion-seed.state.json",
		},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "No changes") {
		t.Errorf("a blocked plan must never show \"No changes\":\n%s", got)
	}
	if !strings.Contains(got, "Plan blocked") {
		t.Errorf("output = %q, it must show the block section", got)
	}
	if !strings.Contains(got, "not found (404)") {
		t.Errorf("output = %q, it must name the reason of the block", got)
	}
}

// C2: --skip-preflight never reads the actual state of a resource the state
// anchors. Compute then fills NotCompared instead of letting an entirely empty
// Plan pass for an observed match.
func TestRenderNotComparedBlocksNoChangeMessage(t *testing.T) {
	p := &Plan{NotCompared: []string{"database.tasks"}}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "matches the actual state") {
		t.Errorf("a resource not compared must never show a match:\n%s", got)
	}
	if !strings.Contains(got, "Not compared") || !strings.Contains(got, "--skip-preflight") {
		t.Errorf("output = %q, it must name the mode that prevented the comparison", got)
	}
	if !strings.Contains(got, "database.tasks") {
		t.Errorf("output = %q, it must name the resource not compared", got)
	}
}

// I3: Note already carries its own quotes where they are needed (a property
// rename warns with `"Old" is not renamed...`). Before the fix, flattening
// re-escaped the whole note with %q, producing unreadable backslashes —
// precisely on the line meant to keep the user from thinking they renamed a
// property. The concatenation now lives in Render, and the other rendering
// tests build Details without a Note: this one alone, going through Compute
// then Render, really exercises it.
func TestRenderNoteWithQuotesIsNotReEscaped(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{{
		Key: "tasks", Name: "Tasks",
		Properties: map[string]config.Property{"Charge": {Type: "number"}},
	}}}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number"},
		}},
	}}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{
		ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number"},
		},
	}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if strings.Contains(got, `\"`) {
		t.Errorf("the note was re-escaped (backslashes): %q", got)
	}
	if !strings.Contains(got, `"Estimate" is not renamed`) {
		t.Errorf("the note must stay readable with its original quotes:\n%s", got)
	}
	if !strings.Contains(got, " — ") {
		t.Errorf("the separator must be \" — \", not \": \":\n%s", got)
	}
}

func TestRenderCreatePlan(t *testing.T) {
	p := &Plan{
		ToAdd: 1,
		Changes: []Change{{
			Class:    ClassSafe,
			Resource: "database.tasks",
			Detail:   "(new)",
			Kind:     resources.KindCreate,
			Details: []resources.Detail{
				{Op: "+", Target: `property "Estimate" (number)`, Class: ClassSafe, Count: -1},
				{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
			},
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"Plan: 1 to add, 0 to change, 0 to destroy",
		"+ database.tasks (new)",
		`property "Estimate" (number)`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain %q\n--- output ---\n%s", want, got)
		}
	}
}

// Since notion-seed no longer blocks on the strength of a Class (see
// core/change), the header marker follows the resource's Kind — what the
// operation DOES, not what it costs. Without this test, swapping the two
// `case`s of Render's switch, or mapping KindDestroy to "+" by a copy-paste
// mistake, would stay invisible: go test ./... would pass anyway, since only
// the creation case was covered by TestRenderCreatePlan.
func TestRenderDestroyMarksResourceWithMinus(t *testing.T) {
	p := &Plan{
		ToDestroy: 1,
		Changes: []Change{{
			Resource: "database.tasks",
			Kind:     resources.KindDestroy,
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	header := findLineContaining(t, buf.String(), "database.tasks")
	if !strings.HasPrefix(header, "  - ") {
		t.Errorf("header = %q, want a \"-\" marker (destruction)", header)
	}
}

// Mirror of the test above, on the update side: without it, a Kind other than
// creation or destruction could slide onto any marker without any test
// noticing.
func TestRenderUpdateMarksResourceWithTilde(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.tasks",
			Kind:     resources.KindUpdate,
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	header := findLineContaining(t, buf.String(), "database.tasks")
	if !strings.HasPrefix(header, "  ~ ") {
		t.Errorf("header = %q, want a \"~\" marker (update)", header)
	}
}

// findLineContaining returns the first line of out that contains sub, to
// isolate a resource's header from the rest of the rendering without depending
// on its exact content beyond the marker.
func findLineContaining(t *testing.T, out, sub string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	t.Fatalf("no line contains %q in:\n%s", sub, out)
	return ""
}

func TestRenderMarksBlockingChanges(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Blocked:  true,
		Changes: []Change{{
			Class:    ClassSilentRewrite,
			Resource: "database.tasks",
			Details: []resources.Detail{
				{Op: "-", Target: `option "Shipped" of status "Status"`,
					Class: ClassSilentRewrite, Count: -1},
			},
		}},
	}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "silent rewrite") {
		t.Errorf("output = %q, it must name the class of the change", got)
	}
	if !strings.Contains(got, "blocked") {
		t.Errorf("output = %q, it must say the plan is blocked", got)
	}
}

// The rendering goes to stdout as plain text: no unconditional color, no
// escape sequence, the output must stay usable in CI and in a pipe.
func TestRenderIsPlainText(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Class: ClassSafe, Resource: "database.tasks", Detail: "(new)",
	}}}
	var buf bytes.Buffer
	if err := Render(&buf, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Error("the output contains an ANSI escape sequence")
	}
}

func TestRenderShowsDriftBeforePlan(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Drifts: []Drift{{Resource: "database.tasks", Lines: []string{
			`~ option "Fait" of property "Statut" renamed to "Terminé" outside notion-seed`,
		}}},
		Changes: []Change{{
			Resource: "database.tasks",
			Class:    ClassMigration,
			Details: []resources.Detail{{
				Op:     "~",
				Target: `option "Terminé" → "Fait" (property "Statut")`,
				Class:  ClassMigration,
				Count:  -1,
			}},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	out := b.String()

	iDrift := strings.Index(out, "Drift detected outside notion-seed")
	iPlan := strings.Index(out, "Plan:")
	if iDrift < 0 || iPlan < 0 || iDrift > iPlan {
		t.Errorf("drift must come before the plan:\n%s", out)
	}
	if !strings.Contains(out, "[migration required]") {
		t.Errorf("the line's class must appear:\n%s", out)
	}
}

func TestRenderOmitsEmptySections(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Resource: "database.tasks", Detail: "(new)",
		Details: []resources.Detail{
			{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
		},
	}}}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "Drift") || strings.Contains(out, "Unmanaged") {
		t.Errorf("without a state, the output must be what it used to be:\n%s", out)
	}
}

func TestRenderBlockedMessageNamesReasonAndRemedy(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.flows: silent rewrite (option \"Annulé\").\n  → migrate the rows",
		},
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows", Class: ClassSilentRewrite,
			Details: []resources.Detail{
				{Op: "-", Target: `option "Annulé"`, Class: ClassSilentRewrite, Count: -1},
			},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Plan blocked") || !strings.Contains(out, "→ migrate the rows") {
		t.Errorf("the block must name its reason and its way out:\n%s", out)
	}
}

func TestRenderListsUnmanaged(t *testing.T) {
	p := &Plan{Unmanaged: []Unmanaged{{
		Resource: "database.tasks", Lines: []string{`property "Créé le" (created_time)`},
	}}}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Unmanaged") || !strings.Contains(out, "Créé le") {
		t.Errorf("unmanaged missing:\n%s", out)
	}
	// Unmanaged content with no change at all is not an absence of change:
	// there is something to show. Locks this choice in, otherwise a future fix
	// could make the message come back as a side effect.
	if strings.Contains(out, "No changes") {
		t.Errorf("unmanaged content is something to show, not an absence of change:\n%s", out)
	}
}

// A line's class is independent of the resource's class: a database that
// receives a safe addition together with a status option removal must not put
// the dangerous label on the safe line. Without this test, a regression that
// derived each line's suffix from c.Class instead of d.Class would go
// unnoticed: in every other test, the resource's class and its details' carry
// the same value.
func TestRenderLineClassIsIndependentOfResourceClass(t *testing.T) {
	p := &Plan{
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows",
			Class:    ClassSilentRewrite,
			Details: []resources.Detail{
				{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
				{Op: "-", Target: `option "Annulé" (status "Étape")`,
					Class: ClassSilentRewrite, Count: -1},
			},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	out := b.String()

	var safeLine, rewriteLine string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, `property "Name"`):
			safeLine = line
		case strings.Contains(line, `option "Annulé"`):
			rewriteLine = line
		}
	}
	if safeLine == "" || rewriteLine == "" {
		t.Fatalf("expected lines missing from the output:\n%s", out)
	}
	if strings.Contains(safeLine, "[") {
		t.Errorf("a safe line must not inherit the resource's label: %q", safeLine)
	}
	if !strings.Contains(rewriteLine, "[silent rewrite]") {
		t.Errorf("the dangerous line must carry its class: %q", rewriteLine)
	}
}

// The order of the sections is deliberate (see Render's comment): unmanaged
// comes before the block, on the same model as TestRenderShowsDriftBeforePlan
// for drift/plan.
func TestRenderOrdersUnmanagedBeforeBlocked(t *testing.T) {
	p := &Plan{
		Blocked: true,
		BlockedReasons: []string{
			"database.flows: silent rewrite (option \"Annulé\").\n  → migrate the rows",
		},
		Unmanaged: []Unmanaged{{
			Resource: "database.tasks", Lines: []string{`property "Créé le" (created_time)`},
		}},
		ToChange: 1,
		Changes: []Change{{
			Resource: "database.flows", Class: ClassSilentRewrite,
			Details: []resources.Detail{
				{Op: "-", Target: `option "Annulé"`, Class: ClassSilentRewrite, Count: -1},
			},
		}},
	}
	var b strings.Builder
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()

	iUnmanaged := strings.Index(out, "Unmanaged")
	iBlocked := strings.Index(out, "Plan blocked")
	if iUnmanaged < 0 || iBlocked < 0 || iUnmanaged > iBlocked {
		t.Errorf("unmanaged must come before the block:\n%s", out)
	}
}

// Each measured line carries its figure: it is what the decision rests on.
func TestRenderShowsTheMeasuredCount(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Annulé" (property "Statut")`,
			Class: ClassSilentRewrite, Count: 47,
			Measure: &resources.Measurement{Property: "Statut", PropertyType: "status", Option: "Annulé"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{"47 rows", "reassigned", "without a trace"} {
		if !strings.Contains(got, want) {
			t.Errorf("output:\n%s\nmissing %q", got, want)
		}
	}
}

// Measured on 2026-09-24 against the API: removing a `select` option EMPTIES
// the cell. The sentence can therefore talk about THEIR value, in the singular
// — the row held only one.
func TestRenderSaysASelectRemovalEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Basse" (property "Priorité")`,
			Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{Property: "Priorité", PropertyType: "select", Option: "Basse"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "3 rows will be emptied") {
		t.Errorf("output:\n%s\na removal on select does empty the cell", b.String())
	}
}

// Measured on 2026-09-24 against the API, on a throwaway page: a row holding
// ['Un','Deux'] from which 'Un' is removed keeps ['Deux']; a row holding only
// ['Un'] goes to []. A multi_select therefore loses THIS value, and is emptied
// only if it held no other.
//
// "will lose their value" reads as "the cell will be emptied": true for
// select, false for multi_select. And we do NOT say how many rows will really
// be emptied — the measurement counts the rows holding the option, not those
// holding only it.
func TestRenderDoesNotClaimAMultiSelectRemovalEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Un" (property "Tags")`,
			Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select", Option: "Un"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "will lose their value") {
		t.Errorf("output:\n%s\n\"their value\" asserts the cell will be emptied, "+
			"which is false on multi_select", got)
	}
	for _, want := range []string{"3 rows will lose this value", "they held no other"} {
		if !strings.Contains(got, want) {
			t.Errorf("output:\n%s\nmissing %q", got, want)
		}
	}
}

// 0 rows: the line must say so, and be safe. Without it, the user does not
// know notion-seed checked.
func TestRenderSaysWhenNoRowIsAffected(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Legacy" (property "Priorité")`,
			Class: ClassSafe, Count: 0,
			Measure: &resources.Measurement{Property: "Priorité", PropertyType: "select", Option: "Legacy"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "0 rows affected") {
		t.Errorf("output:\n%s", b.String())
	}
}

// A capped count must never be shown as an exact count.
func TestRenderMarksACappedCountAsALowerBound(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "X" (property "Statut")`,
			Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{Property: "Statut", PropertyType: "status", Option: "X"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "more than 300") {
		t.Errorf("output:\n%s\na capped count must read as a lower bound", b.String())
	}
}

// The Impact line NEVER adds up two different families, and never claims more
// than what was measured.
func TestImpactSeparatesReassignedFromWeakened(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassSilentRewrite, Count: 47,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
			{Op: "~", Class: ClassSilentRewrite, Count: 230,
				Measure: &resources.Measurement{PropertyType: "multi_select"}},
		},
	}}}
	got := Impact(p)
	if !strings.Contains(got, "47") || !strings.Contains(got, "up to 230") {
		t.Errorf("Impact = %q: the count of non-empty values is an UPPER BOUND, "+
			"it does not say how many rows will really lose something", got)
	}
}

// A migration line withholds its resource: nothing is written. Its count is
// the cost of the fix — the rows to move by hand —, never a loss. Rendering it
// as a removal would announce a reassignment that will not happen.
func TestRenderStatesAMigrationCountAsTheRemedyCost(t *testing.T) {
	for _, tc := range []struct {
		name, propType string
	}{
		{"status rename", "status"},
		{"select color", "select"},
		{"multi_select color", "multi_select"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plan{ToChange: 1, Changes: []Change{{
				Resource: "database.tasks", Kind: resources.KindUpdate,
				Class: ClassMigration,
				Details: []resources.Detail{{
					Op: "~", Target: `option "Fait" → "Terminé" (property "Statut")`,
					Class: ClassMigration, Count: 2,
					Measure: &resources.Measurement{
						Property: "Statut", PropertyType: tc.propType, Option: "Fait",
					},
				}},
			}}}
			var b bytes.Buffer
			if err := Render(&b, p); err != nil {
				t.Fatal(err)
			}
			got := b.String()
			want := `→ 2 rows hold "Fait": migrate them by hand before applying.`
			if !strings.Contains(got, want) {
				t.Errorf("output:\n%s\nmissing %q", got, want)
			}
			for _, lie := range []string{"reassigned", "will be emptied", "will lose", "Impact:"} {
				if strings.Contains(got, lie) {
					t.Errorf("output:\n%s\n%q announces a loss a withheld resource does not cause",
						got, lie)
				}
			}
		})
	}
}

// The aggregate counts only what will be written: a withheld migration does
// not enter it, even next to a real removal it must not inflate.
func TestImpactExcludesMigrationLines(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "~", Class: ClassMigration, Count: 2,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Fait"}},
			{Op: "~", Class: ClassMigration, Count: 5,
				Measure: &resources.Measurement{PropertyType: "select", Option: "Haute"}},
			{Op: "-", Class: ClassSilentRewrite, Count: 3,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
		},
	}}}
	if got, want := Impact(p), "Impact: 3 values reassigned without a trace."; got != want {
		t.Errorf("Impact = %q, want %q", got, want)
	}
	only := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details:  p.Changes[0].Details[:2],
	}}}
	if got := Impact(only); got != "" {
		t.Errorf("Impact = %q, want empty: a withheld migration loses nothing", got)
	}
}

func TestImpactIsEmptyWhenNothingIsAtStake(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details:  []resources.Detail{{Op: "+", Class: ClassSafe, Count: -1}},
	}}}
	if got := Impact(p); got != "" {
		t.Errorf("Impact = %q, want empty", got)
	}
}

// Spec §5: apply aggregates the impact of ONLY the resources it will write;
// plan, which describes the gap and not an execution, aggregates everything.
// Both setups are locked in: without a withheld resource both totals match,
// with a withheld resource apply's is strictly lower — it excludes what the
// withheld resource would have cost.
func TestWritableImpactCountsOnlyWhatApplyWillWrite(t *testing.T) {
	written := Change{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{PropertyType: "select", Option: "Basse"},
		}},
	}
	destroyed := Change{
		Resource: "database.archive", Kind: resources.KindDestroy, Class: ClassDestructive,
		Details: []resources.Detail{{
			Op: "-", Target: "database.archive", Class: ClassDestructive, Count: 5,
			Measure: &resources.Measurement{AllRows: true},
		}},
	}
	withheld := Change{
		Resource: "database.projects", Kind: resources.KindUpdate,
		Withheld: "an option must be migrated by hand",
		Details: []resources.Detail{
			{Op: "~", Class: ClassMigration, Count: 2,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Fait"}},
			{Op: "-", Class: ClassDestructive, Count: 4,
				Measure: &resources.Measurement{PropertyType: "select", Option: "Legacy"}},
		},
	}

	const writtenOnly = "Impact: 3 values lost, 1 database(s) in the trash with 5 row(s)."

	clean := &Plan{Changes: []Change{written, destroyed}}
	if got := Impact(clean); got != writtenOnly {
		t.Fatalf("test setup is wrong: Impact = %q, want %q", got, writtenOnly)
	}
	if got := WritableImpact(clean); got != writtenOnly {
		t.Errorf("WritableImpact = %q, want %q: without a withheld resource, apply and plan "+
			"announce the same total", got, writtenOnly)
	}

	mixed := &Plan{Changes: []Change{written, destroyed, withheld}}
	if got, want := Impact(mixed), "Impact: 7 values lost, 1 database(s) in the trash with 5 row(s)."; got != want {
		t.Errorf("Impact = %q, want %q: plan aggregates everything, withheld included", got, want)
	}
	if got := WritableImpact(mixed); got != writtenOnly {
		t.Errorf("WritableImpact = %q, want %q: apply does not aggregate what it will not cause",
			got, writtenOnly)
	}
}

// RenderForApply differs from Render only by the aggregate line: the whole
// detail of the plan — withheld resource included — stays rendered
// identically.
func TestRenderForApplyDiffersFromRenderOnlyByTheImpactLine(t *testing.T) {
	p := &Plan{ToChange: 2, Changes: []Change{
		{
			Resource: "database.tasks", Kind: resources.KindUpdate, Class: ClassDestructive,
			Details: []resources.Detail{{
				Op: "-", Target: `option "Basse" (property "Prio")`,
				Class: ClassDestructive, Count: 3, Property: "Prio",
				Measure: &resources.Measurement{Property: "Prio", PropertyType: "select", Option: "Basse"},
			}},
		},
		{
			Resource: "database.projects", Kind: resources.KindUpdate, Class: ClassDestructive,
			Withheld: "an option must be migrated by hand",
			Details: []resources.Detail{{
				Op: "-", Target: `option "Legacy" (property "Type")`,
				Class: ClassDestructive, Count: 4, Property: "Type",
				Measure: &resources.Measurement{Property: "Type", PropertyType: "select", Option: "Legacy"},
			}},
		},
	}}

	var forPlan, forApply bytes.Buffer
	if err := Render(&forPlan, p); err != nil {
		t.Fatal(err)
	}
	if err := RenderForApply(&forApply, p); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(forPlan.String(), Impact(p), WritableImpact(p), 1)
	if forApply.String() != want {
		t.Errorf("RenderForApply:\n%s\nwant:\n%s", forApply.String(), want)
	}
	if !strings.Contains(forApply.String(), "Impact: 3 values lost.") {
		t.Errorf("output:\n%s\napply's aggregate must exclude the withheld resource", forApply.String())
	}
}

// lifecycle no longer blocks but must be heard.
func TestRenderShowsLifecycleAcknowledgements(t *testing.T) {
	p := &Plan{ToDestroy: 1, Changes: []Change{{
		Resource: "database.archive", Kind: resources.KindDestroy,
		Class: ClassDestructive, Acknowledged: []string{"prevent_destroy"},
		Details: []resources.Detail{{Op: "-", Target: "database.archive", Class: ClassDestructive, Count: -1}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "prevent_destroy") {
		t.Errorf("output:\n%s", b.String())
	}
}

// A MEASURABLE line whose measurement did not happen must not stay silent.
//
// The case is reachable without an outage or --skip-preflight: rich_text →
// number is classified "silent rewrite" by the measured table, so the detail
// carries a measurement request; but core/measure cannot build a filter for
// rich_text, drops the request without counting it as an incident, and the
// count stays at -1. With no consequence shown, the line reads "nothing to
// report" — right next to a neighbour that carries its figure, and on a
// silent rewrite.
func TestRenderSaysWhenAMeasurableLineWasNotMeasured(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Notes"`, Note: "rich_text → number",
			Class: ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{Property: "Notes", PropertyType: "rich_text"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "impact not measured") {
		t.Errorf("output:\n%s\na measurable line that was not measured must SAY so, not stay silent", b.String())
	}
}

// "Not measurable" and "not measured" are not fixed the same way, so they are
// not said the same way. A type notion-seed cannot filter will not be counted
// any better on the tenth run: promising "rerun" would announce a corrective
// action that will never come — exactly the fault this product exists to
// remove.
func TestRenderNeverPromisesARetryOnAnUnmeasurableLine(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Notes"`, Note: "rich_text → number",
			Class: ClassSilentRewrite, Count: -1, Unmeasurable: true,
			Measure: &resources.Measurement{Property: "Notes", PropertyType: "rich_text"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "rerun") {
		t.Errorf("output:\n%s\nan unmeasurable line is not fixed by rerunning", got)
	}
	// It must still speak, and name what blocks.
	if !strings.Contains(got, "rich_text") {
		t.Errorf("output:\n%s\nthe sentence must name the type that cannot be counted", got)
	}
	if !strings.Contains(got, "unknown") {
		t.Errorf("output:\n%s\nthe actual impact stays unknown and must be said", got)
	}
}

// The exact counterpart: a measurement simply NOT MADE (--skip-preflight, 403,
// 429) is fixed by rerunning, and must keep its promise. Without this test,
// the fix above could remove the remedy everywhere.
func TestRenderStillPromisesARetryOnAMerelyUnmeasuredLine(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Annulé" (property "Statut")`,
			Class: ClassSilentRewrite, Count: -1,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Annulé"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "rerun online to get it") {
		t.Errorf("output:\n%s\na measurement not made is fixed by rerunning: say so", b.String())
	}
}

// The counterpart of the previous test: a line with no measurement request
// costs nothing, and so has no consequence to announce. Without this bound,
// the fix above would spill "impact not measured" onto every property
// addition of a creation.
func TestRenderStaysSilentOnALineThatCostsNothing(t *testing.T) {
	p := &Plan{ToAdd: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindCreate, Detail: "(new)",
		Details: []resources.Detail{
			{Op: "+", Target: `property "Name" (title)`, Class: ClassSafe, Count: -1},
		},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "→") {
		t.Errorf("output:\n%s\na line that costs nothing announces nothing", b.String())
	}
}

// Impact sums counts taken on DIFFERENT measurements, which can cover the
// same rows: two options removed from the same multi_select property are
// filtered with `contains`, so a row holding both is counted twice. 10 + 8
// does not make 18 distinct rows. The total at hand is a number of values —
// of (row, option) pairs — and that is how it must read.
func TestImpactDoesNotPresentValuesAsDistinctRows(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassDestructive, Count: 10,
				Measure: &resources.Measurement{
					Property: "Tags", PropertyType: "multi_select", Option: "A"}},
			{Op: "-", Class: ClassDestructive, Count: 8,
				Measure: &resources.Measurement{
					Property: "Tags", PropertyType: "multi_select", Option: "B"}},
		},
	}}}
	got := Impact(p)
	if strings.Contains(got, "18 rows") {
		t.Errorf("Impact = %q: 18 is a number of values, not of distinct rows "+
			"(the same rows can hold both options)", got)
	}
	if !strings.Contains(got, "18 values") {
		t.Errorf("Impact = %q: the measured total must stay visible, named for what it is", got)
	}
}

// A CAPPED type change is not bounded from above. The count is that of the
// non-empty rows, hence an upper bound of what will be lost — hence "up to" —
// but capped it becomes a LOWER BOUND of that upper bound. Both at once would
// give "up to more than 300", which means nothing and suggests an upper bound
// that does not exist.
func TestRenderNeverBoundsACappedTypeChangeFromAbove(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Class: ClassSilentRewrite,
		Details: []resources.Detail{{
			Op: "~", Target: `property "Tags" (multi_select → select)`,
			Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{Property: "Tags", PropertyType: "multi_select"},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if strings.Contains(got, "up to") {
		t.Errorf("output:\n%s\na capped count bounds nothing from above", got)
	}
	if !strings.Contains(got, "more than 300") {
		t.Errorf("output:\n%s\nthe measured lower bound must stay visible", got)
	}
}

// The Impact line sums counts; if one of them is capped, the sum is a lower
// bound and must read as such. Showing it as a firm count would be exactly the
// unverified claim notion-seed exists to prevent.
func TestImpactKeepsACappedSumALowerBound(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{
			{Op: "-", Class: ClassSilentRewrite, Count: 300, Capped: true,
				Measure: &resources.Measurement{PropertyType: "status", Option: "Annulé"}},
			{Op: "-", Class: ClassDestructive, Count: 12,
				Measure: &resources.Measurement{PropertyType: "select", Option: "Legacy"}},
		},
	}}}
	got := Impact(p)
	if !strings.Contains(got, "more than 300 values reassigned") {
		t.Errorf("Impact = %q: a sum that contains a capped count is a lower bound", got)
	}
	// The uncapped family keeps its firm count: the cap of one must not make
	// the other vaguer than it is.
	if !strings.Contains(got, "12 values lost") {
		t.Errorf("Impact = %q: the uncapped family keeps its exact count", got)
	}
}

// Same reasoning on the "degraded" family: capped, it loses its upper bound,
// hence its "up to".
func TestImpactNeverBoundsACappedTypeChangeFromAbove(t *testing.T) {
	p := &Plan{Changes: []Change{{
		Resource: "database.tasks",
		Details: []resources.Detail{{
			Op: "~", Class: ClassSilentRewrite, Count: 300, Capped: true,
			Measure: &resources.Measurement{PropertyType: "multi_select"},
		}},
	}}}
	got := Impact(p)
	if strings.Contains(got, "up to") {
		t.Errorf("Impact = %q: a capped count bounds nothing from above", got)
	}
	if got == "" {
		t.Error("Impact must not stay silent: rows will be degraded")
	}
}

// A status option that disappears in a TYPE CHANGE is not a status option
// removal: nothing remains to reassign the row to. Measured on select →
// multi_select on 2026-09-25, the row loses the value whose name does not come
// back; announcing it "reassigned" would assert a fate that is not its own,
// and file it under the wrong total.
func TestRenderSaysARetypedStatusOptionEmptiesTheCell(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Fait" (property "Statut")`,
			Class: ClassDestructive, Count: 2,
			Measure: &resources.Measurement{
				Property: "Statut", PropertyType: "status", Option: "Fait", Retyped: true,
			},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "2 rows will be emptied") {
		t.Errorf("output:\n%s\nmissing \"2 rows will be emptied\"", got)
	}
	if strings.Contains(got, "reassigned") {
		t.Errorf("output:\n%s\nan option that disappears with its type reassigns nothing", got)
	}
	if !strings.Contains(got, "Impact: 2 values lost.") {
		t.Errorf("output:\n%s\nthe total must count a loss, not a reassignment", got)
	}
}

// From multi_select, the fate of an option lost in a type change was not
// measured: the sentence states the loss, without describing what remains.
func TestRenderStaysCautiousOnARetypedMultiSelectOption(t *testing.T) {
	p := &Plan{ToChange: 1, Changes: []Change{{
		Resource: "database.tasks", Kind: resources.KindUpdate,
		Details: []resources.Detail{{
			Op: "-", Target: `option "Un" (property "Tags")`,
			Class: ClassDestructive, Count: 3,
			Measure: &resources.Measurement{
				Property: "Tags", PropertyType: "multi_select", Option: "Un", Retyped: true,
			},
		}},
	}}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "3 rows will lose this value (exact outcome not measured)") {
		t.Errorf("output:\n%s\nmissing the cautious loss", got)
	}
	if strings.Contains(got, "they held no other") {
		t.Errorf("output:\n%s\nan unmeasured fate is described", got)
	}
}

func destroyOf(key string, count int, capped bool) Change {
	return Change{
		Resource: "database." + key, Key: key, Kind: resources.KindDestroy, Class: ClassDestructive,
		Details: []resources.Detail{{
			Op: "-", Target: "database." + key, Class: ClassDestructive,
			Count: count, Capped: capped,
			Measure: &resources.Measurement{AllRows: true},
		}},
	}
}

// A destruction's line says how many rows go with the database — or that it
// is not known. Staying silent would make it indistinguishable from an empty
// database.
func TestRenderSaysHowManyRowsADestroyTakesWithIt(t *testing.T) {
	tests := []struct {
		count  int
		capped bool
		want   string
	}{
		{1, false, "→ 1 row(s) go to the trash with it."},
		{0, false, "→ no rows go to the trash with it."},
		{300, true, "→ more than 300 row(s) go to the trash with it."},
		{-1, false, "→ number of rows going to the trash with it not measured; " +
			"rerun to get it."},
	}
	for _, tt := range tests {
		p := &Plan{ToDestroy: 1, Changes: []Change{destroyOf("b", tt.count, tt.capped)}}
		var b bytes.Buffer
		if err := Render(&b, p); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), tt.want) {
			t.Errorf("count=%d:\n%s\nwant %q", tt.count, b.String(), tt.want)
		}
		if !strings.Contains(b.String(), "[destructive]") {
			t.Errorf("count=%d: the destruction lost its class:\n%s", tt.count, b.String())
		}
	}
}

// The aggregate counts the rows of the destroyed databases. An unknown count
// never becomes 0: it is stated, and the known total becomes a lower bound.
func TestImpactCountsTheRowsOfDestroyedDatabases(t *testing.T) {
	tests := []struct {
		name    string
		changes []Change
		want    string
	}{
		{"one", []Change{destroyOf("b", 1, false)},
			"Impact: 1 database(s) in the trash with 1 row(s)."},
		{"sum", []Change{destroyOf("a", 2, false), destroyOf("b", 3, false)},
			"Impact: 2 database(s) in the trash with 5 row(s)."},
		{"capped", []Change{destroyOf("a", 300, true), destroyOf("b", 3, false)},
			"Impact: 2 database(s) in the trash with more than 303 row(s)."},
		{"unknown", []Change{destroyOf("b", -1, false)},
			"Impact: 1 database(s) in the trash, rows not counted."},
		{"partial", []Change{destroyOf("a", 2, false), destroyOf("b", -1, false)},
			"Impact: 2 database(s) in the trash with at least 2 row(s), " +
				"rows not counted for 1 of them."},
	}
	for _, tt := range tests {
		if got := Impact(&Plan{Changes: tt.changes}); got != tt.want {
			t.Errorf("%s: Impact = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A count that covers only one of a database's data sources is a lower bound,
// and both the line and the aggregate say so.
func TestRenderSaysAtLeastWhenSomeDataSourcesWereNotCounted(t *testing.T) {
	partial := destroyOf("b", 3, false)
	partial.Details[0].Measure.UncountedDataSources = 1
	p := &Plan{ToDestroy: 1, Changes: []Change{partial}}
	var b bytes.Buffer
	if err := Render(&b, p); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"→ at least 3 row(s) go to the trash with it: 1 of its 2 data sources was not counted.",
		"Impact: 1 database(s) in the trash with at least 3 row(s).",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output:\n%s\nwant %q", b.String(), want)
		}
	}

	capped := destroyOf("b", 300, true)
	capped.Details[0].Measure.UncountedDataSources = 2
	if got, want := consequence(capped.Details[0]),
		"more than 300 row(s) go to the trash with it: 2 of its 3 data sources were not counted"; got != want {
		t.Errorf("consequence = %q, want %q", got, want)
	}
	if got, want := Impact(&Plan{Changes: []Change{capped, destroyOf("a", 2, false)}}),
		"Impact: 2 database(s) in the trash with more than 302 row(s)."; got != want {
		t.Errorf("Impact = %q, want %q", got, want)
	}
}
