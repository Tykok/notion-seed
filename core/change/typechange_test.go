// SPDX-License-Identifier: GPL-3.0-or-later

package change

import "testing"

// The table holds ONLY what was measured against the API on 2026-09-24. A
// missing pair must return ClassUnknownImpact — asserting "safe" on a pair
// never tried would be exactly the flaw this product calls out.
func TestClassifyTypeChangeUsesMeasuredCouples(t *testing.T) {
	tests := []struct {
		from, to string
		want     Class
	}{
		{"select", "multi_select", ClassSafe},
		{"number", "rich_text", ClassSafe},
		{"status", "select", ClassSafe},
		{"date", "rich_text", ClassSafe},
		{"multi_select", "select", ClassSilentRewrite},
		{"rich_text", "number", ClassSilentRewrite},
		{"checkbox", "number", ClassDestructive},
	}
	for _, tt := range tests {
		if got := ClassifyTypeChange(tt.from, tt.to); got != tt.want {
			t.Errorf("ClassifyTypeChange(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestClassifyTypeChangeIsUnknownForUnmeasuredCouples(t *testing.T) {
	for _, tt := range [][2]string{
		{"people", "number"},
		{"url", "date"},
		{"select", "checkbox"},
	} {
		if got := ClassifyTypeChange(tt[0], tt[1]); got != ClassUnknownImpact {
			t.Errorf("ClassifyTypeChange(%q, %q) = %v, want ClassUnknownImpact",
				tt[0], tt[1], got)
		}
	}
}

// An unchanged type is not a type change: the question does not arise.
func TestClassifyTypeChangeIsSafeWhenTypeDoesNotChange(t *testing.T) {
	if got := ClassifyTypeChange("select", "select"); got != ClassSafe {
		t.Errorf("ClassifyTypeChange on an unchanged type = %v, want ClassSafe", got)
	}
}

func TestClassUnknownImpactHasItsOwnLabel(t *testing.T) {
	if got := ClassUnknownImpact.String(); got != "unknown impact" {
		t.Errorf("String() = %q, want \"unknown impact\"", got)
	}
}
