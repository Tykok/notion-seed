// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"io"
	"strings"

	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Render écrit le plan en texte brut. notion-seed est un outil de terminal :
// pas d'interface, pas de couleur inconditionnelle, une sortie qui reste
// lisible dans un pipe et en CI.
//
// L'ordre est délibéré : la dérive d'abord (ce que quelqu'un a fait), le plan
// ensuite (ce qu'on ferait), le hors config après (ce qu'on ne touchera pas),
// le non comparé ensuite (ce qu'on n'a pas vérifié), le blocage en dernier
// avec son issue. Chaque section disparaît si elle est vide : sans state, la
// sortie est exactement celle d'avant.
func Render(w io.Writer, p *Plan) error {
	if len(p.Drifts) > 0 {
		if _, err := fmt.Fprintln(w, "Dérive détectée hors de notion-seed"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, d := range p.Drifts {
			if _, err := fmt.Fprintf(w, "  ~ %s\n", d.Resource); err != nil {
				return err
			}
			for _, line := range d.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	// Un plan bloqué n'est jamais « aucun changement » : une ressource gérée
	// introuvable ou archivée ne produit ni Change ni Unmanaged, seulement une
	// raison de blocage. Sortir ici afficherait la conformité tout en rendant
	// un code d'erreur.
	//
	// Une ressource non comparée (--skip-preflight) n'est pas non plus « aucun
	// changement » : on ne sait rien d'elle, donc on ne peut pas affirmer
	// qu'elle est conforme.
	if len(p.Changes) == 0 && len(p.Unmanaged) == 0 && len(p.NotCompared) == 0 &&
		len(p.StaleState) == 0 && !p.Blocked {
		_, err := fmt.Fprintln(w, "Aucun changement. La configuration correspond à l'état réel.")
		return err
	}

	if len(p.Changes) > 0 {
		if _, err := fmt.Fprintf(w, "Plan: %d to add, %d to change, %d to destroy\n\n",
			p.ToAdd, p.ToChange, p.ToDestroy); err != nil {
			return err
		}
	}

	for _, c := range p.Changes {
		// Le marqueur suit le Kind de la ressource — ce que l'opération FAIT
		// (créer, détruire, modifier) — pas sa Class : notion-seed ne bloque plus
		// sur la foi d'une classe, donc la classe n'a plus à décider d'un
		// marqueur d'alerte. Le coût, lui, reste visible juste après : l'étiquette
		// [classe] sur l'en-tête et sur chaque ligne concernée.
		marker := "~"
		switch c.Kind {
		case resources.KindCreate:
			marker = "+"
		case resources.KindDestroy:
			marker = "-"
		}

		header := fmt.Sprintf("  %s %s", marker, c.Resource)
		if c.Detail != "" {
			header += " " + c.Detail
		}
		if c.Class != ClassSafe {
			header += fmt.Sprintf("  [%s]", c.Class)
		}
		if _, err := fmt.Fprintln(w, header); err != nil {
			return err
		}
		for _, d := range c.Details {
			line := d.Op + " " + d.Target
			if d.Note != "" {
				// PAS de %q ici : Note porte déjà ses propres guillemets là où il en
				// faut (un renommage rend `"Ancien" → "Nouveau"`). Un %q supplémentaire
				// ré-échappe ces guillemets et l'ensemble de la note, jusqu'à rendre
				// illisible la seule ligne censée éviter qu'on croie avoir renommé une
				// propriété alors qu'elle reste hors config.
				line += " — " + d.Note
			}
			suffix := ""
			// Une ligne sûre dans une ressource par ailleurs dangereuse ne doit pas
			// hériter de l'étiquette : c'est la ligne qui porte sa classe.
			if d.Class != ClassSafe {
				suffix = fmt.Sprintf("  [%s]", d.Class)
			}
			if _, err := fmt.Fprintf(w, "      %s%s\n", line, suffix); err != nil {
				return err
			}
			// Le chiffre, juste sous la ligne qu'il concerne : c'est lui qu'on lit
			// pour décider, et il ne veut rien dire détaché de sa cible.
			if cq := consequence(d); cq != "" {
				if _, err := fmt.Fprintf(w, "          → %s.\n", cq); err != nil {
					return err
				}
			}
		}
		// lifecycle ne bloque plus rien : ces clés ne sont que des accusés de
		// lecture. Les taire les rendrait invisibles, et l'utilisateur ne saurait
		// plus ce qu'il a déjà reconnu.
		for _, a := range c.Acknowledged {
			if _, err := fmt.Fprintf(w,
				"      → déclarée dans lifecycle.%s.\n", a); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	// La ligne d'agrégat vient après la liste : on lit le détail, puis le total
	// par famille. Elle disparaît quand rien de mesuré n'est en jeu — annoncer
	// un impact vide serait une affirmation de plus que ce qui a été mesuré.
	if line := Impact(p); line != "" {
		if _, err := fmt.Fprintf(w, "%s\n\n", line); err != nil {
			return err
		}
	}

	if len(p.Unmanaged) > 0 {
		if _, err := fmt.Fprintln(w, "Hors config — présent dans Notion, non touché"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, u := range p.Unmanaged {
			if _, err := fmt.Fprintf(w, "  %s\n", u.Resource); err != nil {
				return err
			}
			for _, line := range u.Lines {
				if _, err := fmt.Fprintf(w, "      %s\n", line); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}

	if len(p.StaleState) > 0 {
		if _, err := fmt.Fprintln(w,
			"Entrée de state obsolète — la ressource n'existe plus dans Notion"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.StaleState {
			if _, err := fmt.Fprintf(w,
				"  - %s — son identité sera retirée du state, rien ne sera écrit dans Notion\n",
				r); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if len(p.NotCompared) > 0 {
		if _, err := fmt.Fprintln(w, "Non comparé — --skip-preflight ne lit pas l'état réel"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.NotCompared {
			if _, err := fmt.Fprintf(w, "  %s\n", r); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if p.Blocked {
		if _, err := fmt.Fprintln(w, "Plan bloqué. Rien n'a été appliqué."); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, r := range p.BlockedReasons {
			if _, err := fmt.Fprintf(w, "  %s\n", r); err != nil {
				return err
			}
		}
	}
	return nil
}

// bound écrit un compte, en le marquant comme minorant s'il est plafonné.
// « 300 » et « plus de 300 » ne se décident pas pareil.
func bound(n int, capped bool) string {
	if capped {
		return fmt.Sprintf("plus de %d", n)
	}
	return fmt.Sprintf("%d", n)
}

// consequence rend, en clair, ce que ce détail va coûter. C'est la phrase que
// l'utilisateur lit pour décider, donc elle ne doit jamais affirmer plus que ce
// qui a été mesuré.
func consequence(d resources.Detail) string {
	if d.Measure == nil || d.Count < 0 {
		if d.Class == ClassUnknownImpact {
			return "impact non mesuré ; relancez en ligne pour l'obtenir"
		}
		return ""
	}
	count := fmt.Sprintf("%d lignes", d.Count)
	if d.Capped {
		count = fmt.Sprintf("plus de %d lignes", d.Count)
	}
	if d.Count == 0 {
		return "0 ligne concernée"
	}

	// Retrait d'option : le sort des lignes dépend du type, mesuré le 2026-09-24.
	if d.Measure.Option != "" {
		if d.Measure.PropertyType == "status" {
			return count + " seront réassignées à une autre option, sans trace"
		}
		return count + " perdront leur valeur"
	}
	// Changement de type : le compte est celui des valeurs NON VIDES, donc un
	// majorant de ce qui sera réellement perdu.
	if d.Class == ClassSilentRewrite {
		// Plafonné, ce majorant devient lui-même un minorant : « jusqu'à plus de
		// 300 » borne par le haut ce qu'on ne sait justement plus borner. On dit
		// alors le plancher mesuré, et on assume de ne pas savoir le plafond.
		if d.Capped {
			return count + " non vides, dont un nombre non mesuré seront appauvries, sans trace"
		}
		return "jusqu'à " + count + " appauvries, sans trace"
	}
	return count + " non vides dans cette colonne"
}

// Impact agrège les comptes mesurés en une phrase. C'est le produit en une
// ligne : le seul chiffre que personne d'autre ne peut donner.
//
// Il n'additionne JAMAIS deux familles différentes. Le comptage dit combien de
// lignes sont non vides sur une colonne qui change de type ; il ne dit pas
// combien portent plusieurs valeurs, donc combien perdront vraiment quelque
// chose. D'où « jusqu'à N » d'un côté et un compte ferme de l'autre : un
// chiffre faux ici ruinerait le seul argument du produit.
//
// Un compte plafonné contamine sa famille, et elle seule : une somme dont un
// terme est un minorant est un minorant, mais le plafond de l'une ne rend pas
// l'autre plus floue qu'elle n'est.
func Impact(p *Plan) string {
	reassigned, lost, weakened, destroyed := 0, 0, 0, 0
	var reassignedCapped, lostCapped, weakenedCapped bool
	for _, c := range p.Changes {
		if c.Kind == resources.KindDestroy {
			destroyed++
		}
		for _, d := range c.Details {
			if d.Measure == nil || d.Count <= 0 {
				continue
			}
			switch {
			case d.Measure.Option != "" && d.Measure.PropertyType == "status":
				reassigned += d.Count
				reassignedCapped = reassignedCapped || d.Capped
			case d.Measure.Option != "":
				lost += d.Count
				lostCapped = lostCapped || d.Capped
			case d.Class == ClassSilentRewrite:
				weakened += d.Count
				weakenedCapped = weakenedCapped || d.Capped
			}
		}
	}

	var parts []string
	if reassigned > 0 {
		parts = append(parts, fmt.Sprintf("%s lignes réassignées sans trace",
			bound(reassigned, reassignedCapped)))
	}
	if lost > 0 {
		parts = append(parts, fmt.Sprintf("%s lignes perdront leur valeur",
			bound(lost, lostCapped)))
	}
	if weakened > 0 {
		// Non plafonné, le compte des non vides borne par le haut ce qui sera
		// perdu. Plafonné, il ne borne plus rien par le haut : on ne peut plus
		// dire « jusqu'à », et le plancher mesuré vit sur la ligne du détail.
		if weakenedCapped {
			parts = append(parts, "un nombre non mesuré de lignes appauvries sans trace")
		} else {
			parts = append(parts, fmt.Sprintf("jusqu'à %d lignes appauvries sans trace", weakened))
		}
	}
	if destroyed > 0 {
		parts = append(parts, fmt.Sprintf("%d database(s) à la corbeille", destroyed))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Impact : " + strings.Join(parts, ", ") + "."
}
