// SPDX-License-Identifier: GPL-3.0-or-later

package planfile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Drift is one difference between the reviewed plan and the plan recomputed
// now. Any drift refuses the apply: what would be written is no longer what
// was reviewed.
type Drift struct {
	// Resource is the resource concerned, "" for a link (workspace,
	// configuration, state) or for the plan as a whole.
	Resource string
	// Message says what differs, in one line.
	Message string
	// CountFailed says the recomputed line has no figure because its count
	// did not succeed now (resources.Detail.CountFailed). Rerunning apply may
	// clear it without a new review: the caller says so when every drift is
	// of this kind. A line with no figure for any other reason — its type is
	// no longer filterable, nothing said which data source to query — is not
	// marked: rerunning would not change it.
	CountFailed bool
}

func (d Drift) String() string {
	if d.Resource == "" {
		return d.Message
	}
	return d.Resource + ": " + d.Message
}

// CheckLinks compares what ties the reviewed plan to its starting point —
// workspace, configuration, state — with the same links now. Each difference
// is named: "something changed" does not tell the user where to look.
func CheckLinks(saved File, now Meta) []Drift {
	var out []Drift
	if saved.WorkspaceID != now.WorkspaceID {
		out = append(out, Drift{Message: fmt.Sprintf(
			"workspace changed since the plan: planned on %s, ntn is now authenticated on %s",
			orNone(saved.WorkspaceID), orNone(now.WorkspaceID))})
	}
	if saved.ConfigSHA256 != now.ConfigSHA256 {
		out = append(out, Drift{Message: "configuration changed since the plan: " +
			"the branch being applied is not the one that was planned — " +
			"plan again on the merged branch"})
	}
	if saved.StateSHA256 != now.StateSHA256 {
		out = append(out, Drift{Message: "state changed since the plan: " +
			"another apply went through in between"})
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// Compare tells whether the recomputed plan is covered by the reviewed one.
// An empty result means "applicable".
//
// Covered means (spec §4, P4, P5):
//   - the same resources, of the same kind, withheld for the same reason;
//   - the same lines — operation, target, class, and the source type of a
//     type change;
//   - on each counted line, a recomputed count no higher than the reviewed
//     one, with a bound that did not degrade;
//   - the same stale state entries.
//
// Two class changes are not drifts, because they lower the impact rather
// than change it: a line reviewed WITHOUT a figure was accepted whatever it
// costs, so the class its count now gives it stays within what was accepted;
// and a count that falls to zero downgrades its line to safe — the
// measurement pass's own rule.
//
// The function is PURE: no network, no file, no clock.
func Compare(saved File, fresh *diff.Plan) []Drift {
	var out []Drift
	if saved.Blocked {
		out = append(out, Drift{Message: "the reviewed plan was blocked, " +
			"and a blocked plan is never applied"})
	}
	if fresh.Blocked && !saved.Blocked {
		for _, r := range fresh.BlockedReasons {
			out = append(out, Drift{Message: "the plan is blocked now: " + firstLine(r)})
		}
	}

	now := changesOf(fresh)
	byResource := make(map[string]Change, len(now))
	// details keeps the recomputed details beside their lines: what a line
	// does not carry — why it has no figure — is read there.
	details := make(map[string][]resources.Detail, len(now))
	for i, c := range now {
		byResource[c.Resource] = c
		details[c.Resource] = fresh.Changes[i].Details
	}
	reviewed := make(map[string]bool, len(saved.Changes))
	for _, r := range saved.Changes {
		reviewed[r.Resource] = true
		n, ok := byResource[r.Resource]
		if !ok {
			out = append(out, Drift{Resource: r.Resource,
				Message: fmt.Sprintf("change gone (%s)", r.Kind)})
			continue
		}
		out = append(out, compareChange(r, n, details[r.Resource])...)
	}
	for _, n := range now {
		if !reviewed[n.Resource] {
			out = append(out, Drift{Resource: n.Resource,
				Message: fmt.Sprintf("change added (%s)", n.Kind)})
		}
	}

	out = append(out, compareStale(saved.StaleState, fresh.StaleState)...)
	return out
}

// compareChange compares a reviewed change (r) with the same resource
// recomputed (n); nd holds n's details, line for line.
func compareChange(r, n Change, nd []resources.Detail) []Drift {
	if r.Kind != n.Kind {
		return []Drift{{Resource: r.Resource,
			Message: fmt.Sprintf("kind changed: %s reviewed, %s now", r.Kind, n.Kind)}}
	}
	switch {
	case r.Withheld == n.Withheld:
	case r.Withheld == "":
		return []Drift{{Resource: r.Resource,
			Message: "was going to be written, is now withheld: " + firstLine(n.Withheld)}}
	case n.Withheld == "":
		return []Drift{{Resource: r.Resource,
			Message: "was withheld, would now be written"}}
	default:
		return []Drift{{Resource: r.Resource,
			Message: "withheld for another reason now: " + firstLine(n.Withheld)}}
	}

	var out []Drift
	fresh := indexLines(n.Details)
	matched := make(map[lineKey]bool, len(n.Details))
	for i, rl := range r.Details {
		key := keyOf(r.Details, i)
		nl, ok := fresh[key]
		if !ok {
			out = append(out, Drift{Resource: r.Resource,
				Message: "line gone: " + rl.Op + " " + rl.Target})
			continue
		}
		matched[key] = true
		// A destruction's line targets the resource itself: the drift names
		// it once.
		subject := rl.Target
		if subject == r.Resource {
			subject = ""
		}
		if d, ok := compareLine(rl, n.Details[nl], nd[nl], subject); !ok {
			d.Resource = r.Resource
			out = append(out, d)
		}
	}
	for i, nl := range n.Details {
		if !matched[keyOf(n.Details, i)] {
			out = append(out, Drift{Resource: r.Resource,
				Message: "line added: " + nl.Op + " " + nl.Target})
		}
	}
	return out
}

// lineKey identifies a line within its change: its operation, its target, and
// its rank among the lines that share both — two identical lines are two
// lines.
type lineKey struct {
	op, target string
	rank       int
}

func keyOf(lines []Line, i int) lineKey {
	rank := 0
	for j := 0; j < i; j++ {
		if lines[j].Op == lines[i].Op && lines[j].Target == lines[i].Target {
			rank++
		}
	}
	return lineKey{op: lines[i].Op, target: lines[i].Target, rank: rank}
}

// indexLines maps each line's key to its position.
func indexLines(lines []Line) map[lineKey]int {
	out := make(map[lineKey]int, len(lines))
	for i := range lines {
		out[keyOf(lines, i)] = i
	}
	return out
}

// rank orders the bounds from the firmest to the vaguest. at_least and
// more_than share a rank: both leave the ceiling open, and the spec's table
// treats them as one column.
func rank(b Bound) int {
	switch b {
	case BoundExact:
		return 0
	case BoundAtMost:
		return 1
	case BoundAtLeast, BoundMoreThan:
		return 2
	}
	return 3
}

// compareLine compares a reviewed line (r) with the same line recomputed (n),
// whose detail is nd. subject names the line in the drift, "" when the
// resource already does.
//
// The source type comes first: a type change from another type is another
// change, whatever its figure. Then the bound: "12 rows reviewed, 15 now" is
// what the user needs, even when the count also moved the class.
//
// Rule of a counted line (spec §4):
//
//	R \ N       exact M   up to M   at least / more than M   not measured
//	exact n     M ≤ n     refused   refused                  refused
//	up to n     M ≤ n     M ≤ n     refused                  refused
//	at least n  M ≤ n     M ≤ n     M ≤ n                    refused
//	not measured  accepted in every column
//
// that is: the bound must not rank vaguer than the reviewed one, and the
// count must not be higher.
func compareLine(r, n Line, nd resources.Detail, subject string) (Drift, bool) {
	if r.From != n.From {
		return Drift{Message: about(subject, fmt.Sprintf("type change reviewed from %s, now from %s",
			orNone(r.From), orNone(n.From)))}, false
	}

	counted := func(b Bound) bool { return b != BoundNone }
	switch {
	case counted(r.Bound) != counted(n.Bound) && !counted(r.Bound):
		return Drift{Message: about(subject, "cost nothing when reviewed, now has a cost to count")}, false
	case counted(r.Bound) != counted(n.Bound) && r.Bound == BoundUnmeasured:
		// The reviewed line WAS attempted (counted(r.Bound) is true for
		// BoundUnmeasured too), but it never had a figure: saying it "was
		// counted" would claim the opposite of what was reviewed.
		return Drift{Message: about(subject, "had no figure when reviewed, now costs nothing to count")}, false
	case counted(r.Bound) != counted(n.Bound):
		return Drift{Message: about(subject, "was counted when reviewed, is no longer")}, false
	}

	if counted(r.Bound) && r.Bound != BoundUnmeasured {
		switch {
		case n.Bound == BoundUnmeasured:
			return Drift{Message: about(subject, fmt.Sprintf("%s reviewed, no figure now: %s",
				figure(r), whyNoFigure(nd))), CountFailed: nd.CountFailed}, false
		case rank(n.Bound) > rank(r.Bound):
			return Drift{Message: about(subject, fmt.Sprintf(
				"%s reviewed, %s now: the figure no longer bounds the impact the way the reviewed one did",
				figure(r), figure(n)))}, false
		case *n.Count > *r.Count:
			return Drift{Message: about(subject, fmt.Sprintf("%s reviewed, %s now",
				figure(r), figure(n)))}, false
		}
	}

	if r.Class != n.Class && r.Bound != BoundUnmeasured && !zeroDowngrade(n) {
		return Drift{Message: about(subject, fmt.Sprintf("%s reviewed, %s now", r.Class, n.Class))}, false
	}
	return Drift{}, true
}

// about prefixes a line's drift with the line it concerns, when the resource
// does not already name it.
func about(subject, message string) string {
	if subject == "" {
		return message
	}
	return subject + " — " + message
}

// whyNoFigure says why a recomputed line has no figure. Only a count that
// failed may be cleared by rerunning; the two other causes will not change on
// the next run.
func whyNoFigure(d resources.Detail) string {
	switch {
	case d.CountFailed:
		return "its count did not succeed"
	case d.Unmeasurable:
		return "it can no longer be counted"
	}
	return "it was not counted"
}

// zeroDowngrade says the line is safe because its count fell to zero: the
// measurement pass downgrades such a line, and it costs less than reviewed.
func zeroDowngrade(n Line) bool {
	return n.Class == "safe" && n.Count != nil && *n.Count == 0
}

// figure writes a count with its bound, in the rendering's words.
func figure(l Line) string {
	switch l.Bound {
	case BoundAtMost:
		return fmt.Sprintf("up to %d rows", *l.Count)
	case BoundAtLeast:
		return fmt.Sprintf("at least %d rows", *l.Count)
	case BoundMoreThan:
		return fmt.Sprintf("more than %d rows", *l.Count)
	}
	return fmt.Sprintf("%d rows", *l.Count)
}

func compareStale(saved, fresh []string) []Drift {
	was := make(map[string]bool, len(saved))
	for _, r := range saved {
		was[r] = true
	}
	is := make(map[string]bool, len(fresh))
	for _, r := range fresh {
		is[r] = true
	}
	var out []Drift
	for _, r := range sortedKeys(was) {
		if !is[r] {
			out = append(out, Drift{Resource: r, Message: "stale state entry gone"})
		}
	}
	for _, r := range sortedKeys(is) {
		if !was[r] {
			out = append(out, Drift{Resource: r, Message: "stale state entry added"})
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// firstLine keeps the first line of a multi-line reason: the refusal lists one
// drift per line, and the full reason is in the plan that will be reviewed.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
