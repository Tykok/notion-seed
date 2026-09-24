// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Change est un changement présenté à l'utilisateur.
type Change struct {
	Class    Class
	Resource string
	Detail   string
	Lines    []string
	// LineClasses porte la classe de chaque entrée de Lines, dans le même
	// ordre : une database peut recevoir un ajout sûr et un retrait d'option en
	// réécriture silencieuse.
	LineClasses []Class

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
		p.absorb(key, CompareDatabase(key, nil, &a, nil), allowDataLoss, preventDestroy)
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
		Class:    worstClass(res.Changeset.Details),
	}
	if res.Changeset.Kind == resources.KindCreate {
		c.Detail = "(new)"
	}
	for _, d := range res.Changeset.Details {
		line := d.Op + " " + d.Target
		if d.Note != "" {
			// PAS de %q ici : Note porte déjà ses propres guillemets là où il en
			// faut (un renommage rend `"Ancien" → "Nouveau"`). Un %q supplémentaire
			// ré-échappe ces guillemets et l'ensemble de la note, jusqu'à rendre
			// illisible la seule ligne censée éviter qu'on croie avoir renommé une
			// propriété alors qu'elle reste hors config.
			line += " — " + d.Note
		}
		c.Lines = append(c.Lines, line)
		c.LineClasses = append(c.LineClasses, d.Class)
	}
	p.Changes = append(p.Changes, c)

	// lifecycle. prevent_destroy est absolu ; allow_data_loss ne couvre que le
	// destructif ; la réécriture silencieuse n'est couverte par rien — consentir
	// à perdre une donnée n'est pas consentir à ce qu'elle soit remplacée par
	// une autre, plausible et fausse.
	for _, d := range res.Changeset.Details {
		switch {
		case d.Class == change.ClassSilentRewrite:
			p.block(fmt.Sprintf(
				"%s : réécriture silencieuse (%s).\n"+
					"  → retirer une option de status réassigne les lignes concernées à "+
					"l'option par défaut, sans erreur ni avertissement. allow_data_loss ne "+
					"débloque pas ce cas. Migrez les lignes dans Notion, puis retirez "+
					"l'option du YAML", resource, d.Target))
		case res.Changeset.Kind == resources.KindDestroy && preventDestroy[resource]:
			p.block(fmt.Sprintf(
				"%s : destruction interdite par lifecycle.prevent_destroy.\n"+
					"  → retirez %s de prevent_destroy si la destruction est voulue",
				resource, resource))
		case d.Class.CoveredByAllowDataLoss() && !allowDataLoss[resource]:
			p.block(fmt.Sprintf(
				"%s : changement destructif (%s).\n"+
					"  → ajoutez %s à lifecycle.allow_data_loss si la perte est acceptée",
				resource, d.Target, resource))
		}
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
