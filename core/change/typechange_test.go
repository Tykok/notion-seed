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
		{"rich_text", "number", ClassSilentRewrite},
		{"checkbox", "number", ClassDestructive},
		// 2026-09-25.
		{"rich_text", "url", ClassSafe},
		{"people", "rich_text", ClassSafe},
		{"checkbox", "rich_text", ClassSafe},
		{"date", "url", ClassDestructive},
		{"status", "rich_text", ClassDestructive},
		{"url", "number", ClassSilentRewrite},
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
		{"url", "select", []string{"https://a.example"}, ClassSilentRewrite},
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
