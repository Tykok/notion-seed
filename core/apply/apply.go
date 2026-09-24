// SPDX-License-Identifier: GPL-3.0-or-later

// Package apply exécute le plan : il écrit dans Notion ce que le plan a
// affiché, et rien d'autre.
//
// Périmètre de cette version : les créations, et le nettoyage des entrées de
// state dont la ressource a déjà disparu. Tout le reste est NOMMÉ comme non
// appliqué plutôt que tenté à moitié — une ressource est entièrement écrite ou
// pas touchée du tout.
package apply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tykok/notion-seed/core/diff"
	"github.com/tykok/notion-seed/core/providers/notion/mapper"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/providers/notion/transport"
	"github.com/tykok/notion-seed/core/state"
)

// Creator est ce dont apply a besoin pour écrire. L'interface est déclarée ici,
// côté consommateur, pour que les tests n'aient pas à monter une pile de
// transport complète.
type Creator interface {
	Create(ctx context.Context, body []byte) (resources.CreatedDatabase, error)
}

// Options porte ce qui vient de la ligne de commande et de la configuration.
type Options struct {
	Dir          string
	ParentPageID string
	Creator      Creator
}

// Report est le compte rendu d'une exécution. Chaque champ est une liste de
// lignes prêtes à afficher, dans l'ordre où elles se sont produites.
type Report struct {
	Created    []string
	Cleaned    []string
	Skipped    []string
	Mismatches []string
}

// Converged dit si le plan a été appliqué en entier, et si l'API a écrit ce qui
// était annoncé. C'est ce qui décide du code de sortie : un apply qui laisse du
// travail derrière lui doit être bruyant en CI.
func (r Report) Converged() bool {
	return len(r.Skipped) == 0 && len(r.Mismatches) == 0
}

// Run écrit les créations du plan, une ressource à la fois.
//
// Le state est sauvegardé APRÈS CHAQUE création réussie, pas une fois à la fin :
// un arrêt à n'importe quel instant laisse alors un state exactement vrai. Ce
// qui est créé est ancré, ce qui ne l'est pas ressortira en création au prochain
// plan. Sauver une seule fois à la fin produirait, à la moindre interruption,
// des databases réellement créées dont le state ignore l'existence — donc
// recréées en double au prochain apply.
//
// Aucun rollback : archiver ce qu'on vient de créer serait une destruction que
// personne n'a demandée.
func Run(ctx context.Context, p *diff.Plan, snap *state.Snapshot, opts Options) (Report, error) {
	var rep Report
	if snap.Databases == nil {
		snap.Databases = map[string]state.Database{}
	}

	for _, c := range p.Changes {
		// Une cible nulle EST l'interdiction d'écrire : voir diff.Result.Target.
		if c.Kind != resources.KindCreate || c.Target == nil {
			rep.Skipped = append(rep.Skipped, c.Resource)
			continue
		}

		body, err := mapper.DatabaseCreatePayload(c.Key, *c.Target, opts.ParentPageID)
		if err != nil {
			return rep, err
		}

		created, err := opts.Creator.Create(ctx, body)
		if err != nil {
			return rep, creationError(c, rep, err)
		}

		if created.ReadErr != nil {
			// La database EXISTE et on a son id. On inscrit une entrée partielle
			// plutôt que de perdre l'identité : une identité perdue coûte un
			// doublon, une entrée partielle ne coûte qu'une dérive non détectable
			// sur les options — driftLines ignore les options sans id, donc aucune
			// FAUSSE dérive n'en sortira.
			partial := *c.Target
			partial.ID = created.ID
			partial.DataSourceID = created.DataSourceID
			snap.Databases[c.Key] = partial
			if serr := state.Save(opts.Dir, snap); serr != nil {
				return rep, serr
			}
			return rep, fmt.Errorf(
				"%s a été créée (id %s) mais son état n'a pas pu être relu: %w\n"+
					"  → son identité est inscrite dans %s, donc elle ne sera pas recréée. "+
					"Pour resynchroniser son instantané, retirez son entrée de %s puis "+
					"lancez `notion-seed import %s <url>`",
				c.Resource, created.ID, created.ReadErr,
				state.FileName, state.FileName, c.Resource)
		}

		adopted, _ := state.JoinOptionKeys(state.FromRemote(created.Remote), *c.Target)
		snap.Databases[c.Key] = adopted
		if err := state.Save(opts.Dir, snap); err != nil {
			return rep, err
		}
		rep.Created = append(rep.Created, fmt.Sprintf(
			"%s créée — id %s (state mis à jour)", c.Resource, created.ID))
		rep.Mismatches = append(rep.Mismatches, mismatchLines(c, adopted)...)
	}

	for _, resource := range p.StaleState {
		key := strings.TrimPrefix(resource, "database.")
		delete(snap.Databases, key)
		if err := state.Save(opts.Dir, snap); err != nil {
			return rep, err
		}
		rep.Cleaned = append(rep.Cleaned, fmt.Sprintf(
			"%s — entrée retirée du state, rien n'a été écrit dans Notion", resource))
	}

	return rep, nil
}

// mismatchLines confronte le réel relu à ce que le plan avait annoncé, sur
// notre propre écriture.
//
// Le comparateur du plan est réutilisé tel quel : `adopted` joue à la fois la
// voie « dernier état appliqué » et la voie « réel », donc tout détail qui
// subsiste est un écart entre la cible et ce que l'API a réellement écrit. Un
// second comparateur écrit pour l'occasion serait exactement le genre de chemin
// parallèle que cette PR existe pour supprimer.
func mismatchLines(c diff.Change, adopted state.Database) []string {
	gap := diff.CompareDatabase(c.Key, c.Target, &adopted, &adopted)
	out := make([]string, 0, len(gap.Changeset.Details))
	for _, d := range gap.Changeset.Details {
		line := fmt.Sprintf("%s — l'API n'a pas écrit %s %s", c.Resource, d.Op, d.Target)
		if d.Note != "" {
			line += " — " + d.Note
		}
		out = append(out, line)
	}
	return out
}

// creationError rend l'échec d'une création en nommant ce qui est acquis et ce
// qui ne l'est pas. Un message qui ne dit pas où s'est arrêtée la série laisse
// l'utilisateur deviner l'état de son workspace.
func creationError(c diff.Change, rep Report, err error) error {
	acquired := "aucune création n'avait abouti avant celle-ci"
	if len(rep.Created) > 0 {
		names := make([]string, 0, len(rep.Created))
		for _, line := range rep.Created {
			names = append(names, strings.SplitN(line, " ", 2)[0])
		}
		acquired = "déjà créées et inscrites dans le state : " + strings.Join(names, ", ")
	}

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// On ne sait pas si la mutation a été appliquée côté serveur. On
		// n'enchaîne surtout pas : le workspace est dans un état indéterminé, et
		// la création suivante travaillerait à l'aveugle.
		return fmt.Errorf(
			"création de %s : issue inconnue: %w\n"+
				"  → ouvrez la page parente dans Notion. Si la database %q existe, "+
				"adoptez-la avec `notion-seed import %s <url>` ; sinon relancez apply. "+
				"%s",
			c.Resource, err, c.Target.Name, c.Resource, acquired)
	}
	return fmt.Errorf(
		"création de %s impossible: %w\n"+
			"  → corrigez la cause ci-dessus puis relancez apply ; rien n'est annulé, "+
			"%s",
		c.Resource, err, acquired)
}
