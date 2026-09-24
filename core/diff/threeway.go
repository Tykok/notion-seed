// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"sort"
	"strings"

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

	// Target est l'état EXACT qu'aura la ressource après écriture, sur la seule
	// surface gérée. C'est la source unique : Render en dérive ses lignes,
	// mapper en dérive son payload, state en dérive ce qu'il inscrit. Aucun
	// chemin de la configuration vers l'API ne la contourne, et c'est ce qui
	// rend impossible — plutôt que corrigée — une écriture non annoncée.
	//
	// Non nulle UNIQUEMENT là où apply sait écrire : une cible non nulle EST
	// l'autorisation d'écrire. Tant qu'apply ne fait que des créations, seule la
	// création en porte une. Le jour où update écrira, sa cible sera `actual`
	// auquel on applique les seuls changements déclarés — ce qui préserve les
	// propriétés hors config.
	Target *state.Database
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

	case desired == nil && actual == nil:
		// Plus dans la config, et le réel ne la porte pas — ou n'a pas été lu.
		// Les deux situations se distinguent par la raison du refresh, que seul
		// Compute connaît : c'est donc lui qui tranche entre « entrée de state
		// obsolète » et « non comparé ». Conclure ici à une destruction
		// proposerait de détruire ce qui n'existe déjà plus.
		return res

	case desired == nil:
		// Dans le state, TOUJOURS dans Notion, plus dans la config : l'identité
		// n'a plus d'ancre déclarée. Plus strict que la règle des propriétés hors
		// config, et c'est voulu — garder un id « géré mais non déclaré » le
		// rendrait invisible.
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
		target := *desired
		res.Target = &target
		res.Changeset.Details = createLines(&target)
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

// createLines détaille une création depuis la cible résolue : propriétés ET
// options, avec leur couleur et leur groupe.
//
// Les options y figurent parce que la création les ÉCRIT. Les omettre — ce que
// faisait la version précédente — laissait apply poser des couleurs et des
// groupes que le plan n'avait jamais montrés. C'est le même défaut, à la
// création, que celui du groupe substitué par le mapper.
func createLines(target *state.Database) []resources.Detail {
	var out []resources.Detail

	// Le nom, la description et l'icône partent AUSSI dans le payload de
	// création. Les omettre ici serait exactement le défaut que les options
	// avaient : écrit, jamais affiché. La même garde de non-vacuité qu'ailleurs
	// s'applique — ce que le YAML ne déclare pas n'est pas écrit, donc n'est pas
	// annoncé.
	for _, f := range []struct{ label, value string }{
		{"name", target.Name},
		{"description", target.Description},
		{"icon", target.Icon},
	} {
		if f.value == "" {
			continue
		}
		out = append(out, resources.Detail{
			Op:     "+",
			Target: fmt.Sprintf("%s %q", f.label, f.value),
			Class:  change.ClassSafe,
		})
	}

	for _, name := range sortedPropNames(target.Properties) {
		p := target.Properties[name]
		out = append(out, resources.Detail{
			Op:     "+",
			Target: fmt.Sprintf("property %q (%s)", name, p.Type),
			Class:  change.ClassSafe,
		})
		// L'ordre des options est celui du YAML : il est visible dans Notion,
		// le trier le rendrait faux.
		for _, o := range p.Options {
			out = append(out, resources.Detail{
				Op:     "+",
				Target: fmt.Sprintf("option %q (propriété %q)", o.Name, name),
				Note:   optionAttrNote(o),
				Class:  change.ClassSafe,
			})
		}
	}
	return out
}

// optionAttrNote rend les attributs déclarés d'une option, dans un ordre fixe.
// Vide si le YAML n'en déclare aucun : une note vide vaut mieux qu'une note qui
// annonce une valeur que notion-seed n'écrira pas.
func optionAttrNote(o state.Option) string {
	var parts []string
	if o.Color != "" {
		parts = append(parts, "color "+o.Color)
	}
	if o.Group != "" {
		parts = append(parts, "group "+o.Group)
	}
	return strings.Join(parts, ", ")
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
	if desired.Description != "" && desired.Description != actual.Description {
		out = append(out, resources.Detail{
			Op:     "~",
			Target: "description",
			Note:   fmt.Sprintf("%q → %q", actual.Description, desired.Description),
			Class:  change.ClassSafe,
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
			if old := renamedFrom(name, want, desired, applied, actual); old != "" {
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

	// Réclamation par POSITION dans have.Options, pas par nom : un renommage
	// libère son ancien nom, et une réclamation par nom croirait ce nom encore
	// occupé — une option déclarée sortirait alors du plan sans une ligne.
	// L'index est aussi robuste à un id vide, qu'un state écrit à la main peut
	// porter.
	claimed := make([]bool, len(have.Options))

	idxByID := make(map[string]int, len(have.Options))
	idxByName := make(map[string]int, len(have.Options))
	for i, o := range have.Options {
		if o.ID != "" {
			idxByID[o.ID] = i
		}
		if _, dup := idxByName[o.Name]; !dup {
			idxByName[o.Name] = i
		}
	}

	// key de config → position de l'option distante, en passant par l'id
	// Notion porté par le state. C'est la chaîne d'identité : applied ↔ actual
	// par id, desired ↔ applied par key.
	idxByKey := make(map[string]int)
	for _, o := range applied.Options {
		if o.Key == "" || o.ID == "" {
			continue
		}
		if i, ok := idxByID[o.ID]; ok {
			idxByKey[o.Key] = i
		}
	}

	var out []resources.Detail

	// Passe 1 : les options à key, qui ont une identité stable. Une key que le
	// réel ne connaît pas retombe sur l'appariement par nom.
	var keyless []state.Option
	for _, w := range want.Options {
		i, ok := idxByKey[w.Key]
		if w.Key == "" || !ok {
			keyless = append(keyless, w)
			continue
		}
		claimed[i] = true
		if current := have.Options[i].Name; current != w.Name {
			out = append(out, resources.Detail{
				Op:     "~",
				Target: fmt.Sprintf("option %q → %q (propriété %q)", current, w.Name, propName),
				Note:   "l'API répond 200 sans rien changer : créer, migrer les lignes, puis retirer",
				Class:  change.ClassMigration,
			})
		} else {
			// Le nom coïncide : pas de migration en cours sur cette ligne, donc la
			// place est libre pour comparer color et group, en classe sûre — comme
			// le format de number. Sur une migration, la ligne porte déjà son
			// propre changement ; superposer un second écart y sèmerait la
			// confusion sans rien ajouter, puisque l'option va de toute façon être
			// recréée.
			out = append(out, optionAttrLines(propName, w, have.Options[i])...)
		}
	}

	// Passe 2 : les options sans key, appariées par nom — mais seulement sur une
	// position que la passe 1 n'a pas déjà prise.
	for _, w := range keyless {
		if i, ok := idxByName[w.Name]; ok && !claimed[i] {
			claimed[i] = true
			out = append(out, optionAttrLines(propName, w, have.Options[i])...)
			continue
		}
		out = append(out, resources.Detail{
			Op:     "+",
			Target: fmt.Sprintf("option %q (propriété %q)", w.Name, propName),
			Class:  change.ClassSafe,
		})
	}

	// Passe 3 : ce qui existe dans Notion et que le YAML ne réclame pas SERA
	// détruit dès qu'on écrit cette propriété — l'API remplace la liste entière
	// au lieu de la fusionner. C'est l'asymétrie avec les propriétés.
	for i, o := range have.Options {
		if claimed[i] {
			continue
		}
		out = append(out, resources.Detail{
			Op:     "-",
			Target: fmt.Sprintf("option %q (propriété %q)", o.Name, propName),
			Note:   "absente du YAML : l'API remplace la liste entière des options",
			// -1 : le nombre de lignes portant cette option n'est pas mesuré à cet
			// endroit du plan. La mesure existera plus tard, branchée ici même.
			Class: change.ClassifyOptionRemoval(have.Type, -1),
		})
	}
	return out
}

// optionAttrLines compare color et group d'une option appariée dont le nom
// coïncide déjà — via key ou, à défaut, via nom. Classe sûre, comme le format
// de number : la donnée n'est pas perdue, elle est juste réaffichée
// autrement. Ne compare que ce que le YAML déclare : une couleur ou un group
// absent du YAML ne doit jamais produire de changement fantôme, exactement
// comme le nom de database et la description (gardés par un test de
// non-vacuité).
//
// Les garder dans le state sans jamais les lire ici serait la seule façon de
// faire diverger apply du plan le jour où apply existera : le plan promettrait
// une couleur qu'il n'écrirait jamais.
func optionAttrLines(propName string, want, have state.Option) []resources.Detail {
	var out []resources.Detail
	if want.Color != "" && want.Color != have.Color {
		out = append(out, resources.Detail{
			Op:     "~",
			Target: fmt.Sprintf("option %q (propriété %q)", want.Name, propName),
			Note:   fmt.Sprintf("color %s → %s", have.Color, want.Color),
			Class:  change.ClassSafe,
		})
	}
	if want.Group != "" && want.Group != have.Group {
		out = append(out, resources.Detail{
			Op:     "~",
			Target: fmt.Sprintf("option %q (propriété %q)", want.Name, propName),
			Note:   fmt.Sprintf("group %s → %s", have.Group, want.Group),
			Class:  change.ClassSafe,
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
	// Même garde de non-vacuité que pour le nom : un state qui n'a jamais
	// capturé la description ne doit pas faire passer sa valeur réelle pour une
	// dérive à chaque run.
	if applied.Description != "" && applied.Description != actual.Description {
		out = append(out, fmt.Sprintf(
			"~ description %q → %q hors de notion-seed", applied.Description, actual.Description))
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
		if was.Type == "number" && was.Format != "" && was.Format != is.Format {
			out = append(out, fmt.Sprintf(
				"~ propriété %q : format %s → %s hors de notion-seed", name, was.Format, is.Format))
		}

		// Une option sans id ne peut pas être suivie par id : ni retrait, ni
		// renommage, ni changement d'attribut ne peuvent être affirmés pour elle,
		// donc on ne rend aucune ligne de dérive la concernant.
		byID := map[string]state.Option{}
		for _, o := range is.Options {
			if o.ID == "" {
				continue
			}
			byID[o.ID] = o
		}
		wasIDs := map[string]bool{}
		for _, o := range was.Options {
			if o.ID == "" {
				continue
			}
			wasIDs[o.ID] = true
			current, ok := byID[o.ID]
			if !ok {
				out = append(out, fmt.Sprintf(
					"- option %q de la propriété %q retirée hors de notion-seed", o.Name, name))
				continue
			}
			if current.Name != o.Name {
				out = append(out, fmt.Sprintf(
					"~ option %q de la propriété %q renommée en %q hors de notion-seed",
					o.Name, name, current.Name))
			}
			// Même garde de non-vacuité qu'ailleurs : une couleur ou un group que le
			// state n'a jamais capturé ne doit pas faire passer sa valeur réelle
			// pour une dérive.
			if o.Color != "" && current.Color != o.Color {
				out = append(out, fmt.Sprintf(
					"~ option %q de la propriété %q : couleur %s → %s hors de notion-seed",
					o.Name, name, o.Color, current.Color))
			}
			if o.Group != "" && current.Group != o.Group {
				out = append(out, fmt.Sprintf(
					"~ option %q de la propriété %q : groupe %s → %s hors de notion-seed",
					o.Name, name, o.Group, current.Group))
			}
		}

		// Parcours inverse : ce qui a été ajouté à la main n'apparaît dans
		// aucune option de `applied`.
		for _, o := range is.Options {
			if o.ID == "" || wasIDs[o.ID] {
				continue
			}
			out = append(out, fmt.Sprintf(
				"+ option %q de la propriété %q ajoutée hors de notion-seed", o.Name, name))
		}
	}

	// Parcours inverse au niveau des propriétés : ce qui a été ajouté à la
	// main n'apparaît pas dans `applied`.
	for _, name := range sortedPropNames(actual.Properties) {
		if _, declared := applied.Properties[name]; declared {
			continue
		}
		out = append(out, fmt.Sprintf("+ propriété %q ajoutée hors de notion-seed", name))
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
// déclare plus mais qui existe toujours dans le réel : le signe d'un
// renommage. Heuristique d'AFFICHAGE, jamais de décision d'écriture. Si
// plusieurs candidates existent, elle ne désigne personne : une heuristique
// qui se trompe de colonne est pire que pas d'heuristique.
func renamedFrom(newName string, want state.Property, desired, applied, actual *state.Database) string {
	found := ""
	for _, old := range sortedPropNames(applied.Properties) {
		if old == newName {
			continue
		}
		if _, stillDeclared := desired.Properties[old]; stillDeclared {
			continue
		}
		if _, stillInActual := actual.Properties[old]; !stillInActual {
			continue
		}
		if applied.Properties[old].Type != want.Type {
			continue
		}
		if found != "" {
			return ""
		}
		found = old
	}
	return found
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
