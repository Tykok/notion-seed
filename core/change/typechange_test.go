// SPDX-License-Identifier: GPL-3.0-or-later

package change

import (
	"strings"
	"testing"
)

// managedTypes mirrors config.SupportedPropertyTypes. It is copied rather than
// imported because this package is a leaf; TestEveryManagedPairIsMeasured
// fails on its own if the two lists drift, since the table would then miss a
// pair.
var managedTypes = []string{
	"title", "rich_text", "number", "url", "select",
	"status", "multi_select", "date", "checkbox", "people",
}

// The 2026-09-25 campaign measured all 90 ordered pairs of managed types.
// Nothing among them may fall back to "unknown impact" any more, and every
// entry must say when it was measured and what survives: a class without its
// evidence is a claim.
func TestEveryManagedPairIsMeasured(t *testing.T) {
	n := 0
	for _, from := range managedTypes {
		for _, to := range managedTypes {
			if from == to {
				continue
			}
			n++
			e, ok := typeChangeTable[[2]string{from, to}]
			if !ok {
				t.Errorf("%s → %s: missing from the measured table", from, to)
				continue
			}
			if e.measured == "" || e.survives == "" {
				t.Errorf("%s → %s: entry without its measurement date or its note", from, to)
			}
			if got := TypeChangeOf(from, to, nil).Class; got == ClassUnknownImpact {
				t.Errorf("%s → %s: still unknown impact", from, to)
			}
		}
	}
	if n != 90 || len(typeChangeTable) != 90 {
		t.Errorf("pairs = %d, table = %d, want 90 and 90", n, len(typeChangeTable))
	}
}

// The classes the campaigns measured, with the body notion-seed sends when the
// YAML declares no option.
func TestTypeChangeOfUsesTheMeasuredClasses(t *testing.T) {
	tests := []struct {
		from, to string
		want     Class
	}{
		// 2026-09-24, re-measured 2026-09-25 where noted in the table.
		{"select", "multi_select", ClassSafe},
		{"number", "rich_text", ClassSafe},
		{"date", "rich_text", ClassSafe},
		{"status", "select", ClassDestructive},
		{"multi_select", "select", ClassSilentRewrite},
		// Re-measured on 2026-09-25: values are emptied, not only rewritten.
		{"rich_text", "number", ClassDestructive},
		{"checkbox", "number", ClassDestructive},
		// 2026-09-25.
		{"rich_text", "url", ClassSafe},
		{"people", "rich_text", ClassSafe},
		{"checkbox", "rich_text", ClassSafe},
		{"date", "url", ClassDestructive},
		{"status", "rich_text", ClassDestructive},
		{"url", "number", ClassDestructive},
		{"select", "number", ClassDestructive},
		{"multi_select", "number", ClassDestructive},
		{"rich_text", "select", ClassDestructive},
		{"people", "checkbox", ClassDestructive},
		// Toward status, every row — empty ones included — gets a value.
		{"rich_text", "status", ClassSilentRewrite},
		{"date", "status", ClassSilentRewrite},
		{"checkbox", "status", ClassSilentRewrite},
		{"select", "status", ClassSilentRewrite},
	}
	for _, tt := range tests {
		if got := TypeChangeOf(tt.from, tt.to, nil).Class; got != tt.want {
			t.Errorf("TypeChangeOf(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

// The API refuses every pair that touches title (400). The change is not
// expressible: it is classed like the other inexpressible changes, so the
// resource is withheld and apply never sends it.
func TestTypeChangeOfTitleIsRefused(t *testing.T) {
	for _, other := range managedTypes {
		if other == "title" {
			continue
		}
		for _, pair := range [][2]string{{"title", other}, {other, "title"}} {
			tc := TypeChangeOf(pair[0], pair[1], nil)
			if tc.Class != ClassMigration || !tc.Refused {
				t.Errorf("%s → %s = {%v refused:%v}, want {migration required, refused}",
					pair[0], pair[1], tc.Class, tc.Refused)
			}
			if !strings.Contains(tc.Note, "400") {
				t.Errorf("%s → %s: note %q does not name the API refusal", pair[0], pair[1], tc.Note)
			}
		}
	}
}

// The API never creates an option: toward an option type, a value survives
// only where an option with its exact text is declared. From rich_text and
// url, a declared option also brings the cut at the first comma — a rewrite.
func TestTypeChangeOfDependsOnTheDeclaredOptions(t *testing.T) {
	tests := []struct {
		from, to string
		declared []string
		want     Class
	}{
		{"rich_text", "select", []string{"Un"}, ClassSilentRewrite},
		{"rich_text", "multi_select", []string{"Un"}, ClassSilentRewrite},
		// url: the comma cut was never measured; unmatched values are emptied.
		{"url", "select", []string{"https://a.example"}, ClassDestructive},
		{"url", "multi_select", []string{"https://a.example"}, ClassDestructive},
		{"number", "select", []string{"7"}, ClassDestructive},
		// Nothing survives from date or people, even with a homonymous option.
		{"date", "select", []string{"2026-01-15"}, ClassDestructive},
		{"people", "multi_select", []string{"Tykok"}, ClassDestructive},
		// checkbox: true → "Yes", false → "No". A checked row survives
		// only with "Yes"; an unchecked row carries no information to lose.
		{"checkbox", "select", []string{"Yes"}, ClassSafe},
		{"checkbox", "select", []string{"No"}, ClassDestructive},
		{"checkbox", "status", []string{"Yes", "No"}, ClassSafe},
		{"checkbox", "status", []string{"Yes"}, ClassSilentRewrite},
	}
	for _, tt := range tests {
		if got := TypeChangeOf(tt.from, tt.to, tt.declared).Class; got != tt.want {
			t.Errorf("TypeChangeOf(%q, %q, %q) = %v, want %v",
				tt.from, tt.to, tt.declared, got, tt.want)
		}
	}
}

// From a type without options toward an option type, the note must say that
// the values survive only through a declared option of the same text: it is
// the one lever the user holds.
func TestTypeChangeOfNamesTheOptionLever(t *testing.T) {
	for _, from := range []string{"rich_text", "number", "url"} {
		for _, to := range []string{"select", "multi_select", "status"} {
			note := TypeChangeOf(from, to, nil).Note
			if !strings.Contains(note, "only where an option with the same text is declared") {
				t.Errorf("%s → %s: note %q", from, to, note)
			}
		}
	}
}

func TestTypeChangeOfUnmanagedPairIsUnknown(t *testing.T) {
	for _, tt := range [][2]string{{"relation", "number"}, {"formula", "date"}} {
		if got := TypeChangeOf(tt[0], tt[1], nil).Class; got != ClassUnknownImpact {
			t.Errorf("TypeChangeOf(%q, %q) = %v, want ClassUnknownImpact", tt[0], tt[1], got)
		}
	}
}

// An unchanged type is not a type change: the question does not arise.
func TestTypeChangeOfIsSafeWhenTypeDoesNotChange(t *testing.T) {
	if got := TypeChangeOf("select", "select", nil).Class; got != ClassSafe {
		t.Errorf("TypeChangeOf on an unchanged type = %v, want ClassSafe", got)
	}
}

func TestClassUnknownImpactHasItsOwnLabel(t *testing.T) {
	if got := ClassUnknownImpact.String(); got != "unknown impact" {
		t.Errorf("String() = %q, want \"unknown impact\"", got)
	}
}

// Every counted pair gets the filter and the bound the campaign measured.
func TestTypeChangeOfCountsWithTheMeasuredFilter(t *testing.T) {
	tests := []struct {
		from, to string
		declared []string
		count    Count
		bound    Bound
		except   []string
	}{
		// Everything is lost: is_not_empty is exact.
		{"number", "date", nil, CountNonEmpty, BoundExact, nil},
		{"people", "select", []string{"Tykok"}, CountNonEmpty, BoundExact, nil},
		{"status", "checkbox", nil, CountNonEmpty, BoundExact, nil},
		// rich_text: blank text escapes is_not_empty — a lower bound.
		{"rich_text", "people", nil, CountNonEmpty, BoundAtLeast, nil},
		{"rich_text", "select", nil, CountNonEmpty, BoundAtLeast, nil},
		{"rich_text", "select", []string{"Un"}, CountNonEmpty, BoundAtLeast, []string{"Un"}},
		// Some values survive by parsing: an upper bound.
		{"url", "number", nil, CountNonEmpty, BoundAtMost, nil},
		{"select", "date", nil, CountNonEmpty, BoundAtMost, nil},
		{"multi_select", "select", nil, CountNonEmpty, BoundAtMost, nil},
		// No sound filter: not counted.
		{"rich_text", "number", nil, CountUnsound, BoundExact, nil},
		{"rich_text", "date", nil, CountUnsound, BoundExact, nil},
		{"status", "select", []string{"Done"}, CountUnsound, BoundExact, nil},
		{"status", "rich_text", nil, CountUnsound, BoundExact, nil},
		// checkbox: only checked rows lose information.
		{"checkbox", "number", nil, CountChecked, BoundExact, nil},
		{"checkbox", "select", []string{"No"}, CountChecked, BoundExact, nil},
		{"checkbox", "status", []string{"Yes"}, CountUnchecked, BoundExact, nil},
		{"checkbox", "status", nil, CountEveryRow, BoundExact, nil},
		// Toward status, every row gets a value.
		{"date", "status", nil, CountEveryRow, BoundExact, nil},
		{"rich_text", "status", nil, CountEveryRow, BoundExact, nil},
		{"rich_text", "status", []string{"Un"}, CountEveryRow, BoundAtLeast, []string{"Un"}},
		{"select", "status", nil, CountEmpty, BoundExact, nil},
		{"multi_select", "status", nil, CountEveryRow, BoundAtMost, nil},
		// number: only a canonical decimal writing can hold a number.
		{"number", "select", []string{"7", "7.0", "High", "-3.5"}, CountNonEmpty, BoundExact, []string{"7", "-3.5"}},
		{"number", "status", []string{"7"}, CountEveryRow, BoundExact, []string{"7"}},
	}
	for _, tt := range tests {
		tc := TypeChangeOf(tt.from, tt.to, tt.declared)
		if tc.Count != tt.count || tc.Bound != tt.bound ||
			strings.Join(tc.Except, "|") != strings.Join(tt.except, "|") {
			t.Errorf("%s → %s %q = {Count:%v Bound:%v Except:%q}, want {%v %v %q}",
				tt.from, tt.to, tt.declared, tc.Count, tc.Bound, tc.Except,
				tt.count, tt.bound, tt.except)
		}
		if tc.Bound != BoundExact && tc.Caveat == "" {
			t.Errorf("%s → %s: a bound without its reason", tt.from, tt.to)
		}
		if tc.Count == CountUnsound && tc.Caveat == "" {
			t.Errorf("%s → %s: not countable, without saying why", tt.from, tt.to)
		}
	}
}

// With an option "No", unchecked rows become "No": saying they are emptied
// would be false.
func TestCheckboxCaveatFollowsTheDeclaredOptions(t *testing.T) {
	if c := TypeChangeOf("checkbox", "select", []string{"No"}).Caveat; strings.Contains(c, "unchecked") {
		t.Errorf("caveat with \"No\" declared = %q", c)
	}
	if c := TypeChangeOf("checkbox", "select", []string{"Other"}).Caveat; !strings.Contains(c, "unchecked rows are emptied") {
		t.Errorf("caveat without \"No\" = %q", c)
	}
	if c := TypeChangeOf("checkbox", "number", nil).Caveat; !strings.Contains(c, "unchecked rows are emptied") {
		t.Errorf("caveat toward number = %q", c)
	}
}

// From multi_select, the removal lines already count the rows holding an
// option that is not redeclared. The property line excludes them, so no row
// lands in two families of the total.
func TestWithoutRowsHoldingExcludesTheRemovedOptions(t *testing.T) {
	tc := TypeChangeOf("multi_select", "select", []string{"A"}).WithoutRowsHolding([]string{"B", "C"})
	if strings.Join(tc.Except, ",") != "B,C" || tc.Bound != BoundAtMost ||
		!strings.Contains(tc.Caveat, "counted on their own lines") {
		t.Errorf("TypeChange = %+v", tc)
	}
	if tc := TypeChangeOf("multi_select", "status", nil).WithoutRowsHolding(nil); tc.Except != nil {
		t.Errorf("no removed option, Except = %q", tc.Except)
	}
}

// Measured on 2026-09-25: a number survives toward an option only when the
// option name is its canonical text — "7" keeps 7, "7.0" keeps nothing. A
// non-canonical name saves no row, so it is not excluded from the count.
func TestNumberOptionSavesRowsOnlyUnderItsCanonicalText(t *testing.T) {
	tc := TypeChangeOf("number", "select", []string{"7.0"})
	if tc.Except != nil || tc.Count != CountNonEmpty || tc.Bound != BoundExact {
		t.Errorf("TypeChange = %+v, want every non-empty row counted, exactly", tc)
	}
}

// Measured on 2026-09-25: a number survives only under its exact 'f' writing
// ("123456789012345680"), and the numeric filter is exact — but magnitudes
// from 1e21 up were not measured: a count excluding such a name is only a
// lower bound.
func TestNumberCountIsALowerBoundBeyondTheMeasuredMagnitude(t *testing.T) {
	if tc := TypeChangeOf("number", "select", []string{"123456789012345680"}); tc.Bound != BoundExact ||
		strings.Join(tc.Except, ",") != "123456789012345680" {
		t.Errorf("measured magnitude: %+v, want exact", tc)
	}
	for _, name := range []string{"1000000000000000000000", "-1000000000000000000000"} {
		tc := TypeChangeOf("number", "select", []string{"7", name})
		if tc.Bound != BoundAtLeast || !strings.Contains(tc.Caveat, "1e21") {
			t.Errorf("%s: %+v, want a lower bound naming the unmeasured magnitude", name, tc)
		}
	}
}

// url: measured on 2026-09-25, the filter ignores case and trailing spaces
// but not a trailing slash. The caveat says exactly that.
func TestURLCaveatSaysWhatTheFilterIgnores(t *testing.T) {
	c := TypeChangeOf("url", "select", []string{"https://a.example"}).Caveat
	if !strings.Contains(c, "case") || !strings.Contains(c, "trailing spaces") ||
		!strings.Contains(c, "trailing slash") {
		t.Errorf("caveat = %q", c)
	}
}
