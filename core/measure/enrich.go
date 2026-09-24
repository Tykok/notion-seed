// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
	"errors"
	"fmt"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/diff"
)

// Enrich exécute les demandes de mesure d'un plan et reclasse chaque détail.
//
// Ne rend JAMAIS d'erreur fatale : une mesure qui échoue laisse sa ligne en
// « impact inconnu » et sa cause est rendue pour affichage. Ne pas savoir n'est
// pas un échec de la commande — priver l'utilisateur du reste de son plan parce
// qu'un comptage a échoué le serait.
//
// dataSourceIDs associe la key de configuration à l'id du data source À
// INTERROGER. L'appelant le compose : l'id frais que le refresh vient de lire
// l'emporte, celui du state ne sert que de repli — mesurer sur un id périmé
// compterait les lignes d'un autre objet. Une ressource absente de cette table
// n'est pas mesurable : rien, ni relu ni en state, ne dit quoi interroger.
func Enrich(ctx context.Context, c Counter, dataSourceIDs map[string]string, p *diff.Plan) []string {
	var failures []string

	for i := range p.Changes {
		ch := &p.Changes[i]
		dsID := dataSourceIDs[ch.Key]

		for j := range ch.Details {
			d := &ch.Details[j]
			if d.Measure == nil || dsID == "" {
				continue
			}

			res, err := c.Count(ctx, Request{
				DataSourceID: dsID,
				Property:     d.Measure.Property,
				PropertyType: d.Measure.PropertyType,
				Option:       d.Measure.Option,
			})
			if err != nil {
				// La ligne reste inconnue, ce qu'elle était déjà. On ne dégrade
				// jamais vers « sûr » sur un échec.
				//
				// Un type non filtrable N'EST PAS une panne : c'est une question
				// qu'on ne sait pas poser, et notion-seed le sait d'avance sans
				// avoir rien tenté. Le verser dans la liste des échecs ferait
				// apparaître « comptage impossible » à chaque plan portant un tel
				// type, et apprendrait à ignorer une ligne qui signale par ailleurs
				// de vrais incidents — 403, 429 épuisé, réponse incomprise.
				// Le MÊME test tranche les deux conséquences : ne pas compter
				// l'incident, et marquer la ligne comme non mesurable pour que le
				// rendu cesse de promettre un remède inexistant. Relancer ne rendra
				// pas rich_text filtrable.
				if !errors.Is(err, ErrUnsupportedFilter) {
					failures = append(failures, fmt.Sprintf("%s : %v", ch.Resource, err))
				} else {
					d.Unmeasurable = true
				}
				continue
			}

			d.Count = res.Count
			d.Capped = res.Capped

			switch {
			case d.Class == change.ClassMigration:
				// Un renommage ou une couleur d'option porte déjà ClassMigration
				// AVANT toute mesure : le changement est inexprimable côté API,
				// indépendamment du nombre de lignes concernées. Seul le COÛT du
				// remède (retirer l'ancienne option) restait à mesurer, et c'est
				// fait ci-dessus. Recalculer la classe ici la ferait retomber à
				// ClassSafe/ClassDestructive/ClassSilentRewrite selon le compte, et
				// `--fail-on=migration` cesserait de se déclencher sur un simple
				// renommage.
			case d.Measure.Option != "":
				d.Class = change.ClassifyOptionRemoval(d.Measure.PropertyType, res.Count)
			case res.Count == 0:
				// Le compte reclasse un changement de type DANS UN SEUL SENS, et
				// l'asymétrie n'a rien d'évident :
				//
				// Zéro déclasse. Un couple destructeur sur une colonne vide ne coûte
				// rien — il n'y a aucune valeur à appauvrir. Sans ce déclassement la
				// ligne se contredit elle-même, « réécriture silencieuse » suivi de
				// « 0 ligne concernée », et --fail-on=silent-rewrite arrête une CI sur
				// une colonne sans aucune donnée. C'est la même règle que pour le
				// retrait d'option, où un compte nul rend ClassSafe.
				//
				// Un compte non nul, lui, ne touche à rien : la classe vient de la
				// table des couples de types, mesurée contre l'API. Le compte dit
				// l'AMPLEUR, la table dit la NATURE, et rien dans « 12 lignes non
				// vides » ne rend une réécriture silencieuse moins silencieuse.
				d.Class = change.ClassSafe
			}
		}

		// La classe d'en-tête a été calculée par Compute, AVANT la mesure : elle
		// est périmée dès qu'un détail change de classe. La recalculer ici est
		// obligatoire, sinon l'en-tête d'une ressource annonce une gravité qui ne
		// correspond plus à ses lignes — un « impact inconnu » sur une ressource
		// dont toutes les lignes sont désormais mesurées sûres, ou l'inverse.
		//
		// diff.WorstClass est la SEULE règle de gravité du dépôt : en écrire une
		// seconde ici la ferait diverger de celle de Compute au premier ajout de
		// classe.
		ch.Class = diff.WorstClass(ch.Details)
	}
	return failures
}
