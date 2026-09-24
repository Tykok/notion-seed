// SPDX-License-Identifier: GPL-3.0-or-later

package measure

import (
	"context"
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
// dataSourceIDs associe la key de configuration à l'id du data source, lu dans
// le state. Une ressource absente de cette table n'est pas mesurable : elle n'a
// jamais été importée, donc il n'y a rien à interroger.
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
				failures = append(failures, fmt.Sprintf("%s : %v", ch.Resource, err))
				continue
			}

			d.Count = res.Count
			d.Capped = res.Capped

			// Seul le retrait d'option se reclasse avec le compte. Un changement
			// de type tient sa classe de la table mesurée : le compte dit
			// l'ampleur, pas la nature.
			if d.Measure.Option != "" {
				d.Class = change.ClassifyOptionRemoval(d.Measure.PropertyType, res.Count)
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
