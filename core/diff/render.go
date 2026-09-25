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
func Render(w io.Writer, p *Plan) error { return render(w, p, Impact(p)) }

// RenderForApply écrit le plan comme Render, à une ligne près : l'agrégat
// d'impact ne porte que sur ce qu'apply VA écrire.
//
// Une ressource retenue garde un impact qu'apply ne causera pas. Reprendre le
// total de plan ferait mentir la ligne la plus lue du produit au seul moment où
// l'utilisateur décide, juste avant la confirmation. Sans ressource retenue, les
// deux rendus coïncident — c'est le cas courant.
func RenderForApply(w io.Writer, p *Plan) error { return render(w, p, WritableImpact(p)) }

// render écrit le plan. impact est la ligne d'agrégat déjà calculée, "" pour
// n'en afficher aucune : son périmètre est le choix de l'appelant — tout le plan
// pour plan, ce qui sera écrit pour apply.
func render(w io.Writer, p *Plan, impact string) error {
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
	if impact != "" {
		if _, err := fmt.Fprintf(w, "%s\n\n", impact); err != nil {
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
	// Aucune demande de mesure : la ligne ne coûte rien à personne, et il n'y a
	// rien à annoncer. C'est le SEUL cas qui autorise le silence.
	if d.Measure == nil {
		return ""
	}
	if d.Measure.AllRows {
		return destroyedRows(d)
	}
	// Pas de compte. Se taire ici rendrait la ligne indiscernable d'une ligne
	// sans coût, à côté de voisines qui portent leur chiffre — et une réécriture
	// silencieuse qu'on croit anodine est le pire malentendu que ce rendu puisse
	// produire. Reste à dire LAQUELLE des deux raisons s'applique, parce qu'elles
	// ne se réparent pas pareil.
	if d.Count < 0 {
		// Question impossible à poser : aucun remède à proposer, donc aucun
		// promis. Annoncer « relancez » ici annoncerait une action corrective qui
		// n'arrivera jamais.
		if d.Unmeasurable {
			return fmt.Sprintf(
				"impact réel inconnu : notion-seed ne sait pas compter les lignes "+
					"d'une propriété %s", d.Measure.PropertyType)
		}
		// Question posable, réponse pas obtenue (--skip-preflight, 403, 429) :
		// relancer marche vraiment.
		return "impact non mesuré ; relancez en ligne pour l'obtenir"
	}
	if d.Count == 0 {
		return "0 ligne concernée"
	}
	// Sur UNE mesure, le compte est bien un nombre de lignes distinctes : un
	// filtre, une propriété. C'est en sommant plusieurs mesures que Impact perd
	// cette propriété, et c'est pour ça qu'il parle de valeurs, pas de lignes.
	count := bound(d.Count, d.Capped) + " lignes"

	// Migration : la ressource est retenue, rien ne sera écrit. Le compte porte
	// sur l'option ACTUELLE parce que c'est le coût du remède — les lignes à
	// déplacer à la main —, pas une perte. Laisser la ligne tomber dans le cas
	// du retrait ci-dessous annoncerait une réassignation ou un vidage qui
	// n'aura pas lieu : un chiffre faux sur la donnée de l'utilisateur.
	if d.Class == ClassMigration && d.Measure.Option != "" {
		return fmt.Sprintf("%s portent %q : à migrer à la main avant d'appliquer",
			count, d.Measure.Option)
	}

	// Retrait d'option : le sort des lignes dépend du type, et les TROIS cas
	// mesurés le 2026-09-24 diffèrent. Les confondre affirmerait plus que ce qui
	// a été mesuré, ce qui est le seul défaut que ce produit ne peut pas se
	// permettre.
	if d.Measure.Option != "" {
		switch removalFate(*d.Measure) {
		case "status":
			return count + " seront réassignées à une autre option, sans trace"
		case "multi_select":
			// Sous un changement de type depuis multi_select, rien n'a été
			// mesuré : on dit la perte, pas ce qu'il reste de la cellule.
			if d.Measure.Retyped {
				return count + " perdront cette valeur (sort exact non mesuré)"
			}
			// Mesuré : ['Un','Deux'] moins 'Un' donne ['Deux'] ; ['Un'] moins 'Un'
			// donne []. La ligne perd CETTE valeur, pas forcément toute sa cellule.
			//
			// On ne dit pas combien de lignes se videront : le filtre `contains`
			// compte les lignes portant l'option, pas celles qui n'en portent
			// qu'elle. Le savoir coûterait de relire chaque ligne, ce que la passe
			// de mesure ne fait pas — alors on nomme ce qu'on a.
			return count + " perdront cette valeur ; elles ne passeront à vide que si " +
				"elles n'en portaient pas d'autre"
		default:
			// select : mesuré, la cellule est vidée. La ligne n'en portait qu'une.
			return count + " passeront à vide"
		}
	}
	// Changement de type : le compte est celui des valeurs NON VIDES, donc un
	// majorant de ce qui sera réellement perdu.
	if d.Class == ClassSilentRewrite {
		// Plafonné, ce majorant devient lui-même un minorant : « jusqu'à plus de
		// 300 » borne par le haut ce qu'on ne sait justement plus borner. On dit
		// alors le plancher mesuré, et on assume de ne pas savoir le plafond.
		if d.Capped {
			return count + " non vides, dont un nombre non mesuré sera appauvri, sans trace"
		}
		return "jusqu'à " + count + " appauvries, sans trace"
	}
	return count + " non vides dans cette colonne"
}

// destroyedRows dit combien de lignes une database mise à la corbeille emporte.
// Non mesuré — comptage refusé, épuisé ou incompris —, il le dit : se taire
// rendrait la ligne indiscernable d'une database vide.
func destroyedRows(d resources.Detail) string {
	switch {
	case d.Count < 0:
		return "nombre de lignes qui partent à la corbeille avec elle non mesuré ; " +
			"relancez pour l'obtenir"
	case d.Count == 0:
		return "aucune ligne ne part à la corbeille avec elle"
	}
	return bound(d.Count, d.Capped) + " ligne(s) partent à la corbeille avec elle"
}

// removalFate dit de quel type le sort des lignes suit, pour une option qui
// part. C'est l'ancien type, sauf quand l'option disparaît avec un changement de
// type depuis status : il est alors traité comme un select, une perte.
// Seul select → multi_select a été mesuré (2026-09-25) ; les autres couples sont
// traités par prudence comme destructifs, sans que leur sort ait été observé.
func removalFate(m resources.Measurement) string {
	if m.Retyped && m.PropertyType == "status" {
		return "select"
	}
	return m.PropertyType
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
//
// Ces totaux comptent des VALEURS, pas des lignes distinctes, et le disent.
// Chaque compte est un nombre de lignes pour SA mesure, mais deux mesures d'une
// même database peuvent tomber sur les mêmes lignes : deux options retirées
// d'un même multi_select se filtrent par `contains`, et une ligne portant les
// deux est comptée deux fois. Écrire « 18 lignes » là où 10 lignes distinctes
// sont touchées serait un chiffre faux dans la ligne qui EST l'argument du
// produit. Le dédoublonnage exigerait de collecter les identifiants de lignes,
// ce que la passe de mesure ne fait pas — alors on nomme ce qu'on a.
func Impact(p *Plan) string { return impactOf(p.Changes) }

// WritableImpact agrège le même impact que Impact, sur les SEULES ressources
// qu'apply va écrire : celles dont Withheld est vide.
//
// plan continue d'agréger tout, parce qu'il décrit l'écart, pas une exécution.
// apply, lui, annonce ce qu'il va causer : une ressource retenue n'est pas
// écrite, donc son coût n'est pas le sien.
func WritableImpact(p *Plan) string {
	writable := make([]Change, 0, len(p.Changes))
	for _, c := range p.Changes {
		if c.Withheld == "" {
			writable = append(writable, c)
		}
	}
	return impactOf(writable)
}

// impactOf est le calcul lui-même, partagé par Impact et WritableImpact : une
// seule règle d'agrégat, deux périmètres. Deux règles finiraient par diverger,
// et plan et apply par annoncer deux coûts différents pour la même écriture.
func impactOf(changes []Change) string {
	reassigned, lost, weakened := 0, 0, 0
	var reassignedCapped, lostCapped, weakenedCapped bool
	var trash trashedRows
	for _, c := range changes {
		if c.Kind == resources.KindDestroy {
			trash.add(c)
		}
		for _, d := range c.Details {
			// Le compte d'une destruction est agrégé par trash, à part : ce sont
			// des lignes, pas des valeurs, et elles ne se mêlent à aucune famille.
			if d.Measure == nil || d.Measure.AllRows || d.Count <= 0 {
				continue
			}
			// Une ligne de migration retient sa ressource : rien n'est écrit, donc
			// rien n'est perdu. Son compte est le coût d'un remède manuel, que la
			// ligne elle-même affiche ; l'additionner ici en ferait une perte.
			if d.Class == ClassMigration {
				continue
			}
			switch {
			case d.Measure.Option != "" && removalFate(*d.Measure) == "status":
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
		parts = append(parts, fmt.Sprintf("%s valeurs réassignées sans trace",
			bound(reassigned, reassignedCapped)))
	}
	if lost > 0 {
		parts = append(parts, fmt.Sprintf("%s valeurs perdues",
			bound(lost, lostCapped)))
	}
	if weakened > 0 {
		// Non plafonné, le compte des non vides borne par le haut ce qui sera
		// perdu. Plafonné, il ne borne plus rien par le haut : on ne peut plus
		// dire « jusqu'à », et le plancher mesuré vit sur la ligne du détail.
		if weakenedCapped {
			parts = append(parts, "un nombre non mesuré de valeurs appauvries sans trace")
		} else {
			parts = append(parts, fmt.Sprintf("jusqu'à %d valeurs appauvries sans trace", weakened))
		}
	}
	if trash.databases > 0 {
		parts = append(parts, trash.String())
	}
	if len(parts) == 0 {
		return ""
	}
	return "Impact : " + strings.Join(parts, ", ") + "."
}

// trashedRows agrège les databases mises à la corbeille et les lignes qu'elles
// emportent.
//
// Ici, et ici seulement, le total parle de LIGNES : chaque ligne appartient à
// un seul data source, donc deux databases détruites ne comptent jamais deux
// fois la même. Un compte inconnu n'est jamais pris pour 0 : il est nommé, et le
// total connu devient un minorant.
type trashedRows struct {
	databases, rows, unknown int
	capped                   bool
}

func (t *trashedRows) add(c Change) {
	t.databases++
	for _, d := range c.Details {
		if d.Measure != nil && d.Measure.AllRows && d.Count >= 0 {
			t.rows += d.Count
			t.capped = t.capped || d.Capped
			return
		}
	}
	// Aucun compte obtenu pour cette database : ni 0, ni rien.
	t.unknown++
}

func (t trashedRows) String() string {
	head := fmt.Sprintf("%d database(s) à la corbeille", t.databases)
	switch {
	case t.unknown == t.databases:
		return head + ", lignes non comptées"
	case t.unknown > 0:
		return fmt.Sprintf("%s avec au moins %d ligne(s), lignes non comptées pour %d d'entre elles",
			head, t.rows, t.unknown)
	}
	return head + " avec " + bound(t.rows, t.capped) + " ligne(s)"
}
