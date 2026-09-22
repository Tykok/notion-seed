// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import "testing"

// Le cœur de la classification, mesuré au spike : les trois types ne se
// comportent PAS pareil quand on retire une option.
//
//	select        → la ligne passe à null. Donnée perdue, pas de fausse valeur.
//	multi_select  → l'option quitte la liste. Donnée perdue, pas de fausse valeur.
//	status        → la ligne est réassignée à l'option par défaut. Donnée
//	                perdue ET remplacée par une valeur fausse.
func TestClassifyOptionRemoval(t *testing.T) {
	tests := []struct {
		propertyType string
		want         Class
	}{
		{"select", ClassDestructive},
		{"multi_select", ClassDestructive},
		{"status", ClassSilentRewrite},
	}
	for _, tt := range tests {
		t.Run(tt.propertyType, func(t *testing.T) {
			if got := ClassifyOptionRemoval(tt.propertyType); got != tt.want {
				t.Errorf("ClassifyOptionRemoval(%q) = %v, want %v", tt.propertyType, got, tt.want)
			}
		})
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

// Une réécriture silencieuse ne doit jamais être autorisable par le même
// garde-fou qu'une suppression ordinaire : l'utilisateur croit consentir à une
// perte, il consent à une falsification.
func TestSilentRewriteIsNotCoveredByAllowDataLoss(t *testing.T) {
	if ClassSilentRewrite.CoveredByAllowDataLoss() {
		t.Error("ClassSilentRewrite ne doit pas être couverte par allow_data_loss")
	}
	if !ClassDestructive.CoveredByAllowDataLoss() {
		t.Error("ClassDestructive doit être couverte par allow_data_loss")
	}
}
