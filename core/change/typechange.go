// SPDX-License-Identifier: GPL-3.0-or-later

package change

// typeChangeImpact est ce que l'API Notion fait d'une colonne quand son type
// change. MESURÉ le 2026-09-24 contre l'API 2025-09-03 (ntn 0.22.11), sur une
// ligne remplie, database jetable dans un espace personnel.
//
// 7 couples sur 90 possibles. Tout le reste est inconnu et doit le rester :
// écrire « sûr » sur un couple jamais essayé serait précisément le défaut que
// cet outil existe pour rendre visible.
//
//	de           vers          ligne avant      ligne après
//	select    → multi_select   "Alpha"          ["Alpha"]        sans perte
//	number    → rich_text      7                "7"              sans perte
//	status    → select         "Ouvert"         "Ouvert"         sans perte
//	date      → rich_text      2026-01-15       "2026-01-15"     sans perte
//	multi_select → select      ["Un","Deux"]    "Un"             RÉÉCRITURE
//	rich_text → number         "42 texte"       42               RÉÉCRITURE
//	checkbox  → number         true             (vide)           DESTRUCTIF
//
// multi_select → select mérite l'attention : la ligne portait deux valeurs,
// elle en porte une, et plus rien ne dit que la seconde a existé. C'est la
// définition exacte de la réécriture silencieuse, et elle n'était donc pas
// propre au status.
var typeChangeImpact = map[[2]string]Class{
	{"select", "multi_select"}: ClassSafe,
	{"number", "rich_text"}:    ClassSafe,
	{"status", "select"}:       ClassSafe,
	{"date", "rich_text"}:      ClassSafe,

	{"multi_select", "select"}: ClassSilentRewrite,
	{"rich_text", "number"}:    ClassSilentRewrite,

	{"checkbox", "number"}: ClassDestructive,
}

// ClassifyTypeChange dit ce que coûte le passage d'un type à un autre.
//
// Un couple absent de la table rend ClassUnknownImpact : la réponse honnête
// quand personne n'a essayé.
func ClassifyTypeChange(from, to string) Class {
	if from == to {
		return ClassSafe
	}
	if c, measured := typeChangeImpact[[2]string{from, to}]; measured {
		return c
	}
	return ClassUnknownImpact
}
