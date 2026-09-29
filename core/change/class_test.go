// SPDX-License-Identifier: GPL-3.0-or-later

package change

import "testing"

// The count decides, not the type alone. Removing an option nobody uses costs
// nothing, whatever the type — that is what blocking on principle could not
// see.
func TestClassifyOptionRemovalIsSafeWhenNoRowUsesTheOption(t *testing.T) {
	for _, propType := range []string{"select", "multi_select", "status"} {
		if got := ClassifyOptionRemoval(propType, 0); got != ClassSafe {
			t.Errorf("ClassifyOptionRemoval(%q, 0) = %v, want ClassSafe", propType, got)
		}
	}
}

// Measured on 2026-09-24: on a status, the rows are REASSIGNED to another
// option, not emptied. The data is replaced by a plausible, false value —
// indistinguishable after the fact.
func TestClassifyOptionRemovalOnStatusWithRowsIsSilentRewrite(t *testing.T) {
	if got := ClassifyOptionRemoval("status", 47); got != ClassSilentRewrite {
		t.Errorf("ClassifyOptionRemoval(status, 47) = %v, want ClassSilentRewrite", got)
	}
}

// Measured on 2026-09-24: on a select, the row is emptied. Lost, but visibly.
func TestClassifyOptionRemovalOnSelectWithRowsIsDestructive(t *testing.T) {
	for _, propType := range []string{"select", "multi_select"} {
		if got := ClassifyOptionRemoval(propType, 3); got != ClassDestructive {
			t.Errorf("ClassifyOptionRemoval(%q, 3) = %v, want ClassDestructive", propType, got)
		}
	}
}

// A negative count says "not measured". It is neither safe nor dangerous: it
// is unknown, and saying so is the only honest answer.
func TestClassifyOptionRemovalIsUnknownWhenNotMeasured(t *testing.T) {
	for _, propType := range []string{"select", "status"} {
		if got := ClassifyOptionRemoval(propType, -1); got != ClassUnknownImpact {
			t.Errorf("ClassifyOptionRemoval(%q, -1) = %v, want ClassUnknownImpact", propType, got)
		}
	}
}

func TestClassStringIsStable(t *testing.T) {
	tests := []struct {
		c    Class
		want string
	}{
		{ClassSafe, "safe"},
		{ClassMigration, "migration required"},
		{ClassDestructive, "destructive"},
		{ClassSilentRewrite, "silent rewrite"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Class(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

// Measured on 2026-09-25: under a type change, an option that is not
// redeclared empties its rows toward select or multi_select, and reassigns
// them to the first declared option toward status.
func TestClassifyRetypedOptionRemovalFollowsTheNewType(t *testing.T) {
	tests := []struct {
		to    string
		count int
		want  Class
	}{
		{"multi_select", 3, ClassDestructive},
		{"select", 3, ClassDestructive},
		{"status", 3, ClassSilentRewrite},
		{"status", 0, ClassSafe},
		{"status", -1, ClassUnknownImpact},
	}
	for _, tt := range tests {
		if got := ClassifyRetypedOptionRemoval(tt.to, tt.count); got != tt.want {
			t.Errorf("ClassifyRetypedOptionRemoval(%q, %d) = %v, want %v", tt.to, tt.count, got, tt.want)
		}
	}
}
