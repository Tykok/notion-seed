// SPDX-License-Identifier: GPL-3.0-or-later

// Package change porte la classification des changements : ce qui est sûr, ce
// qui exige une migration, ce qui détruit, et ce qui réécrit silencieusement.
//
// C'est un paquet feuille, sans dépendance interne, précisément pour que
// `resources` puisse porter une Class sur chaque ligne de changement sans créer
// de cycle avec `diff`, qui importe `resources`.
package change

// Class est la catégorie d'un changement. Elle décrit ce qu'il COÛTE — sûr,
// migration requise, destructif, réécriture silencieuse, ou impact inconnu —
// elle ne décide plus s'il passe : notion-seed ne refuse plus rien sur la foi
// d'une classe, il la mesure et la dit.
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
	//
	// select et multi_select partagent la classe, PAS le comportement, et le
	// rendu les sépare : mesuré le 2026-09-24, un select retiré vide la cellule,
	// un multi_select ne lui retire que cette valeur — ['Un','Deux'] moins 'Un'
	// donne ['Deux']. La perte est réelle des deux côtés, son étendue non.
	ClassDestructive

	// ClassSilentRewrite : suppression d'une option de status. Les lignes qui
	// la portaient sont réassignées à UNE AUTRE option, sans erreur ni
	// avertissement de l'API. La donnée n'est pas seulement perdue : elle est
	// remplacée par une valeur fausse, indistinguable après coup.
	//
	// « une autre » et pas « l'option par défaut » : la mesure du 2026-09-24
	// donne À faire → Fait, qui n'est pas l'option par défaut du groupe. Laquelle
	// l'API choisit n'a pas été mesuré, donc n'est pas affirmé.
	ClassSilentRewrite

	// ClassUnknownImpact : on ne sait pas ce que ce changement coûte. Un couple
	// de types hors de la table mesurée, ou une mesure qui n'a pas pu être
	// faite (--skip-preflight, requête en échec).
	//
	// En dernier de l'énumération DÉLIBÉRÉMENT : WorstClass prend le maximum, et
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

// ClassifyOptionRemoval donne le coût du retrait d'une option, selon le type de
// la propriété ET le nombre de lignes qui la portent.
//
// count < 0 signifie « non mesuré » : sous --skip-preflight, ou quand la
// requête de comptage a échoué.
//
// Mesuré le 2026-09-24 contre l'API 2025-09-03 : retirer une option de select
// vide la ligne ; retirer une option de status RÉASSIGNE la ligne à une autre
// option, sans erreur ni avertissement. Le premier cas perd une donnée, le
// second la remplace par une valeur plausible et fausse.
//
// Le compte change tout : une option que personne n'utilise peut être retirée
// sans rien coûter, quel que soit son type. C'est ce que le blocage par
// principe ne savait pas voir, et pourquoi il a été remplacé par une mesure.
func ClassifyOptionRemoval(propertyType string, count int) Class {
	switch {
	case count < 0:
		return ClassUnknownImpact
	case count == 0:
		return ClassSafe
	case propertyType == "status":
		return ClassSilentRewrite
	default:
		return ClassDestructive
	}
}
