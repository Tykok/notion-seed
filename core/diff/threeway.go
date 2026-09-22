// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Result est la sortie du comparateur pour UNE ressource.
//
// Les trois champs répondent à trois questions distinctes, et les mélanger
// serait l'erreur à ne pas commettre : Changeset dit ce qu'on écrirait, Drift
// dit ce que quelqu'un a fait à la main, Unmanaged dit ce qui existe sans être
// déclaré et qu'on ne touchera pas.
type Result struct {
	Changeset resources.Changeset
	Drift     []string
	Unmanaged []string
}

// CompareDatabase compare les trois voies d'une database.
//
// Un pointeur nul signifie « absente de cette voie » :
//   - desired nil, applied non nil  → ressource orpheline, destruction planifiée
//   - applied nil                   → jamais appliquée, donc création
//   - actual nil avec applied non nil → l'appelant n'a pas pu la lire ; ce cas
//     est traité par Compute, pas ici, parce que la raison (404, archivée) vient
//     du transport.
//
// La fonction est PURE : ni réseau, ni fichier, ni horloge. C'est ce qui permet
// de la couvrir en table sur des triplets, et c'est là que vit toute la sûreté
// du produit.
func CompareDatabase(key string, desired, applied, actual *state.Database) Result {
	res := Result{Changeset: resources.Changeset{Resource: "database." + key}}

	switch {
	case desired == nil && applied == nil:
		return res

	case desired == nil:
		// Dans le state, plus dans la config : l'identité n'a plus d'ancre
		// déclarée. Plus strict que la règle des propriétés hors config, et c'est
		// voulu — garder un id « géré mais non déclaré » le rendrait invisible.
		res.Changeset.Kind = resources.KindDestroy
		res.Changeset.Details = []resources.Detail{{
			Op:     "-",
			Target: "database." + key,
			Note:   "présente dans le state, absente de la configuration",
			Class:  change.ClassDestructive,
		}}
		return res

	case actual == nil && applied != nil:
		// Le state l'ancre, mais le réel n'a pas été lu — c'est le cas de
		// --skip-preflight, qui ne fait aucun appel. On ne sait rien, donc on ne
		// dit rien : annoncer une création ici proposerait de recréer une
		// database déjà importée.
		return res

	case actual == nil:
		// Jamais appliquée : création complète, aucun appel API n'a eu lieu.
		res.Changeset.Kind = resources.KindCreate
		for _, name := range sortedPropNames(desired.Properties) {
			res.Changeset.Details = append(res.Changeset.Details, resources.Detail{
				Op:     "+",
				Target: fmt.Sprintf("property %q (%s)", name, desired.Properties[name].Type),
				Class:  change.ClassSafe,
			})
		}
		return res
	}

	// Les trois voies existent : diff fin.
	res.Drift = driftLines(applied, actual)
	res.Changeset.Details = planLines(desired, applied, actual)
	res.Unmanaged = unmanagedLines(desired, actual)

	if len(res.Changeset.Details) > 0 {
		res.Changeset.Kind = resources.KindUpdate
	}
	return res
}

// planLines produit ce qu'on écrirait pour ramener `actual` vers `desired`.
func planLines(desired, applied, actual *state.Database) []resources.Detail {
	var out []resources.Detail

	if desired.Name != "" && desired.Name != actual.Name {
		out = append(out, resources.Detail{
			Op:     "~",
			Target: "name",
			Note:   fmt.Sprintf("%q → %q", actual.Name, desired.Name),
			Class:  change.ClassSafe,
		})
	}
	if desired.Description != actual.Description {
		out = append(out, resources.Detail{
			Op: "~", Target: "description", Class: change.ClassSafe,
		})
	}

	for _, name := range sortedPropNames(desired.Properties) {
		want := desired.Properties[name]
		have, exists := actual.Properties[name]

		if !exists {
			note := ""
			// Un nom disparu du YAML en même temps qu'un autre apparaît du même
			// type : très probablement un renommage. On ne DÉCIDE rien là-dessus
			// (une propriété hors config n'est jamais touchée), on prévient, parce
			// que l'utilisateur croit avoir renommé et va trouver une colonne vide.
			if old := renamedFrom(name, want, desired, applied); old != "" {
				note = fmt.Sprintf(
					"%q n'est pas renommée, elle reste hors config avec ses données", old)
			}
			out = append(out, resources.Detail{
				Op:     "+",
				Target: fmt.Sprintf("property %q (%s)", name, want.Type),
				Note:   note,
				Class:  change.ClassSafe,
			})
			continue
		}

		if want.Type != have.Type {
			out = append(out, resources.Detail{
				Op:     "~",
				Target: fmt.Sprintf("property %q", name),
				Note:   fmt.Sprintf("%s → %s", have.Type, want.Type),
				Class:  change.ClassDestructive,
			})
			continue
		}
		if want.Type == "number" && want.Format != "" && want.Format != have.Format {
			out = append(out, resources.Detail{
				Op:     "~",
				Target: fmt.Sprintf("property %q", name),
				Note:   fmt.Sprintf("format %s → %s", have.Format, want.Format),
				Class:  change.ClassSafe,
			})
		}
		out = append(out, optionLines(name, want, have, applied.Properties[name])...)
	}
	return out
}

// optionLines compare les options d'une propriété.
//
// L'appariement suit la chaîne d'identité : applied ↔ actual par id Notion,
// desired ↔ applied par key si déclarée, sinon par nom. C'est elle qui rend un
// renommage visible plutôt que de le faire passer pour un retrait suivi d'un
// ajout — et un retrait d'option de status, lui, réassigne silencieusement les
// lignes.
func optionLines(propName string, want, have, applied state.Property) []resources.Detail {
	if len(want.Options) == 0 && len(have.Options) == 0 {
		return nil
	}

	// key → nom actuel dans Notion, en passant par l'id.
	nameByKey := map[string]string{}
	nameByID := map[string]string{}
	for _, o := range have.Options {
		nameByID[o.ID] = o.Name
	}
	for _, o := range applied.Options {
		if o.Key != "" {
			if n, ok := nameByID[o.ID]; ok {
				nameByKey[o.Key] = n
			}
		}
	}

	actualNames := map[string]bool{}
	for _, o := range have.Options {
		actualNames[o.Name] = true
	}

	var out []resources.Detail
	claimed := map[string]bool{} // noms distants couverts par une option désirée

	for _, w := range want.Options {
		if w.Key != "" {
			if current, ok := nameByKey[w.Key]; ok {
				claimed[current] = true
				if current != w.Name {
					out = append(out, resources.Detail{
						Op:     "~",
						Target: fmt.Sprintf("option %q → %q (propriété %q)", current, w.Name, propName),
						Note:   "l'API répond 200 sans rien changer : créer, migrer les lignes, puis retirer",
						Class:  change.ClassMigration,
					})
				}
				continue
			}
		}
		if actualNames[w.Name] {
			claimed[w.Name] = true
			continue
		}
		out = append(out, resources.Detail{
			Op:     "+",
			Target: fmt.Sprintf("option %q (propriété %q)", w.Name, propName),
			Class:  change.ClassSafe,
		})
	}

	// Ce qui existe dans Notion et que le YAML ne réclame pas SERA détruit dès
	// qu'on écrit cette propriété : l'API remplace la liste entière au lieu de
	// la fusionner. C'est l'asymétrie avec les propriétés, et elle doit être dite.
	for _, o := range have.Options {
		if claimed[o.Name] {
			continue
		}
		out = append(out, resources.Detail{
			Op:     "-",
			Target: fmt.Sprintf("option %q (propriété %q)", o.Name, propName),
			Note:   "absente du YAML : l'API remplace la liste entière des options",
			Class:  change.ClassifyOptionRemoval(have.Type),
		})
	}
	return out
}

// driftLines dit ce qui a bougé dans Notion depuis la dernière application.
// C'est un constat, jamais une action : la réconciliation vers le YAML est
// calculée séparément par planLines.
func driftLines(applied, actual *state.Database) []string {
	var out []string
	if applied.Name != "" && applied.Name != actual.Name {
		out = append(out, fmt.Sprintf("~ nom %q → %q", applied.Name, actual.Name))
	}

	for _, name := range sortedPropNames(applied.Properties) {
		was := applied.Properties[name]
		is, exists := actual.Properties[name]
		if !exists {
			out = append(out, fmt.Sprintf("- propriété %q supprimée hors de notion-seed", name))
			continue
		}
		if was.Type != is.Type {
			out = append(out, fmt.Sprintf(
				"~ propriété %q : type %s → %s hors de notion-seed", name, was.Type, is.Type))
		}

		nameByID := map[string]string{}
		for _, o := range is.Options {
			nameByID[o.ID] = o.Name
		}
		for _, o := range was.Options {
			current, ok := nameByID[o.ID]
			if !ok {
				out = append(out, fmt.Sprintf(
					"- option %q de la propriété %q retirée hors de notion-seed", o.Name, name))
				continue
			}
			if current != o.Name {
				out = append(out, fmt.Sprintf(
					"~ option %q de la propriété %q renommée en %q hors de notion-seed",
					o.Name, name, current))
			}
		}
	}
	return out
}

// unmanagedLines liste ce qui existe dans Notion sans être déclaré. Non touché :
// l'API modifie les propriétés une par une, donc ne pas les déclarer suffit à
// ne pas y toucher. (Ce raisonnement ne vaut PAS pour les options : voir
// optionLines.)
func unmanagedLines(desired, actual *state.Database) []string {
	var out []string
	for _, name := range sortedPropNames(actual.Properties) {
		if _, declared := desired.Properties[name]; declared {
			continue
		}
		out = append(out, fmt.Sprintf(
			"property %q (%s)", name, actual.Properties[name].Type))
	}
	return out
}

// renamedFrom cherche la propriété du state, de même type, que le YAML ne
// déclare plus : le signe d'un renommage. Heuristique d'AFFICHAGE, jamais de
// décision d'écriture.
func renamedFrom(newName string, want state.Property, desired, applied *state.Database) string {
	for _, old := range sortedPropNames(applied.Properties) {
		if old == newName {
			continue
		}
		if _, stillDeclared := desired.Properties[old]; stillDeclared {
			continue
		}
		if applied.Properties[old].Type == want.Type {
			return old
		}
	}
	return ""
}

func sortedPropNames(m map[string]state.Property) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// worstClass rend la classe la plus grave d'un jeu de détails. L'ordre de
// l'énumération va du plus sûr au plus grave, donc le maximum suffit.
func worstClass(ds []resources.Detail) change.Class {
	worst := change.ClassSafe
	for _, d := range ds {
		if d.Class > worst {
			worst = d.Class
		}
	}
	return worst
}
