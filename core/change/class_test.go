// SPDX-License-Identifier: GPL-3.0-or-later

package change

import "testing"

// Le compte décide, pas le type seul. Retirer une option que personne n'utilise
// ne coûte rien, quel que soit le type — c'est ce que le blocage par principe
// ne savait pas voir.
func TestClassifyOptionRemovalIsSafeWhenNoRowUsesTheOption(t *testing.T) {
	for _, propType := range []string{"select", "multi_select", "status"} {
		if got := ClassifyOptionRemoval(propType, 0); got != ClassSafe {
			t.Errorf("ClassifyOptionRemoval(%q, 0) = %v, want ClassSafe", propType, got)
		}
	}
}

// Mesuré le 2026-09-24 : sur un status, les lignes sont RÉASSIGNÉES à une autre
// option, pas vidées. La donnée est remplacée par une valeur plausible et
// fausse — indistinguable après coup.
func TestClassifyOptionRemovalOnStatusWithRowsIsSilentRewrite(t *testing.T) {
	if got := ClassifyOptionRemoval("status", 47); got != ClassSilentRewrite {
		t.Errorf("ClassifyOptionRemoval(status, 47) = %v, want ClassSilentRewrite", got)
	}
}

// Mesuré le 2026-09-24 : sur un select, la ligne passe à vide. Perdue, mais
// visiblement.
func TestClassifyOptionRemovalOnSelectWithRowsIsDestructive(t *testing.T) {
	for _, propType := range []string{"select", "multi_select"} {
		if got := ClassifyOptionRemoval(propType, 3); got != ClassDestructive {
			t.Errorf("ClassifyOptionRemoval(%q, 3) = %v, want ClassDestructive", propType, got)
		}
	}
}

// Un compte négatif dit « pas mesuré ». Ce n'est ni sûr ni dangereux : c'est
// inconnu, et le dire est la seule réponse honnête.
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
		{ClassSafe, "sûr"},
		{ClassMigration, "migration requise"},
		{ClassDestructive, "destructif"},
		{ClassSilentRewrite, "réécriture silencieuse"},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Class(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}
