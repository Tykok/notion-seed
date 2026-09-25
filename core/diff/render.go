// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"io"
	"strings"

	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Render writes the plan as plain text. notion-seed is a terminal tool: no
// interface, no unconditional color, an output that stays readable in a pipe
// and in CI.
//
// The order is deliberate: drift first (what someone did), the plan next (what
// would be done), unmanaged after (what will not be touched), not compared
// next (what was not checked), the block last with its way out. Each section
// disappears when empty: without a state, the output is exactly what it used
// to be.
func Render(w io.Writer, p *Plan) error { return render(w, p, Impact(p)) }

// RenderForApply writes the plan like Render, except for one line: the
// aggregate impact line only covers what apply WILL write.
//
// A withheld resource keeps an impact apply will not cause. Reusing the plan
// total would make the product's most-read line lie at the one moment the user
// decides, just before the confirmation. Without a withheld resource, both
// renderings match — the common case.
func RenderForApply(w io.Writer, p *Plan) error { return render(w, p, WritableImpact(p)) }

// render writes the plan. impact is the aggregate line already computed, "" to
// show none: its scope is the caller's choice — the whole plan for plan, what
// will be written for apply.
func render(w io.Writer, p *Plan, impact string) error {
	if len(p.Drifts) > 0 {
		if _, err := fmt.Fprintln(w, "Drift detected outside notion-seed"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, d := range p.Drifts {
			if _, err := fmt.Fprintf(w, "  ~ %s\n", d.Resource); err != nil {
				return err
			}
			for _, line := range d.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	// A blocked plan is never "no changes": a managed resource that is not
	// found or archived produces neither Change nor Unmanaged, only a block
	// reason. Returning here would show a match while returning an error code.
	//
	// A resource not compared (--skip-preflight) is not "no changes" either:
	// nothing is known about it, so it cannot be asserted to match.
	if len(p.Changes) == 0 && len(p.Unmanaged) == 0 && len(p.NotCompared) == 0 &&
		len(p.StaleState) == 0 && !p.Blocked {
		_, err := fmt.Fprintln(w, "No changes. The configuration matches the actual state.")
		return err
	}

	if len(p.Changes) > 0 {
		if _, err := fmt.Fprintf(w, "Plan: %d to add, %d to change, %d to destroy\n\n",
			p.ToAdd, p.ToChange, p.ToDestroy); err != nil {
			return err
		}
	}

	for _, c := range p.Changes {
		// The marker follows the resource's Kind — what the operation DOES
		// (create, destroy, update) — not its Class: notion-seed no longer
		// blocks on the strength of a class, so the class no longer has to
		// decide on a warning marker. The cost stays visible right after: the
		// [class] label on the header and on each affected line.
		marker := "~"
		switch c.Kind {
		case resources.KindCreate:
			marker = "+"
		case resources.KindDestroy:
			marker = "-"
		}

		header := fmt.Sprintf("  %s %s", marker, c.Resource)
		if c.Detail != "" {
			header += " " + c.Detail
		}
		if c.Class != ClassSafe {
			header += fmt.Sprintf("  [%s]", c.Class)
		}
		if _, err := fmt.Fprintln(w, header); err != nil {
			return err
		}
		for _, d := range c.Details {
			line := d.Op + " " + d.Target
			if d.Note != "" {
				// NO %q here: Note already carries its own quotes where they are
				// needed (a rename renders `"Old" → "New"`). An extra %q
				// re-escapes those quotes and the whole note, to the point of
				// making unreadable the one line meant to keep the user from
				// thinking a property was renamed while it stays unmanaged.
				line += " — " + d.Note
			}
			suffix := ""
			// A safe line in an otherwise dangerous resource must not inherit
			// the label: the line carries its own class.
			if d.Class != ClassSafe {
				suffix = fmt.Sprintf("  [%s]", d.Class)
			}
			if _, err := fmt.Fprintf(w, "      %s%s\n", line, suffix); err != nil {
				return err
			}
			// The figure, right under the line it concerns: it is what gets read
			// to decide, and it means nothing detached from its target.
			if cq := consequence(d); cq != "" {
				if _, err := fmt.Fprintf(w, "          → %s.\n", cq); err != nil {
					return err
				}
			}
		}
		// lifecycle no longer blocks anything: these keys are only
		// acknowledgements. Hiding them would make them invisible, and the user
		// would no longer know what they have already acknowledged.
		for _, a := range c.Acknowledged {
			if _, err := fmt.Fprintf(w,
				"      → declared in lifecycle.%s.\n", a); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	// The aggregate line comes after the list: the detail is read first, then
	// the total per family. It disappears when nothing measured is at stake —
	// announcing an empty impact would assert more than what was measured.
	if impact != "" {
		if _, err := fmt.Fprintf(w, "%s\n\n", impact); err != nil {
			return err
		}
	}

	if len(p.Unmanaged) > 0 {
		if _, err := fmt.Fprintln(w, "Unmanaged — present in Notion, left untouched"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, u := range p.Unmanaged {
			if _, err := fmt.Fprintf(w, "  %s\n", u.Resource); err != nil {
				return err
			}
			for _, line := range u.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	if len(p.StaleState) > 0 {
		if _, err := fmt.Fprintln(w,
			"Stale state entry — the resource no longer exists in Notion"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.StaleState {
			if _, err := fmt.Fprintf(w,
				"  - %s — its identity will be removed from the state, nothing will be written to Notion\n",
				r); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if len(p.NotCompared) > 0 {
		if _, err := fmt.Fprintln(w, "Not compared — --skip-preflight does not read the actual state"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.NotCompared {
			if _, err := fmt.Fprintf(w, "  %s\n", r); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if p.Blocked {
		if _, err := fmt.Fprintln(w, "Plan blocked. Nothing was applied."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.BlockedReasons {
			if _, err := fmt.Fprintf(w, "  %s\n", r); err != nil {
				return err
			}
		}
	}
	return nil
}

// bound writes a count, marking it as a lower bound when it is capped. "300"
// and "more than 300" are not decided the same way.
func bound(n int, capped bool) string {
	if capped {
		return fmt.Sprintf("more than %d", n)
	}
	return fmt.Sprintf("%d", n)
}

// consequence says, in plain words, what this detail will cost. It is the
// sentence the user reads to decide, so it must never assert more than what
// was measured.
func consequence(d resources.Detail) string {
	// No measurement request: the line costs nobody anything, and there is
	// nothing to announce. It is the ONLY case that allows silence.
	if d.Measure == nil {
		return ""
	}
	if d.Measure.AllRows {
		return destroyedRows(d)
	}
	// No count. Staying silent here would make the line indistinguishable
	// from a line with no cost, next to neighbours that carry their figure —
	// and a silent rewrite believed harmless is the worst misunderstanding this
	// rendering can produce. It remains to say WHICH of the two reasons
	// applies, because they are not fixed the same way.
	if d.Count < 0 {
		// A question that cannot be asked: no fix to offer, so none promised.
		// Announcing "rerun" here would announce a corrective action that will
		// never come.
		if d.Unmeasurable {
			return fmt.Sprintf(
				"actual impact unknown: notion-seed cannot count the rows "+
					"of a %s property", d.Measure.PropertyType)
		}
		// A question that can be asked, answer not obtained (--skip-preflight,
		// 403, 429): rerunning really works.
		return "impact not measured; rerun online to get it"
	}
	if d.Count == 0 {
		return "0 rows affected"
	}
	// On ONE measurement, the count really is a number of distinct rows: one
	// filter, one property. It is by summing several measurements that Impact
	// loses this property, and that is why it talks about values, not rows.
	count := bound(d.Count, d.Capped) + " rows"

	// Migration: the resource is withheld, nothing will be written. The count
	// is on the CURRENT option because it is the cost of the fix — the rows to
	// move by hand —, not a loss. Letting the line fall into the removal case
	// below would announce a reassignment or an emptying that will not happen:
	// a false figure about the user's data.
	if d.Class == ClassMigration && d.Measure.Option != "" {
		return fmt.Sprintf("%s hold %q: migrate them by hand before applying",
			count, d.Measure.Option)
	}

	// Option removal: the fate of the rows depends on the type, and the THREE
	// cases measured on 2026-09-24 differ. Mixing them up would assert more
	// than what was measured, which is the one flaw this product cannot
	// afford.
	if d.Measure.Option != "" {
		switch removalFate(*d.Measure) {
		case "status":
			return count + " will be reassigned to another option, without a trace"
		case "multi_select":
			// Under a type change from multi_select, nothing was measured: we
			// state the loss, not what remains of the cell.
			if d.Measure.Retyped {
				return count + " will lose this value (exact outcome not measured)"
			}
			// Measured: ['Un','Deux'] minus 'Un' gives ['Deux']; ['Un'] minus
			// 'Un' gives []. The row loses THIS value, not necessarily its
			// whole cell.
			//
			// We don't say how many rows will be emptied: the `contains` filter
			// counts the rows holding the option, not those holding only it.
			// Knowing it would cost reading every row back, which the
			// measurement pass does not do — so we name what we have.
			return count + " will lose this value; they will be emptied only if " +
				"they held no other"
		default:
			// select: measured, the cell is emptied. The row held only one.
			return count + " will be emptied"
		}
	}
	// Type change: the count is that of NON-EMPTY values, hence an upper bound
	// of what will actually be lost.
	if d.Class == ClassSilentRewrite {
		// Capped, this upper bound itself becomes a lower bound: "up to more
		// than 300" bounds from above what precisely can no longer be bounded.
		// We then state the measured floor, and own not knowing the ceiling.
		if d.Capped {
			return count + " non-empty, an unmeasured number of which will be degraded, without a trace"
		}
		return "up to " + count + " degraded, without a trace"
	}
	return count + " non-empty in this column"
}

// destroyedRows says how many rows a database moved to the trash takes with
// it. Not measured — count rejected, exhausted or not understood —, it says
// so: staying silent would make the line indistinguishable from an empty
// database.
//
// A count that covers only one of the database's data sources is a lower
// bound: the line says so, and names how many were not counted.
func destroyedRows(d resources.Detail) string {
	if d.Count < 0 {
		return "number of rows going to the trash with it not measured; " +
			"rerun to get it"
	}
	other := d.Measure.UncountedDataSources
	if other == 0 {
		if d.Count == 0 {
			return "no rows go to the trash with it"
		}
		return bound(d.Count, d.Capped) + " row(s) go to the trash with it"
	}
	missing := fmt.Sprintf("%d of its %d data sources was not counted", other, other+1)
	if other > 1 {
		missing = fmt.Sprintf("%d of its %d data sources were not counted", other, other+1)
	}
	if d.Count == 0 {
		return "no rows on the counted data source: " + missing
	}
	// Capped, the count is already a lower bound and says so ("more than");
	// "at least more than" would add nothing.
	count := "at least " + bound(d.Count, false)
	if d.Capped {
		count = bound(d.Count, true)
	}
	return count + " row(s) go to the trash with it: " + missing
}

// removalFate says which type the fate of the rows follows, for an option
// that goes away. It is the old type, except when the option disappears with
// a type change from status: it is then treated as a select, a loss.
// Only select → multi_select was measured (2026-09-25); the other pairs are
// treated as destructive out of caution, without their fate being observed.
func removalFate(m resources.Measurement) string {
	if m.Retyped && m.PropertyType == "status" {
		return "select"
	}
	return m.PropertyType
}

// Impact aggregates the measured counts into one sentence. It is the product
// in one line: the one figure nobody else can give.
//
// It NEVER adds up two different families. The count says how many rows are
// non-empty on a column that changes type; it does not say how many hold
// several values, hence how many will really lose something. Hence "up to N"
// on one side and a firm count on the other: a false figure here would ruin
// the product's one argument.
//
// A capped count contaminates its family, and only it: a sum with one term
// that is a lower bound is a lower bound, but the cap of one does not make the
// other vaguer than it is.
//
// These totals count VALUES, not distinct rows, and say so. Each count is a
// number of rows for ITS measurement, but two measurements of the same
// database can land on the same rows: two options removed from the same
// multi_select are filtered with `contains`, and a row holding both is counted
// twice. Writing "18 rows" where 10 distinct rows are affected would be a
// false figure in the line that IS the product's argument. Deduplicating would
// require collecting row identifiers, which the measurement pass does not do —
// so we name what we have.
func Impact(p *Plan) string { return impactOf(p.Changes) }

// WritableImpact aggregates the same impact as Impact, over ONLY the resources
// apply will write: those whose Withheld is empty.
//
// plan keeps aggregating everything, because it describes the gap, not an
// execution. apply announces what it will cause: a withheld resource is not
// written, so its cost is not apply's.
func WritableImpact(p *Plan) string {
	writable := make([]Change, 0, len(p.Changes))
	for _, c := range p.Changes {
		if c.Withheld == "" {
			writable = append(writable, c)
		}
	}
	return impactOf(writable)
}

// impactOf is the computation itself, shared by Impact and WritableImpact: one
// aggregation rule, two scopes. Two rules would end up diverging, and plan and
// apply would end up announcing two different costs for the same write.
func impactOf(changes []Change) string {
	reassigned, lost, weakened := 0, 0, 0
	var reassignedCapped, lostCapped, weakenedCapped bool
	var trash trashedRows
	for _, c := range changes {
		if c.Kind == resources.KindDestroy {
			trash.add(c)
		}
		for _, d := range c.Details {
			// A destruction's count is aggregated by trash, separately: they are
			// rows, not values, and they mix with no family.
			if d.Measure == nil || d.Measure.AllRows || d.Count <= 0 {
				continue
			}
			// A migration line withholds its resource: nothing is written, so
			// nothing is lost. Its count is the cost of a manual fix, which the
			// line itself shows; adding it here would turn it into a loss.
			if d.Class == ClassMigration {
				continue
			}
			switch {
			case d.Measure.Option != "" && removalFate(*d.Measure) == "status":
				reassigned += d.Count
				reassignedCapped = reassignedCapped || d.Capped
			case d.Measure.Option != "":
				lost += d.Count
				lostCapped = lostCapped || d.Capped
			case d.Class == ClassSilentRewrite:
				weakened += d.Count
				weakenedCapped = weakenedCapped || d.Capped
			}
		}
	}

	var parts []string
	if reassigned > 0 {
		parts = append(parts, fmt.Sprintf("%s values reassigned without a trace",
			bound(reassigned, reassignedCapped)))
	}
	if lost > 0 {
		parts = append(parts, fmt.Sprintf("%s values lost",
			bound(lost, lostCapped)))
	}
	if weakened > 0 {
		// Not capped, the count of non-empty values bounds from above what
		// will be lost. Capped, it no longer bounds anything from above: "up
		// to" can no longer be said, and the measured floor lives on the detail
		// line.
		if weakenedCapped {
			parts = append(parts, "an unmeasured number of values degraded without a trace")
		} else {
			parts = append(parts, fmt.Sprintf("up to %d values degraded without a trace", weakened))
		}
	}
	if trash.databases > 0 {
		parts = append(parts, trash.String())
	}
	if len(parts) == 0 {
		return ""
	}
	return "Impact: " + strings.Join(parts, ", ") + "."
}

// trashedRows aggregates the databases moved to the trash and the rows they
// take with them.
//
// Here, and only here, the total talks about ROWS: each row belongs to a
// single data source, so two destroyed databases never count the same one
// twice. An unknown count is never taken for 0: it is named, and the known
// total becomes a lower bound.
type trashedRows struct {
	databases, rows, unknown int
	// capped: a term is capped. partial: a term does not cover all the data
	// sources of its database. Both make the total a lower bound.
	capped, partial bool
}

func (t *trashedRows) add(c Change) {
	t.databases++
	for _, d := range c.Details {
		if d.Measure != nil && d.Measure.AllRows && d.Count >= 0 {
			t.rows += d.Count
			t.capped = t.capped || d.Capped
			t.partial = t.partial || d.Measure.UncountedDataSources > 0
			return
		}
	}
	// No count obtained for this database: neither 0, nor anything.
	t.unknown++
}

func (t trashedRows) String() string {
	head := fmt.Sprintf("%d database(s) in the trash", t.databases)
	switch {
	case t.unknown == t.databases:
		return head + ", rows not counted"
	case t.unknown > 0:
		return fmt.Sprintf("%s with at least %d row(s), rows not counted for %d of them",
			head, t.rows, t.unknown)
	}
	if t.partial && !t.capped {
		return fmt.Sprintf("%s with at least %d row(s)", head, t.rows)
	}
	return head + " with " + bound(t.rows, t.capped) + " row(s)"
}
