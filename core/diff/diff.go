// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Change est un changement présenté à l'utilisateur.
type Change struct {
	Class    Class
	Resource string
	Detail   string

	// Details porte les changements élémentaires, non aplatis. La passe de
	// mesure les enrichit APRÈS Compute : aplatir en chaînes ici rendrait le
	// plan immesurable.
	Details []resources.Detail

	// Key est la key de configuration, sans le préfixe "database.". apply en a
	// besoin pour indexer le state ; Resource est fait pour l'affichage.
	Key string
	// Kind dit s'il s'agit d'une création, d'une modification ou d'une
	// destruction. apply n'écrit aujourd'hui que les créations, et doit pouvoir
	// le décider sans relire le texte des lignes.
	Kind resources.ChangeKind
	// Target est la cible résolue, non nulle seulement là où apply sait
	// écrire. Voir Result.Target.
	Target *state.Database
}

// Drift est un écart constaté entre le state et le réel.
type Drift struct {
	Resource string
	Lines    []string
}

// Unmanaged liste ce qui existe dans Notion sans être déclaré.
type Unmanaged struct {
	Resource string
	Lines    []string
}

// Refreshed est le résultat de la lecture d'une ressource du state.
//
// Missing distingue « lue et absente » de « pas lue » : sans cette distinction,
// une ressource gérée disparue passerait pour une ressource jamais appliquée,
// donc pour une création — exactement le contresens à éviter.
type Refreshed struct {
	Database state.Database
	Missing  bool
	Reason   string
}

// Plan est le résultat de la passe de diff.
type Plan struct {
	ToAdd     int
	ToChange  int
	ToDestroy int
	Changes   []Change
	Drifts    []Drift
	Unmanaged []Unmanaged

	// NotCompared nomme les ressources que le state ancre mais que le réel n'a
	// pas lues (--skip-preflight, qui ne fait aucun appel). CompareDatabase rend
	// alors un résultat vide « on ne sait rien, donc on ne dit rien » — mais un
	// résultat vide n'est pas une conformité constatée, et Render doit pouvoir
	// distinguer les deux.
	NotCompared []string

	// StaleState nomme les ressources que le state ancre, que la configuration
	// ne déclare plus, et qui n'existent plus dans Notion : la destruction a
	// déjà eu lieu hors de notion-seed. Retirer leur entrée n'écrit rien dans
	// Notion — c'est un nettoyage local, pas une destruction.
	StaleState []string

	Blocked bool
	// BlockedReasons nomme chaque blocage et son issue. « au moins un changement
	// refusé » ne dit pas à l'utilisateur quoi faire.
	BlockedReasons []string
}

// Compute compare la configuration désirée, le dernier état appliqué et le réel.
//
// applied peut être nil (aucun state) et actual peut être vide : on retombe
// alors exactement sur le comportement d'avant l'existence du state, où tout
// ressort en création.
func Compute(cfg *config.Config, applied *state.Snapshot, actual map[string]Refreshed) (*Plan, error) {
	p := &Plan{}
	appliedDBs := map[string]state.Database{}
	if applied != nil {
		appliedDBs = applied.Databases
	}

	allowDataLoss := setOf(cfg.Lifecycle.AllowDataLoss)
	preventDestroy := setOf(cfg.Lifecycle.PreventDestroy)

	seen := map[string]bool{}
	for _, db := range cfg.Databases {
		seen[db.Key] = true
		desired := state.FromConfig(db)

		var appliedPtr, actualPtr *state.Database
		if a, ok := appliedDBs[db.Key]; ok {
			appliedPtr = &a
		}
		if r, ok := actual[db.Key]; ok {
			if r.Missing {
				p.Blocked = true
				p.BlockedReasons = append(p.BlockedReasons, fmt.Sprintf(
					"database.%s est dans le state mais %s dans Notion.\n"+
						"  → restaurez-la dans Notion, ou retirez son entrée de %s pour "+
						"assumer une recréation (la nouvelle database repartira vide)",
					db.Key, r.Reason, state.FileName))
				continue
			}
			d := r.Database
			actualPtr = &d
		} else if appliedPtr != nil {
			// Le state ancre cette ressource, mais actual n'a pas d'entrée : c'est
			// --skip-preflight, qui ne lit jamais le réel (voir le commentaire de
			// CompareDatabase sur ce même cas). Le plan ne doit pas laisser croire
			// qu'il a vérifié une conformité qu'il n'a en fait jamais lue.
			p.NotCompared = append(p.NotCompared, "database."+db.Key)
		}

		p.absorb(db.Key, CompareDatabase(db.Key, &desired, appliedPtr, actualPtr),
			allowDataLoss, preventDestroy)
	}

	// Les ressources du state que la configuration ne déclare plus.
	orphans := make([]string, 0)
	for key := range appliedDBs {
		if !seen[key] {
			orphans = append(orphans, key)
		}
	}
	sort.Strings(orphans)
	for _, key := range orphans {
		a := appliedDBs[key]
		resource := "database." + key

		r, read := actual[key]
		switch {
		case !read:
			// --skip-preflight : rien n'a été lu, donc on ne peut ni planifier la
			// destruction ni conclure qu'elle a déjà eu lieu. Annoncer une
			// destruction ici, c'est ce que faisait la version précédente : elle
			// concluait sans jamais regarder le réel.
			p.NotCompared = append(p.NotCompared, resource)
			continue

		case r.Missing:
			// Introuvable ou archivée : dans Notion, détruire une database c'est
			// l'archiver, donc les deux cas valent destruction déjà effective.
			if preventDestroy[resource] {
				p.block(fmt.Sprintf(
					"%s : protégée par lifecycle.prevent_destroy, mais %s dans Notion.\n"+
						"  → la ressource la mieux protégée du fichier a disparu hors de "+
						"notion-seed. Restaurez-la dans Notion, ou retirez %s de "+
						"prevent_destroy pour acter sa disparition",
					resource, r.Reason, resource))
				continue
			}
			p.StaleState = append(p.StaleState, resource)
			continue
		}

		d := r.Database
		p.absorb(key, CompareDatabase(key, nil, &a, &d), allowDataLoss, preventDestroy)
	}
	return p, nil
}

// absorb verse le résultat d'une ressource dans le plan, en appliquant
// lifecycle.
func (p *Plan) absorb(key string, res Result, allowDataLoss, preventDestroy map[string]bool) {
	resource := "database." + key

	if len(res.Drift) > 0 {
		p.Drifts = append(p.Drifts, Drift{Resource: resource, Lines: res.Drift})
	}
	if len(res.Unmanaged) > 0 {
		p.Unmanaged = append(p.Unmanaged, Unmanaged{Resource: resource, Lines: res.Unmanaged})
	}
	if res.Changeset.Kind == resources.KindNone {
		return
	}

	switch res.Changeset.Kind {
	case resources.KindCreate:
		p.ToAdd++
	case resources.KindUpdate:
		p.ToChange++
	case resources.KindDestroy:
		p.ToDestroy++
	}

	c := Change{
		Resource: resource,
		Key:      key,
		Kind:     res.Changeset.Kind,
		Target:   res.Target,
		Class:    WorstClass(res.Changeset.Details),
	}
	if res.Changeset.Kind == resources.KindCreate {
		c.Detail = "(new)"
	}
	// Les détails passent tels quels : c'est la passe de mesure, après Compute,
	// qui les enrichira. Les aplatir en chaînes ici — ce que faisait la version
	// précédente — rendait le plan immesurable, et le rendu n'a de toute façon
	// besoin que de Details.
	c.Details = res.Changeset.Details
	p.Changes = append(p.Changes, c)

	// lifecycle.prevent_destroy reste le seul blocage fondé sur autre chose
	// qu'une mesure : notion-seed ne refuse plus un changement sur la foi de sa
	// classe — il MESURE son coût et le dit (voir Class et Details
	// ci-dessus). allow_data_loss n'a donc plus rien à débloquer ici.
	if res.Changeset.Kind == resources.KindDestroy && preventDestroy[resource] {
		p.block(fmt.Sprintf(
			"%s : destruction interdite par lifecycle.prevent_destroy.\n"+
				"  → retirez %s de prevent_destroy si la destruction est voulue",
			resource, resource))
	}
}

func (p *Plan) block(reason string) {
	p.Blocked = true
	for _, r := range p.BlockedReasons {
		if r == reason {
			return
		}
	}
	p.BlockedReasons = append(p.BlockedReasons, reason)
}

func setOf(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, i := range items {
		out[i] = true
	}
	return out
}
