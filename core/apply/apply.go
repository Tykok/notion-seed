// SPDX-License-Identifier: GPL-3.0-or-later

// Package apply exécute le plan : il écrit dans Notion ce que le plan a
// affiché, et rien d'autre.
//
// Périmètre de cette version : les créations, les modifications, et le nettoyage
// des entrées de state dont la ressource a déjà disparu. Les destructions sont
// NOMMÉES comme non appliquées plutôt que tentées à moitié.
package apply

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// Updater est ce dont apply a besoin pour écrire une database existante.
//
// DatabaseExists en fait partie, et ce n'est pas une commodité : le 404 du PATCH
// du data source accuse le partage avec l'intégration, alors que la cause peut
// être une page ancêtre à la corbeille. Sonder la database — et elle seule, sans
// son data source qui répondrait le même 404 — est le seul moyen de trancher, et
// il ne coûte un appel que sur un chemin déjà en échec.
type Updater interface {
	Update(ctx context.Context, id, dsID string, dbBody, dsBody []byte) (resources.UpdatedDatabase, error)
	DatabaseExists(ctx context.Context, id string) (bool, error)
}

// Options porte ce qui vient de la ligne de commande et de la configuration.
type Options struct {
	Dir          string
	ParentPageID string
	Creator      Creator
	Updater      Updater
}

// Report est le compte rendu d'une exécution. Chaque champ est une liste de
// lignes prêtes à afficher, dans l'ordre où elles se sont produites.
type Report struct {
	Created    []string
	Updated    []string
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

// Run écrit les créations et les modifications du plan, une ressource à la fois.
//
// Le state est sauvegardé APRÈS CHAQUE écriture réussie, pas une fois à la fin :
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

	// La garde vit ICI, dans le paquet qui écrit, et pas seulement dans la
	// commande. Un plan peut être bloqué par une AUTRE ressource que celles
	// qu'on s'apprête à créer : sans cette ligne, un second appelant écrirait
	// les créations d'un plan refusé, et aucun test ne le verrait.
	if p.Blocked {
		return rep, fmt.Errorf(
			"plan bloqué, rien n'a été appliqué\n" +
				"  → levez chaque blocage listé par `notion-seed plan` avant de relancer")
	}

	if snap.Databases == nil {
		snap.Databases = map[string]state.Database{}
	}

	for _, c := range p.Changes {
		// Withheld EST l'interdiction d'écrire : voir diff.Result.Withheld.
		if c.Withheld != "" {
			rep.Skipped = append(rep.Skipped, c.Resource)
			continue
		}
		switch {
		case c.Kind == resources.KindCreate && c.Target != nil:
			if err := createOne(ctx, c, &rep, snap, opts); err != nil {
				return rep, err
			}
		case c.Kind == resources.KindUpdate && c.Target != nil:
			if err := updateOne(ctx, c, &rep, snap, opts); err != nil {
				return rep, err
			}
		default:
			rep.Skipped = append(rep.Skipped, c.Resource)
		}
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

// createOne crée une database et inscrit le résultat relu dans le state.
func createOne(ctx context.Context, c diff.Change, rep *Report, snap *state.Snapshot, opts Options) error {
	body, err := mapper.DatabaseCreatePayload(c.Key, *c.Target, opts.ParentPageID)
	if err != nil {
		return err
	}

	created, err := opts.Creator.Create(ctx, body)
	if err != nil {
		return creationError(c, *rep, err, opts.ParentPageID)
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
			return serr
		}
		return fmt.Errorf(
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
		return err
	}
	rep.Created = append(rep.Created, fmt.Sprintf(
		"%s créée — id %s (state mis à jour)", c.Resource, created.ID))
	rep.Mismatches = append(rep.Mismatches, mismatchLines(c, adopted)...)
	return nil
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
		// La Note des détails est écrite pour le contexte du PLAN — « absente du
		// YAML », par exemple — et se lit à l'envers ici : ce qui manquait au YAML
		// est ce que l'API a écrit en trop. On ne la reprend donc pas, et on dit
		// le sens réel à partir de l'opération.
		out = append(out, fmt.Sprintf("%s — %s : %s",
			c.Resource, mismatchVerb(d.Op), d.Target))
	}
	return out
}

// mismatchVerb traduit l'opération du plan en ce qui s'est réellement passé
// pendant l'écriture. `-` est le cas qui se lisait à l'envers : dans un plan il
// veut dire « à retirer », ici il veut dire « l'API l'a écrit alors que la
// cible ne le portait pas ».
func mismatchVerb(op string) string {
	switch op {
	case "-":
		return "l'API a écrit en trop"
	case "~":
		return "l'API a écrit une autre valeur que celle annoncée"
	default:
		return "l'API n'a pas écrit"
	}
}

// creationError rend l'échec d'une création en nommant ce qui est acquis et ce
// qui ne l'est pas. Un message qui ne dit pas où s'est arrêtée la série laisse
// l'utilisateur deviner l'état de son workspace.
func creationError(c diff.Change, rep Report, err error, parentPageID string) error {
	acquired := acquiredBefore(rep)

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// On ne sait pas si la mutation a été appliquée côté serveur. On
		// n'enchaîne surtout pas : le workspace est dans un état indéterminé, et
		// la création suivante travaillerait à l'aveugle.
		return fmt.Errorf(
			"création de %s : issue inconnue: %w\n"+
				"  → ouvrez la page parente %s dans Notion. Si la database %q existe, "+
				"adoptez-la avec `notion-seed import %s <url>` ; sinon relancez apply. "+
				"%s",
			c.Resource, err, parentPageID, c.Target.Name, c.Resource, acquired)
	}
	return fmt.Errorf(
		"création de %s impossible: %w\n"+
			"  → corrigez la cause ci-dessus puis relancez apply ; rien n'est annulé, "+
			"%s",
		c.Resource, err, acquired)
}

// acquiredBefore nomme ce que l'exécution a déjà écrit et inscrit dans le
// state avant la ressource en échec. Un message qui ne dit pas où s'est arrêtée
// la série laisse l'utilisateur deviner l'état de son workspace.
func acquiredBefore(rep Report) string {
	var parts []string
	if names := resourceNames(rep.Created); len(names) > 0 {
		parts = append(parts, "déjà créées et inscrites dans le state : "+strings.Join(names, ", "))
	}
	if names := resourceNames(rep.Updated); len(names) > 0 {
		parts = append(parts, "déjà modifiées et inscrites dans le state : "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		return "aucune écriture n'avait abouti avant celle-ci"
	}
	return strings.Join(parts, " ; ")
}

// resourceNames extrait le nom de ressource en tête de chaque ligne du rapport.
func resourceNames(lines []string) []string {
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		names = append(names, strings.SplitN(line, " ", 2)[0])
	}
	return names
}

// writeSet dérive du PLAN ce qui part vers l'API : les champs de database et les
// propriétés qui portent au moins une ligne.
//
// C'est l'invariant du produit rendu littéral — ce qui est écrit est exactement
// ce qui est affiché. Une propriété déclarée mais identique au réel ne porte
// aucune ligne, donc ne part pas ; une propriété hors config n'en porte jamais.
func writeSet(details []resources.Detail) (fields, props []string) {
	seenF := map[string]bool{}
	seenP := map[string]bool{}
	for _, d := range details {
		if d.Field != "" {
			if !seenF[d.Field] {
				seenF[d.Field] = true
				fields = append(fields, d.Field)
			}
			continue
		}
		if d.Property != "" && !seenP[d.Property] {
			seenP[d.Property] = true
			props = append(props, d.Property)
		}
	}
	sort.Strings(fields)
	sort.Strings(props)
	return fields, props
}

// updateOne écrit une database existante et inscrit le résultat relu dans le
// state.
func updateOne(ctx context.Context, c diff.Change, rep *Report, snap *state.Snapshot, opts Options) error {
	if opts.Updater == nil {
		return fmt.Errorf(
			"%s : une modification est à écrire mais aucun Updater n'est branché\n"+
				"  → c'est un défaut interne de notion-seed : signalez-le avec la "+
				"sortie de `notion-seed plan`", c.Resource)
	}
	// La cible d'un update vient TOUJOURS d'une relecture fraîche : un id vide
	// ne peut venir que d'un défaut en amont. PATCHer "/v1/data_sources/" ne
	// désignerait rien — on refuse AVANT tout appel.
	if c.Target.ID == "" || c.Target.DataSourceID == "" {
		return fmt.Errorf(
			"%s : la cible de la modification ne porte pas d'id de database ou de "+
				"data source, rien n'a été écrit\n"+
				"  → c'est un défaut interne de notion-seed : signalez-le avec la "+
				"sortie de `notion-seed plan`", c.Resource)
	}

	fields, props := writeSet(c.Details)

	var dbBody, dsBody []byte
	if len(fields) > 0 {
		b, err := mapper.DatabaseUpdatePayload(c.Key, *c.Target, fields)
		if err != nil {
			return err
		}
		dbBody = b
	}
	if len(props) > 0 {
		b, err := mapper.DataSourceUpdatePayload(c.Key, *c.Target, props)
		if err != nil {
			return err
		}
		dsBody = b
	}
	if dbBody == nil && dsBody == nil {
		// Un changement sans aucune ligne écrivable n'existe pas : le plan ne
		// produit un KindUpdate que s'il porte des détails. Sauter en silence
		// masquerait un défaut de writeSet.
		return fmt.Errorf(
			"%s : le plan annonce une modification mais aucune écriture n'en découle\n"+
				"  → c'est un défaut interne de notion-seed : signalez-le avec la "+
				"sortie de `notion-seed plan`", c.Resource)
	}

	upd, err := opts.Updater.Update(ctx, c.Target.ID, c.Target.DataSourceID, dbBody, dsBody)
	if err != nil {
		return updateError(ctx, c, *rep, err, upd, fields, len(dbBody) > 0, opts)
	}
	if upd.ReadErr != nil {
		return fmt.Errorf(
			"%s a été modifiée mais son état n'a pas pu être relu: %w\n"+
				"  → son entrée de %s porte encore l'état d'avant l'écriture. Relancez "+
				"`notion-seed plan` pour voir le réel",
			c.Resource, upd.ReadErr, state.FileName)
	}

	adopted, _ := state.JoinOptionKeys(state.FromRemote(upd.Remote), *c.Target)
	snap.Databases[c.Key] = adopted
	if err := state.Save(opts.Dir, snap); err != nil {
		return err
	}
	rep.Updated = append(rep.Updated, fmt.Sprintf(
		"%s modifiée — %d champ(s), %d propriété(s) (state mis à jour)",
		c.Resource, len(fields), len(props)))
	rep.Mismatches = append(rep.Mismatches, mismatchLines(c, adopted)...)
	return nil
}

// fieldLabels nomme, en français, les champs de database écrits.
func fieldLabels(fields []string) string {
	labels := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case "name":
			labels = append(labels, "le nom")
		case "description":
			labels = append(labels, "la description")
		case "icon":
			labels = append(labels, "l'icône")
		default:
			labels = append(labels, f)
		}
	}
	if len(labels) <= 1 {
		return strings.Join(labels, "")
	}
	return strings.Join(labels[:len(labels)-1], ", ") + " et " + labels[len(labels)-1]
}

// updateError rend l'échec d'une mise à jour en nommant ce qui est passé sur
// cette ressource, et ce qui était acquis avant elle.
//
// Quel PATCH a échoué se déduit : si un corps partait vers la database et
// qu'elle n'est pas écrite, c'est le premier ; sinon, c'est le data source.
func updateError(
	ctx context.Context, c diff.Change, rep Report, err error,
	upd resources.UpdatedDatabase, fields []string, dbSent bool, opts Options,
) error {
	dsFailed := upd.DatabaseWritten || !dbSent
	acquired := acquiredBefore(rep)
	here := "rien n'a été écrit sur cette ressource"
	if upd.DatabaseWritten {
		here = "déjà écrit sur cette ressource : " + fieldLabels(fields) +
			" ; aucune donnée de ligne n'a été touchée"
	}

	var unknown *transport.OutcomeUnknownError
	if errors.As(err, &unknown) {
		// Arrêt net, sans enchaîner : la ressource suivante travaillerait sur un
		// workspace dans un état indéterminé. Contrairement à la création, aucune
		// identité n'est en jeu — la relecture du plan suffit.
		pending := "l'écriture a peut-être abouti côté serveur"
		if upd.DatabaseWritten {
			pending = here + ", mais l'écriture du schéma a peut-être abouti côté serveur"
		}
		return fmt.Errorf(
			"modification de %s : issue inconnue: %w\n"+
				"  → lancez `notion-seed plan` pour voir ce que Notion porte réellement ; "+
				"il suffit, rien n'est à ré-adopter. %s. %s",
			c.Resource, err, pending, acquired)
	}

	var apiErr *transport.APIError
	if !errors.As(err, &apiErr) {
		return genericUpdateError(c, err, here, acquired, upd.DatabaseWritten)
	}

	// Mesuré le 2026-09-24 : sur une page ancêtre à la corbeille, le PATCH
	// database rend un 400 qui nomme la cause.
	if !dsFailed && apiErr.Status == 400 && strings.Contains(apiErr.Message, "archived ancestor") {
		return fmt.Errorf(
			"modification de %s impossible : une page ancêtre de la database est à "+
				"la corbeille\n"+
				"  → restaurez la page parente dans Notion, puis relancez apply. %s ; %s",
			c.Resource, here, acquired)
	}

	// Le 404 du PATCH data source accuse le partage avec l'intégration, et il
	// peut mentir : mesuré le 2026-09-24, une page ancêtre à la corbeille produit
	// exactement ce 404, alors que GET /v1/databases répond 200. Son texte n'est
	// donc JAMAIS relayé tel quel ; la sonde de la database tranche.
	if dsFailed && apiErr.Status == 404 {
		exists, perr := opts.Updater.DatabaseExists(ctx, c.Target.ID)
		switch {
		case perr != nil:
			return fmt.Errorf(
				"modification de %s impossible : le data source répond %d %s, et la "+
					"database n'a pas pu être relue pour en trouver la cause: %w\n"+
					"  → corrigez la cause ci-dessus puis relancez apply ; rien n'est "+
					"annulé. %s ; %s",
				c.Resource, apiErr.Status, apiErr.NotionCode, perr, here, acquired)
		case exists:
			return fmt.Errorf(
				"modification de %s impossible : ancêtre archivé — la database se lit "+
					"encore, mais une page ancêtre est à la corbeille\n"+
					"  → restaurez la page parente dans Notion, puis relancez apply. %s ; %s",
				c.Resource, here, acquired)
		default:
			return fmt.Errorf(
				"modification de %s impossible : la database a réellement disparu, ou "+
					"n'est plus partagée avec l'intégration\n"+
					"  → vérifiez dans Notion qu'elle existe et qu'elle est partagée avec "+
					"l'intégration, puis relancez `notion-seed plan`. %s ; %s",
				c.Resource, here, acquired)
		}
	}

	return genericUpdateError(c, err, here, acquired, upd.DatabaseWritten)
}

// genericUpdateError est le message d'un échec sans diagnostic propre.
func genericUpdateError(c diff.Change, err error, here, acquired string, dbWritten bool) error {
	next := "corrigez la cause ci-dessus puis relancez apply"
	if dbWritten {
		next += " ; `notion-seed plan` montre ce qui reste à écrire"
	}
	return fmt.Errorf(
		"modification de %s impossible: %w\n"+
			"  → %s ; rien n'est annulé. %s ; %s",
		c.Resource, err, next, here, acquired)
}
