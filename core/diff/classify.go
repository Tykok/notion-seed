// SPDX-License-Identifier: GPL-3.0-or-later

// Package diff calcule le plan de changements et le rend en texte.
package diff

// Class est la catégorie d'un changement. Elle détermine le comportement par
// défaut : appliqué, migré, refusé, ou refusé sans échappatoire ordinaire.
type Class int

const (
	// ClassSafe : ajout d'une database, d'une propriété, d'une option neuve.
	ClassSafe Class = iota

	// ClassMigration : même key d'option, name différent. L'API ne sait pas
	// renommer une option — elle accepte la requête et renvoie 200 sans rien
	// changer. Il faut créer, migrer les lignes, puis retirer l'ancienne.
	ClassMigration

	// ClassDestructive : suppression d'une option de select ou multi_select,
	// suppression d'une propriété, changement de type. La donnée est perdue,
	// mais aucune fausse valeur n'est écrite.
	ClassDestructive

	// ClassSilentRewrite : suppression d'une option de status. Les lignes qui
	// la portaient sont réassignées à l'option par défaut, sans erreur ni
	// avertissement de l'API. La donnée n'est pas seulement perdue : elle est
	// remplacée par une valeur fausse, indistinguable après coup.
	ClassSilentRewrite
)

func (c Class) String() string {
	switch c {
	case ClassSafe:
		return "sûr"
	case ClassMigration:
		return "migration requise"
	case ClassDestructive:
		return "destructif"
	case ClassSilentRewrite:
		return "réécriture silencieuse"
	default:
		return "inconnu"
	}
}

// CoveredByAllowDataLoss dit si lifecycle.allow_data_loss suffit à autoriser
// ce changement. Une réécriture silencieuse n'est jamais couverte : consentir
// à perdre une donnée n'est pas consentir à ce qu'elle soit remplacée par une
// autre valeur, plausible et fausse.
func (c Class) CoveredByAllowDataLoss() bool {
	return c == ClassDestructive
}

// Blocking dit si ce changement arrête le plan par défaut.
func (c Class) Blocking() bool {
	return c == ClassDestructive || c == ClassSilentRewrite
}

// ClassifyOptionRemoval donne la classe du retrait d'une option, selon le type
// de la propriété. Mesuré au spike : les trois types ne se comportent pas
// pareil.
func ClassifyOptionRemoval(propertyType string) Class {
	if propertyType == "status" {
		return ClassSilentRewrite
	}
	return ClassDestructive
}
