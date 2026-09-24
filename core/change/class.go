// SPDX-License-Identifier: GPL-3.0-or-later

// Package change porte la classification des changements : ce qui est sûr, ce
// qui exige une migration, ce qui détruit, et ce qui réécrit silencieusement.
//
// C'est un paquet feuille, sans dépendance interne, précisément pour que
// `resources` puisse porter une Class sur chaque ligne de changement sans créer
// de cycle avec `diff`, qui importe `resources`.
package change

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

	// ClassUnknownImpact : on ne sait pas ce que ce changement coûte. Un couple
	// de types hors de la table mesurée, ou une mesure qui n'a pas pu être
	// faite (--skip-preflight, requête en échec).
	//
	// En dernier de l'énumération DÉLIBÉRÉMENT : worstClass prend le maximum, et
	// un impact qu'on ne sait pas nommer doit dominer l'en-tête d'une ressource.
	// Ne pas savoir mérite plus d'attention que savoir que c'est sûr.
	ClassUnknownImpact
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
	case ClassUnknownImpact:
		return "impact inconnu"
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
