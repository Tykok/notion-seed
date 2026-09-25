// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
)

// Enrich runs a plan's measurement requests and reclassifies each detail.
//
// It NEVER returns a fatal error: a measurement that fails leaves its line as
// "unknown impact" and its cause is returned for display. Not knowing is not a
// failure of the command — depriving the user of the rest of their plan
// because a count failed would be.
//
// dataSourceIDs maps the configuration key to the id of the data source TO
// QUERY. The caller builds it: the fresh id the refresh just read wins, the
// state's one is only a fallback — measuring on a stale id would count the
// rows of another object. A resource missing from this table is not
// measurable: nothing, neither read back nor in the state, says what to query.
func Enrich(ctx context.Context, c Counter, dataSourceIDs map[string]string, p *diff.Plan) []string {
	var failures []string

	for i := range p.Changes {
		ch := &p.Changes[i]
		dsID := dataSourceIDs[ch.Key]

		for j := range ch.Details {
			d := &ch.Details[j]
			if d.Measure == nil || dsID == "" {
				continue
			}

			res, err := c.Count(ctx, Request{
				DataSourceID: dsID,
				Property:     d.Measure.Property,
				PropertyType: d.Measure.PropertyType,
				Option:       d.Measure.Option,
				AllRows:      d.Measure.AllRows,
				Count:        d.Measure.Count,
				Except:       d.Measure.Except,
			})
			if err != nil {
				// The line stays unknown, which it already was. A failure never
				// downgrades it to "safe".
				//
				// A non-filterable type IS NOT an outage: it is a question
				// notion-seed cannot ask, and it knows so in advance without
				// having tried anything. Adding it to the failure list would
				// make "count failed" show up on every plan carrying such a
				// type, and would teach users to ignore a line that otherwise
				// reports real incidents — 403, exhausted 429, misunderstood
				// response. The SAME test decides both consequences: do not
				// count the incident, and mark the line as unmeasurable so the
				// rendering stops promising a remedy that does not exist.
				// Rerunning will not make rich_text filterable.
				if !errors.Is(err, ErrUnsupportedFilter) {
					failures = append(failures, fmt.Sprintf("%s: %v", ch.Resource, err))
				} else {
					d.Unmeasurable = true
				}
				continue
			}

			d.Count = res.Count
			d.Capped = res.Capped

			switch {
			case d.Measure.AllRows:
				// A destruction stays destructive, at 0 rows as at 10,000: the
				// database goes to the trash in both cases. The count says what
				// it takes with it, not whether it goes — and letting it fall
				// into the "zero downgrades" case below would announce a
				// trashing as "safe".
			case d.Class == change.ClassMigration:
				// An option rename or color change already carries
				// ClassMigration BEFORE any measurement: the change is not
				// expressible in the API, regardless of the number of rows
				// affected. Only the COST of the remedy (removing the old
				// option) remained to be measured, and that is done above.
				// Recomputing the class here would drop it back to
				// ClassSafe/ClassDestructive/ClassSilentRewrite depending on the
				// count, and `--fail-on=migration` would stop triggering on a
				// plain rename.
			case d.Measure.Option != "" && d.Measure.Retyped:
				d.Class = change.ClassifyRetypedOptionRemoval(d.Measure.TargetType, res.Count)
			case d.Measure.Option != "":
				d.Class = change.ClassifyOptionRemoval(d.Measure.PropertyType, res.Count)
			case res.Count == 0 && d.Measure.Bound == change.BoundAtLeast:
				// A lower bound of zero proves nothing: the rows the filter
				// cannot see — text made only of spaces, a value that differs
				// from an option only by case — are touched all the same.
				// Downgrading here would announce "safe" on a count that cannot
				// say so.
			case res.Count == 0:
				// The count reclassifies a type change IN ONE DIRECTION ONLY,
				// and the asymmetry is not obvious:
				//
				// Zero downgrades. A destructive pair on an empty column costs
				// nothing — there is no value to degrade. Without this
				// downgrade the line contradicts itself, "silent rewrite"
				// followed by "0 rows affected", and --fail-on=silent-rewrite
				// stops a CI on a column with no data at all. It is the same
				// rule as for option removal, where a zero count yields
				// ClassSafe.
				//
				// A non-zero count, on the other hand, changes nothing: the
				// class comes from the table of type pairs, measured against
				// the API. The count gives the SCALE, the table gives the
				// NATURE, and nothing in "12 non-empty rows" makes a silent
				// rewrite any less silent.
				d.Class = change.ClassSafe
			}
		}

		// The header class was computed by Compute, BEFORE the measurement: it
		// is stale as soon as a detail changes class. Recomputing it here is
		// mandatory, otherwise a resource's header announces a severity that
		// no longer matches its lines — an "unknown impact" on a resource
		// whose lines are now all measured safe, or the reverse.
		//
		// diff.WorstClass is the ONLY severity rule in the repository: writing
		// a second one here would make it diverge from Compute's at the first
		// new class.
		ch.Class = diff.WorstClass(ch.Details)
	}
	return failures
}
