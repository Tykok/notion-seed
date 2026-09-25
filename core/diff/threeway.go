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
	// Non nulle sur les chemins où il y a un état après écriture : la création et
	// la mise à jour. Pour une mise à jour, c'est `actual` auquel on applique les
	// SEULS changements déclarés — ce qui préserve les propriétés hors config, et
	// ce qui fait porter à la cible les ids d'options distants.
	//
	// L'AUTORISATION d'écrire, elle, est portée par Withheld : une destruction
	// n'a pas d'état après, donc elle ne pourrait pas être autorisée par une
	// cible.
	Target *state.Database

	// Withheld dit POURQUOI cette ressource ne sera pas écrite, ou "" si elle
	// peut l'être. C'est désormais l'autorisation d'écrire : `Target` dit ce
	// qu'on écrit, `Withheld` dit si on a le droit.
	//
	// Une raison plutôt qu'un booléen : une ressource sautée sans motif renvoie
	// l'utilisateur deviner, et notion-seed ne laisse jamais deviner.
	Withheld string
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
		d := resources.NewDetail("-", "database."+key, change.ClassDestructive)
		d.Note = "présente dans le state, absente de la configuration"
		res.Changeset.Details = []resources.Detail{d}
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
		if res.Withheld = withheldReason(res.Changeset.Details); res.Withheld == "" {
			t := updateTarget(desired, applied, actual)
			res.Target = &t
		}
	}
	return res
}

// withheldReason dit pourquoi une ressource ne peut pas être écrite, ou "" si
// elle peut l'être.
//
// Une seule cause aujourd'hui, mesurée deux fois : une ligne `migration
// requise` est inexprimable dans l'API. Un renommage d'option rend 200 sans
// rien changer ; une couleur d'option rend 400 et fait échouer tout le PATCH.
// Dans le premier cas, écrire inscrirait dans le state un nom que Notion ne
// porte pas, et chaque run suivant afficherait une dérive fantôme.
//
// Ce n'est PAS un refus de sûreté : notion-seed ne refuse rien sur la foi d'une
// classe, il mesure et il dit. C'est une limite de l'API, nommée comme telle,
// avec sa procédure.
func withheldReason(ds []resources.Detail) string {
	for _, d := range ds {
		if d.Class == change.ClassMigration {
			return "une option doit être migrée à la main : l'API ne sait ni renommer " +
				"une option ni changer sa couleur\n" +
				"  → créez la nouvelle option dans Notion, déplacez-y les lignes " +
				"comptées ci-dessus, retirez l'ancienne, puis relancez"
		}
	}
	return ""
}

// updateTarget résout l'état exact qu'aura la database après écriture : c'est
// `actual` auquel on applique les SEULS changements déclarés.
//
// Partir de `desired` — ce que faisait la version qui n'écrivait pas — ferait
// disparaître tout ce que le YAML ne déclare pas. Partir d'`actual` est ce qui
// donne son sens à « propriété non déclarée = non touchée », et c'est aussi ce
// qui fait porter à la cible les ids d'options distants, sans lesquels le
// premier PATCH détruirait chaque option qu'il croit modifier.
func updateTarget(desired, applied, actual *state.Database) state.Database {
	target := state.Database{
		ID:           actual.ID,
		DataSourceID: actual.DataSourceID,
		Name:         actual.Name,
		Description:  actual.Description,
		Icon:         actual.Icon,
		Properties:   make(map[string]state.Property, len(desired.Properties)),
	}
	// Même garde de non-vacuité que dans planLines : ce que le YAML ne déclare
	// pas n'est pas écrit, donc n'entre pas dans la cible.
	if desired.Name != "" {
		target.Name = desired.Name
	}
	if desired.Description != "" {
		target.Description = desired.Description
	}
	if desired.Icon != "" {
		target.Icon = desired.Icon
	}

	for name, want := range desired.Properties {
		have, exists := actual.Properties[name]
		if !exists {
			// Propriété neuve : la valeur du YAML, options toutes neuves.
			target.Properties[name] = newProperty(want)
			continue
		}
		if want.Type != have.Type {
			// Mesuré le 2026-09-24 : un changement de type recrée les options et
			// ignore les ids transmis. planLines annonce ces mêmes options, toutes
			// en `+`, depuis le même newProperty : les deux restent alignés.
			p := newProperty(want)
			p.ID = have.ID
			target.Properties[name] = p
			continue
		}

		p := state.Property{ID: have.ID, Type: have.Type, Format: have.Format}
		if want.Format != "" {
			p.Format = want.Format
		}
		pr := pairOptions(want, have, applied.Properties[name])
		for wi, w := range want.Options {
			o := state.Option{Key: w.Key, Name: w.Name, Color: w.Color, Group: w.Group}
			if i := pr.At[wi]; i != -1 {
				remote := have.Options[i]
				o.ID = remote.ID
				// Le nom et la couleur d'une option existante ne sont pas
				// écrivables : un nom divergent rend 200 sans effet, une couleur
				// divergente rend 400. Les deux cas retiennent la ressource
				// entière, donc la cible ne sert pas — mais elle doit rester VRAIE
				// plutôt que de promettre une écriture impossible.
				o.Name = remote.Name
				o.Color = remote.Color
				if o.Group == "" {
					o.Group = remote.Group
				}
			}
			p.Options = append(p.Options, o)
		}
		target.Properties[name] = p
	}
	return target
}

// newProperty rend la valeur cible d'une propriété dont aucune option ne peut
// hériter d'une identité distante : une propriété neuve, ou une propriété dont
// le type change. Aucune option ne porte d'id.
func newProperty(want state.Property) state.Property {
	out := state.Property{Type: want.Type, Format: want.Format}
	for _, o := range want.Options {
		out.Options = append(out.Options, state.Option{
			Key: o.Key, Name: o.Name, Color: o.Color, Group: o.Group,
		})
	}
	return out
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
	for _, f := range []struct{ field, value string }{
		{"name", target.Name},
		{"description", target.Description},
		{"icon", target.Icon},
	} {
		if f.value == "" {
			continue
		}
		d := resources.NewDetail("+",
			fmt.Sprintf("%s %q", f.field, f.value), change.ClassSafe)
		d.Field = f.field
		out = append(out, d)
	}

	for _, name := range sortedPropNames(target.Properties) {
		p := target.Properties[name]
		d := resources.NewDetail("+",
			fmt.Sprintf("property %q (%s)", name, p.Type), change.ClassSafe)
		d.Property = name
		out = append(out, d)
		out = append(out, newOptionLines(name, p.Options)...)
	}
	return out
}

// newOptionLines annonce les options d'une propriété écrite sans aucune
// identité distante : à la création, sous une propriété neuve, ou sous un
// changement de type. Toutes partent, avec leur couleur et leur groupe : les
// taire serait écrire ce que le plan n'a jamais montré.
//
// L'ordre des options est celui du YAML : il est visible dans Notion, le trier
// le rendrait faux.
func newOptionLines(propName string, opts []state.Option) []resources.Detail {
	var out []resources.Detail
	for _, o := range opts {
		d := resources.NewDetail("+",
			fmt.Sprintf("option %q (propriété %q)", o.Name, propName), change.ClassSafe)
		d.Property = propName
		d.Note = optionAttrNote(o)
		out = append(out, d)
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
		d := resources.NewDetail("~", "name", change.ClassSafe)
		d.Field = "name"
		d.Note = fmt.Sprintf("%q → %q", actual.Name, desired.Name)
		out = append(out, d)
	}
	if desired.Description != "" && desired.Description != actual.Description {
		d := resources.NewDetail("~", "description", change.ClassSafe)
		d.Field = "description"
		d.Note = fmt.Sprintf("%q → %q", actual.Description, desired.Description)
		out = append(out, d)
	}
	if desired.Icon != "" && desired.Icon != actual.Icon {
		d := resources.NewDetail("~", "icon", change.ClassSafe)
		d.Field = "icon"
		d.Note = fmt.Sprintf("%q → %q", actual.Icon, desired.Icon)
		out = append(out, d)
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
			d := resources.NewDetail("+",
				fmt.Sprintf("property %q (%s)", name, want.Type), change.ClassSafe)
			d.Property = name
			d.Note = note
			out = append(out, d)
			// Mêmes options que celles que la cible écrit : newProperty est la
			// seule source des deux.
			out = append(out, newOptionLines(name, newProperty(want).Options)...)
			continue
		}

		if want.Type != have.Type {
			// La table mesurée suffit à classer ; le compte des valeurs non vides
			// précisera l'ampleur.
			class := change.ClassifyTypeChange(have.Type, want.Type)
			d := resources.NewDetail("~", fmt.Sprintf("property %q", name), class)
			d.Property = name
			d.Note = fmt.Sprintf("%s → %s", have.Type, want.Type)
			// Un couple que la table dit SÛR (select→multi_select,
			// number→rich_text, status→select, date→rich_text) ne demande aucune
			// mesure : le compte ne changerait ni sa classe ni la décision, et
			// notion-seed paierait un appel contre l'API pour un nombre qui ne dit
			// rien. Ne pas payer d'appels pour rien est une propriété du produit,
			// pas une optimisation.
			if class != change.ClassSafe {
				d.Measure = &resources.Measurement{
					Property:     name,
					PropertyType: have.Type,
				}
			}
			out = append(out, d)
			// Un changement de type recrée les options : toutes celles du YAML
			// partent neuves, sans id, exactement comme sous une propriété neuve.
			out = append(out, newOptionLines(name, newProperty(want).Options)...)
			continue
		}
		if want.Type == "number" && want.Format != "" && want.Format != have.Format {
			d := resources.NewDetail("~", fmt.Sprintf("property %q", name), change.ClassSafe)
			d.Property = name
			d.Note = fmt.Sprintf("format %s → %s", have.Format, want.Format)
			out = append(out, d)
		}
		out = append(out, optionLines(name, want, have, applied.Properties[name])...)
	}
	return out
}

// pairing est le résultat de l'appariement des options déclarées aux options
// distantes. Un seul exemplaire de cette règle existe : optionLines en dérive
// les lignes du plan, updateTarget en dérive la cible écrite. Deux règles
// d'identité divergentes feraient écrire une option que le plan aurait montrée
// ailleurs.
type pairing struct {
	// At donne, pour chaque option déclarée, la position de l'option distante
	// appariée, ou -1.
	At []int
	// ViaKey dit que l'appariement est passé par la key de config. Il sépare les
	// deux passes de optionLines, dont l'ordre de sortie est visible.
	ViaKey []bool
	// Claimed dit quelles positions distantes ont été prises. Les autres seront
	// détruites par l'écriture : l'API remplace la liste entière.
	Claimed []bool
}

// pairOptions suit la chaîne d'identité : applied ↔ actual par id, desired ↔
// applied par key, à défaut par nom.
//
// La réclamation se fait par POSITION dans have.Options, pas par nom : un
// renommage libère son ancien nom, et une réclamation par nom croirait ce nom
// encore occupé. L'index est aussi robuste à un id vide, qu'un state écrit à la
// main peut porter.
func pairOptions(want, have, applied state.Property) pairing {
	p := pairing{
		At:      make([]int, len(want.Options)),
		ViaKey:  make([]bool, len(want.Options)),
		Claimed: make([]bool, len(have.Options)),
	}
	for i := range p.At {
		p.At[i] = -1
	}

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

	idxByKey := make(map[string]int)
	for _, o := range applied.Options {
		if o.Key == "" || o.ID == "" {
			continue
		}
		if i, ok := idxByID[o.ID]; ok {
			idxByKey[o.Key] = i
		}
	}

	// Passe 1 : les options à key, qui ont une identité stable.
	for wi, w := range want.Options {
		if w.Key == "" {
			continue
		}
		i, ok := idxByKey[w.Key]
		if !ok {
			continue
		}
		p.At[wi], p.ViaKey[wi], p.Claimed[i] = i, true, true
	}

	// Passe 2 : les autres, appariées par nom — mais seulement sur une position
	// que la passe 1 n'a pas déjà prise.
	for wi, w := range want.Options {
		if p.At[wi] != -1 {
			continue
		}
		if i, ok := idxByName[w.Name]; ok && !p.Claimed[i] {
			p.At[wi], p.Claimed[i] = i, true
		}
	}
	return p
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
	p := pairOptions(want, have, applied)

	var out []resources.Detail

	// Passe 1 : les options dont la key a résolu.
	for wi, w := range want.Options {
		if !p.ViaKey[wi] {
			continue
		}
		current := have.Options[p.At[wi]]
		if current.Name != w.Name {
			d := resources.NewDetail("~",
				fmt.Sprintf("option %q → %q (propriété %q)", current.Name, w.Name, propName),
				change.ClassMigration)
			d.Property = propName
			d.Note = "l'API répond 200 sans rien changer : créer, migrer les lignes, puis retirer"
			// Le remède passe par le retrait de l'ancienne option : son coût est
			// le nombre de lignes qui la portent, et c'est le nom ACTUEL qui sait
			// les filtrer.
			d.Measure = &resources.Measurement{
				Property: propName, PropertyType: have.Type, Option: current.Name,
			}
			out = append(out, d)
			continue
		}
		// Le nom coïncide : pas de migration en cours sur cette ligne, donc la
		// place est libre pour comparer color et group. Sur une migration, la
		// ligne porte déjà son propre changement ; superposer un second écart y
		// sèmerait la confusion sans rien ajouter.
		out = append(out, optionAttrLines(propName, have.Type, w, current)...)
	}

	// Passe 2 : les options sans key, ou dont la key n'a pas résolu.
	for wi, w := range want.Options {
		if p.ViaKey[wi] {
			continue
		}
		if i := p.At[wi]; i != -1 {
			out = append(out, optionAttrLines(propName, have.Type, w, have.Options[i])...)
			continue
		}
		d := resources.NewDetail("+",
			fmt.Sprintf("option %q (propriété %q)", w.Name, propName), change.ClassSafe)
		d.Property = propName
		out = append(out, d)
	}

	// Passe 3 : ce que le YAML ne réclame pas SERA détruit dès qu'on écrit cette
	// propriété — l'API remplace la liste entière au lieu de la fusionner.
	for i, o := range have.Options {
		if p.Claimed[i] {
			continue
		}
		out = append(out, resources.Detail{
			Op:       "-",
			Target:   fmt.Sprintf("option %q (propriété %q)", o.Name, propName),
			Property: propName,
			Note:     "absente du YAML : l'API remplace la liste entière des options",
			// Classe et compte viennent de la mesure. Avant elle, on ne sait pas :
			// -1 dit « non mesuré », et ClassifyOptionRemoval le traduit en impact
			// inconnu plutôt qu'en « sûr ».
			Class: change.ClassifyOptionRemoval(have.Type, -1),
			Count: -1,
			Measure: &resources.Measurement{
				Property: propName, PropertyType: have.Type, Option: o.Name,
			},
		})
	}
	return out
}

// optionAttrLines compare color et group d'une option appariée dont le nom
// coïncide déjà. Les deux attributs ne se comportent PAS pareil, et c'est
// mesuré :
//
//   - color est IMMUABLE. L'API répond 400 — « Cannot update color of select
//     with id » — par id comme par nom, et l'échec porte sur tout le PATCH de la
//     propriété. Le changement est donc inexprimable : il faut créer une option,
//     migrer les lignes, retirer l'ancienne. D'où ClassMigration, et une mesure,
//     puisque ce remède coûte autant de lignes que l'option en porte.
//   - group est MUTABLE par id, et survit même quand on l'omet. Classe sûre,
//     comme le format de number.
//
// Ne compare que ce que le YAML déclare : une couleur ou un group absent du
// YAML ne doit jamais produire de changement fantôme.
func optionAttrLines(propName, propType string, want, have state.Option) []resources.Detail {
	var out []resources.Detail
	target := fmt.Sprintf("option %q (propriété %q)", want.Name, propName)
	if want.Color != "" && want.Color != have.Color {
		d := resources.NewDetail("~", target, change.ClassMigration)
		d.Property = propName
		d.Note = fmt.Sprintf(
			"color %s → %s : la couleur d'une option est immuable, l'API répond 400. "+
				"Créer une option, migrer les lignes, puis retirer l'ancienne",
			have.Color, want.Color)
		d.Measure = &resources.Measurement{
			Property:     propName,
			PropertyType: propType,
			Option:       have.Name,
		}
		out = append(out, d)
	}
	if want.Group != "" && want.Group != have.Group {
		d := resources.NewDetail("~", target, change.ClassSafe)
		d.Property = propName
		d.Note = fmt.Sprintf("group %s → %s", have.Group, want.Group)
		out = append(out, d)
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
	// Même garde de non-vacuité : un state qui n'a jamais capturé l'icône ne
	// doit pas faire passer sa valeur réelle pour une dérive à chaque run.
	if applied.Icon != "" && applied.Icon != actual.Icon {
		out = append(out, fmt.Sprintf(
			"~ icône %q → %q hors de notion-seed", applied.Icon, actual.Icon))
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

// WorstClass rend la classe la plus grave d'un jeu de détails. L'ordre de
// l'énumération va du plus sûr au plus grave, donc le maximum suffit.
//
// Exportée parce que la passe de mesure, qui reclasse les détails APRÈS Compute,
// doit recalculer la classe d'en-tête d'une ressource depuis un autre paquet.
// Un seul exemplaire : la dupliquer laisserait deux règles de gravité diverger.
func WorstClass(ds []resources.Detail) change.Class {
	worst := change.ClassSafe
	for _, d := range ds {
		if d.Class > worst {
			worst = d.Class
		}
	}
	return worst
}
