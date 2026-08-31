package diff

import (
	"fmt"
	"sort"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

// Change est un changement présenté à l'utilisateur.
type Change struct {
	Class    Class
	Resource string   // "database.projects"
	Detail   string   // libellé court, ex. "(new)"
	Lines    []string // détail, une entrée par ligne affichée
}

// Plan est le résultat de la passe de diff.
type Plan struct {
	ToAdd     int
	ToChange  int
	ToDestroy int
	Changes   []Change

	// Blocked vaut true si au moins un changement arrête le plan par défaut.
	Blocked bool
}

// Compute compare la config désirée à l'état réel.
//
// Au MVP 0, remote est toujours vide : sans fichier de state, aucune ressource
// n'est mise en correspondance avec une database existante. Le paramètre est
// là parce que la signature ne changera pas au MVP 1, quand le state fournira
// les identités.
func Compute(cfg *config.Config, remote map[string]resources.RemoteState) (*Plan, error) {
	p := &Plan{}
	dbRes := resources.NewDatabaseResource(nil, nil)

	for _, db := range cfg.Databases {
		key := "database." + db.Key
		cs, err := dbRes.Diff(db, remote[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		switch cs.Kind {
		case resources.KindCreate:
			p.ToAdd++
			p.Changes = append(p.Changes, Change{
				Class:    ClassSafe,
				Resource: cs.Resource,
				Detail:   "(new)",
				Lines:    detailLines(cs.Details),
			})
		case resources.KindUpdate:
			p.ToChange++
			p.Changes = append(p.Changes, Change{
				Class:    ClassSafe,
				Resource: cs.Resource,
				Lines:    detailLines(cs.Details),
			})
		}
	}

	for _, c := range p.Changes {
		if c.Class.Blocking() {
			p.Blocked = true
			break
		}
	}
	return p, nil
}

func detailLines(details []resources.Detail) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		line := d.Op + " " + d.Target
		if d.Note != "" {
			line += " : " + d.Note
		}
		out = append(out, line)
	}
	sort.Strings(out)
	return out
}
