// SPDX-License-Identifier: GPL-3.0-or-later

package change

import "testing"

// La table ne contient QUE ce qui a été mesuré contre l'API le 2026-09-24.
// Un couple absent doit rendre ClassUnknownImpact — affirmer « sûr » sur un
// couple jamais essayé serait exactement le défaut que ce produit dénonce.
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

// Un type inchangé n'est pas un changement de type : la question ne se pose pas.
func TestClassifyTypeChangeIsSafeWhenTypeDoesNotChange(t *testing.T) {
	if got := ClassifyTypeChange("select", "select"); got != ClassSafe {
		t.Errorf("ClassifyTypeChange sur un type inchangé = %v, want ClassSafe", got)
	}
}

func TestClassUnknownImpactHasItsOwnLabel(t *testing.T) {
	if got := ClassUnknownImpact.String(); got != "impact inconnu" {
		t.Errorf("String() = %q, want \"impact inconnu\"", got)
	}
}
