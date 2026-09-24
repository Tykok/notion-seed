// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

// Une création doit annoncer ses OPTIONS, pas seulement ses propriétés. Sans
// ça, apply écrit des options — avec leur couleur et leur groupe — que le plan
// n'a jamais montrées.
func TestCompareDatabaseListsOptionsOnCreation(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{
				{Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"},
			}},
		},
	}
	res := CompareDatabase("tasks", &desired, nil, nil)

	var lines []string
	for _, d := range res.Changeset.Details {
		lines = append(lines, d.Op+" "+d.Target+" — "+d.Note)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{`option "À faire"`, "color blue", "group To-do"} {
		if !strings.Contains(joined, want) {
			t.Errorf("détails =\n%s\nil manque %q", joined, want)
		}
	}
}

// Une création écrit AUSSI le nom, la description et l'icône : ils doivent donc
// être annoncés. Les omettre était la moitié non corrigée du même défaut que
// les options — écrire ce que le plan n'a pas montré.
func TestCompareDatabaseAnnouncesNameDescriptionAndIconOnCreation(t *testing.T) {
	desired := state.Database{
		Name:        "Notes de réunion",
		Description: "Toutes les notes",
		Icon:        "📝",
		Properties:  map[string]state.Property{"Titre": {Type: "title"}},
	}
	res := CompareDatabase("notes", &desired, nil, nil)

	var lines []string
	for _, d := range res.Changeset.Details {
		lines = append(lines, d.Op+" "+d.Target)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		`name "Notes de réunion"`,
		`description "Toutes les notes"`,
		`icon "📝"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("détails =\n%s\nil manque %q", joined, want)
		}
	}
	// Le nom vient en premier : c'est ce que l'utilisateur cherche d'abord.
	if !strings.HasPrefix(joined, `+ name "Notes de réunion"`) {
		t.Errorf("la première ligne doit être le nom, obtenu :\n%s", joined)
	}
}

// Ce que le YAML ne déclare pas n'est pas écrit, donc n'est pas annoncé : même
// garde de non-vacuité que partout ailleurs.
func TestCompareDatabaseOmitsUndeclaredFieldsOnCreation(t *testing.T) {
	desired := state.Database{
		Name:       "Notes",
		Properties: map[string]state.Property{"Titre": {Type: "title"}},
	}
	res := CompareDatabase("notes", &desired, nil, nil)
	for _, d := range res.Changeset.Details {
		if strings.HasPrefix(d.Target, "description") || strings.HasPrefix(d.Target, "icon") {
			t.Errorf("ligne inattendue %q : le YAML ne déclare ni description ni icône", d.Target)
		}
	}
}

// La cible résolue EST ce qui sera écrit. Tant qu'apply ne fait que des
// créations, elle coïncide avec la voie desired, et c'est précisément
// l'invariant qui rend plan et apply indissociables.
func TestCompareDatabaseResolvesTargetOnCreation(t *testing.T) {
	desired := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Name": {Type: "title"}},
	}
	res := CompareDatabase("tasks", &desired, nil, nil)
	if res.Target == nil {
		t.Fatal("Target = nil, want la cible résolue de la création")
	}
	if !reflect.DeepEqual(*res.Target, desired) {
		t.Errorf("Target = %+v, want %+v", *res.Target, desired)
	}
}

// db construit une database pivot à une seule propriété, pour alléger la table.
func db(propName string, p state.Property) *state.Database {
	return &state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{propName: p},
	}
}

func status(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "status", Options: opts}
}

func sel(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "select", Options: opts}
}

func multiSel(opts ...state.Option) state.Property {
	return state.Property{ID: "p1", Type: "multi_select", Options: opts}
}

// fixtureTasks rend un triplet qui produit au moins une ligne de chaque forme :
// un champ de database, une propriété neuve, un changement de type, une option
// neuve, une option retirée. Les tests de ce fichier s'en servent plutôt que de
// remonter un triplet chacun.
func fixtureTasks() (desired, applied, actual state.Database) {
	actual = state.Database{
		ID: "db-1", DataSourceID: "ds-1",
		Name: "Tasks", Description: "ancienne", Icon: "🔵",
		Properties: map[string]state.Property{
			"Name":  {ID: "title", Type: "title"},
			"Notes": {ID: "n1", Type: "rich_text"},
			"Libre": {ID: "l1", Type: "rich_text"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	applied = state.Database{
		ID: "db-1", DataSourceID: "ds-1",
		Name: "Tasks", Description: "ancienne", Icon: "🔵",
		Properties: map[string]state.Property{
			"Name":  {ID: "title", Type: "title"},
			"Notes": {ID: "n1", Type: "rich_text"},
			"Libre": {ID: "l1", Type: "rich_text"},
			"Prio": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o-haute", Key: "haute", Name: "Haute", Color: "red"},
				{ID: "o-basse", Key: "basse", Name: "Basse", Color: "blue"},
			}},
		},
	}
	desired = state.Database{
		Name: "Tâches", Description: "nouvelle", Icon: "🟢",
		Properties: map[string]state.Property{
			"Name":  {Type: "title"},
			"Notes": {Type: "number", Format: "number"},
			"Neuve": {Type: "rich_text"},
			"Prio": {Type: "select", Options: []state.Option{
				{Key: "haute", Name: "Haute", Color: "red"},
				{Key: "moyenne", Name: "Moyenne", Color: "orange"},
			}},
		},
	}
	return desired, applied, actual
}

func TestUpdateTargetCarriesRemoteOptionIDs(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Target == nil {
		t.Fatal("Target = nil sur un update")
	}

	prio := res.Target.Properties["Prio"]
	if len(prio.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(prio.Options))
	}
	// "Haute" existe dans Notion : la cible DOIT porter son id, sinon le PATCH
	// la détruit et la recrée, et les lignes qui la portaient perdent leur
	// valeur.
	if prio.Options[0].Name != "Haute" || prio.Options[0].ID != "o-haute" {
		t.Errorf("option[0] = %+v, want Haute / o-haute", prio.Options[0])
	}
	// "Moyenne" est neuve : pas d'id, l'API lui en crée un.
	if prio.Options[1].Name != "Moyenne" || prio.Options[1].ID != "" {
		t.Errorf("option[1] = %+v, want Moyenne sans id", prio.Options[1])
	}
	// "Basse" n'est plus réclamée : son absence de la cible EST ce qui la
	// détruit, et le plan l'annonce déjà par une ligne "-".
	for _, o := range prio.Options {
		if o.Name == "Basse" {
			t.Error("l'option Basse est dans la cible alors que le YAML ne la réclame plus")
		}
	}
}

func TestUpdateTargetKeepsUndeclaredPropertiesOut(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if _, present := res.Target.Properties["Libre"]; present {
		t.Error("la propriété hors config est dans la cible : elle partirait dans un payload")
	}
}

func TestUpdateTargetDoesNotOverwriteUndeclaredFields(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Name, desired.Description, desired.Icon = "", "", ""
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Target.Name != "Tasks" || res.Target.Description != "ancienne" || res.Target.Icon != "🔵" {
		t.Errorf("cible = %q / %q / %q, want les valeurs réelles intactes",
			res.Target.Name, res.Target.Description, res.Target.Icon)
	}
}

// Mesuré le 2026-09-24 : un changement de type RECRÉE les options, et l'API
// ignore les ids transmis (envoyés 6993c61f/36af0279, rendus fba2569a/f62f86cf).
// Les porter ferait croire à une continuité d'identité qui n'existe pas.
func TestUpdateTargetDropsOptionIDsOnTypeChange(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "multi_select", Options: []state.Option{
		{Key: "haute", Name: "Haute", Color: "red"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	prio := res.Target.Properties["Prio"]
	if prio.Type != "multi_select" {
		t.Fatalf("Type = %q, want multi_select", prio.Type)
	}
	for _, o := range prio.Options {
		if o.ID != "" {
			t.Errorf("option %q porte l'id %q alors que le type change", o.Name, o.ID)
		}
	}
}

// Review Focus #5 : une option dont ni la key ni le nom ne résolvent est
// NEUVE. Lui donner l'id d'une autre option en ferait un renommage silencieux,
// exactement ce que ce produit existe pour rendre impossible.
func TestUpdateTargetGivesNoIDToAnUnresolvedOption(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "inconnue", Name: "Inconnue", Color: "gray"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	prio := res.Target.Properties["Prio"]
	if len(prio.Options) != 1 {
		t.Fatalf("options = %d, want 1", len(prio.Options))
	}
	if prio.Options[0].ID != "" {
		t.Errorf("ID = %q, want vide : ni la key ni le nom ne résolvent", prio.Options[0].ID)
	}
}

func TestPlanLinesComparesIcon(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	var found *resources.Detail
	for i, d := range res.Changeset.Details {
		if d.Field == "icon" {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("aucune ligne d'icône alors que 🔵 → 🟢")
	}
	if found.Op != "~" {
		t.Errorf("Op = %q, want %q", found.Op, "~")
	}
}

// Même garde de non-vacuité que pour le nom : une icône que le YAML ne déclare
// pas ne doit produire aucune ligne.
func TestPlanLinesIgnoresUndeclaredIcon(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Icon = ""
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	for _, d := range res.Changeset.Details {
		if d.Field == "icon" {
			t.Errorf("ligne d'icône %q alors que le YAML n'en déclare pas", d.Target)
		}
	}
}

func TestDriftLinesNamesIconChangedOutside(t *testing.T) {
	_, applied, actual := fixtureTasks()
	actual.Icon = "🟣"
	desired := applied // le YAML colle au dernier état appliqué
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	joined := strings.Join(res.Drift, "\n")
	if !strings.Contains(joined, "icône") {
		t.Errorf("dérive = %q, want une ligne nommant l'icône", joined)
	}
}

// D1 (mesuré le 2026-09-24) : la couleur d'une option est immuable côté API —
// le PATCH répond 400 « Cannot update color of select with id/name » et fait
// échouer toute la propriété. Le remède passe par la recréation de l'option,
// donc ClassMigration, et son coût se mesure — le nombre de lignes qui portent
// l'option ACTUELLE.
func TestOptionColorChangeIsMigrationAndIsMeasured(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	// Une seule option, un seul écart : la couleur.
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "haute", Name: "Haute", Color: "purple"},
		{Key: "basse", Name: "Basse", Color: "blue"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	var found *resources.Detail
	for i, d := range res.Changeset.Details {
		if strings.Contains(d.Note, "color") {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("aucune ligne de couleur alors que red → purple")
	}
	if found.Class != change.ClassMigration {
		t.Errorf("Class = %v, want ClassMigration", found.Class)
	}
	if found.Measure == nil {
		t.Fatal("Measure = nil : le remède passe par le retrait de l'option, son coût se mesure")
	}
	if found.Measure.Option != "Haute" || found.Measure.PropertyType != "select" {
		t.Errorf("Measure = %+v, want Option=Haute PropertyType=select", *found.Measure)
	}
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1 (non mesuré)", found.Count)
	}
}

// Le group est MUTABLE par id (mesuré le 2026-09-24 : "Fait" déplacée de
// Complete vers In progress). Il ne doit donc pas suivre la couleur.
func TestOptionGroupChangeStaysSafe(t *testing.T) {
	actual := state.Database{
		ID: "db-1", DataSourceID: "ds-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "s1", Type: "status", Options: []state.Option{
				{ID: "o-fait", Name: "Fait", Color: "green", Group: "Complete"},
			}},
		},
	}
	applied := actual
	applied.Properties = map[string]state.Property{
		"Statut": {ID: "s1", Type: "status", Options: []state.Option{
			{ID: "o-fait", Key: "fait", Name: "Fait", Color: "green", Group: "Complete"},
		}},
	}
	desired := state.Database{Name: "Tasks", Properties: map[string]state.Property{
		"Statut": {Type: "status", Options: []state.Option{
			{Key: "fait", Name: "Fait", Group: "In progress"},
		}},
	}}

	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if len(res.Changeset.Details) != 1 {
		t.Fatalf("détails = %d, want 1 : %+v", len(res.Changeset.Details), res.Changeset.Details)
	}
	if got := res.Changeset.Details[0].Class; got != change.ClassSafe {
		t.Errorf("Class = %v, want ClassSafe — le group est mutable", got)
	}
}

// Un renommage porte lui aussi son coût : le remède passe par le retrait de
// l'ancienne option.
func TestOptionRenameIsMeasured(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	desired.Properties["Prio"] = state.Property{Type: "select", Options: []state.Option{
		{Key: "haute", Name: "Très haute", Color: "red"},
		{Key: "basse", Name: "Basse", Color: "blue"},
	}}
	res := CompareDatabase("tasks", &desired, &applied, &actual)

	for _, d := range res.Changeset.Details {
		if d.Class != change.ClassMigration {
			continue
		}
		if d.Measure == nil {
			t.Fatalf("ligne de migration %q sans Measure", d.Target)
		}
		if d.Measure.Option != "Haute" {
			t.Errorf("Measure.Option = %q, want %q (le nom ACTUEL, celui qui filtre)",
				d.Measure.Option, "Haute")
		}
		return
	}
	t.Fatal("aucune ligne de migration alors que le nom change")
}

func TestUpdateDetailsNameExactlyOnePropertyOrField(t *testing.T) {
	desired, applied, actual := fixtureTasks()
	res := CompareDatabase("tasks", &desired, &applied, &actual)
	if res.Changeset.Kind != resources.KindUpdate {
		t.Fatalf("Kind = %v, want KindUpdate", res.Changeset.Kind)
	}
	if len(res.Changeset.Details) == 0 {
		t.Fatal("aucun détail : la fixture ne teste rien")
	}
	for _, d := range res.Changeset.Details {
		hasProp, hasField := d.Property != "", d.Field != ""
		if hasProp == hasField {
			t.Errorf("détail %q %q : Property=%q Field=%q — il en faut exactement un",
				d.Op, d.Target, d.Property, d.Field)
		}
	}
}

func TestCreateDetailsNameExactlyOnePropertyOrField(t *testing.T) {
	desired, _, _ := fixtureTasks()
	res := CompareDatabase("tasks", &desired, nil, nil)
	if res.Changeset.Kind != resources.KindCreate {
		t.Fatalf("Kind = %v, want KindCreate", res.Changeset.Kind)
	}
	for _, d := range res.Changeset.Details {
		hasProp, hasField := d.Property != "", d.Field != ""
		if hasProp == hasField {
			t.Errorf("détail %q %q : Property=%q Field=%q — il en faut exactement un",
				d.Op, d.Target, d.Property, d.Field)
		}
	}
}

func TestCompareDatabase(t *testing.T) {
	cases := []struct {
		name      string
		desired   *state.Database
		applied   *state.Database
		actual    *state.Database
		wantKind  resources.ChangeKind
		wantClass change.Class
		wantLine  string // sous-chaîne attendue dans une ligne de changement
		wantDrift string // sous-chaîne attendue dans la dérive ("" = aucune)
		wantUnman string // sous-chaîne attendue en hors config ("" = aucun)
	}{
		{
			name:      "création quand rien n'existe",
			desired:   db("Name", state.Property{Type: "title"}),
			wantKind:  resources.KindCreate,
			wantClass: change.ClassSafe,
			wantLine:  `property "Name"`,
		},
		{
			name:      "aucun changement quand les trois voies coïncident",
			desired:   db("Statut", status(state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "À faire", Group: "To-do"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			name:      "option renommée par key",
			desired:   db("Statut", status(state.Option{Key: "done", Name: "Terminé", Group: "Complete"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantLine:  `"Fait" → "Terminé"`,
		},
		{
			// Le retrait passe par ClassifyOptionRemoval(have.Type, -1) à cet
			// endroit du plan : le nombre de lignes concernées n'y est pas encore
			// mesuré (une tâche ultérieure le branchera), donc -1 dit « non mesuré »
			// et la classe rendue est ClassUnknownImpact, quel que soit le type.
			name:      "option renommée sans key : retrait plus ajout, impact non mesuré",
			desired:   db("Statut", status(state.Option{Name: "Terminé", Group: "Complete"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"Fait"`,
		},
		{
			name:      "option de status retirée : impact non mesuré à ce point du plan",
			desired:   db("Statut", status(state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Key: "ko", Name: "Annulé", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Name: "Annulé", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"Annulé"`,
		},
		{
			name:      "option de select retirée : impact non mesuré à ce point du plan",
			desired:   db("Tag", sel(state.Option{Key: "a", Name: "A"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "a", Name: "A"}, state.Option{ID: "o2", Key: "b", Name: "B"})),
			actual:    db("Tag", sel(state.Option{ID: "o1", Name: "A"}, state.Option{ID: "o2", Name: "B"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"B"`,
		},
		{
			name:      "option de multi_select retirée : impact non mesuré à ce point du plan",
			desired:   db("Tags", multiSel(state.Option{Key: "a", Name: "A"})),
			applied:   db("Tags", multiSel(state.Option{ID: "o1", Key: "a", Name: "A"}, state.Option{ID: "o2", Key: "b", Name: "B"})),
			actual:    db("Tags", multiSel(state.Option{ID: "o1", Name: "A"}, state.Option{ID: "o2", Name: "B"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  `"B"`,
		},
		{
			name:      "option ajoutée : sûr",
			desired:   db("Tag", sel(state.Option{Key: "a", Name: "A"}, state.Option{Key: "b", Name: "B"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "a", Name: "A"})),
			actual:    db("Tag", sel(state.Option{ID: "o1", Name: "A"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  `"B"`,
		},
		{
			// L'id porté par `applied` ("o1") n'existe plus côté réel : l'option a
			// été détruite puis recréée hors de notion-seed, sous un nouvel id mais
			// le même nom. La key ne peut plus servir de pont — elle doit retomber
			// sur l'appariement par nom plutôt que produire un retrait suivi d'une
			// création fantômes.
			name:      "key déclarée sans correspondance dans le réel : retombe sur le nom",
			desired:   db("Tag", sel(state.Option{Key: "x", Name: "Foo"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "x", Name: "Foo"})),
			actual:    db("Tag", sel(state.Option{ID: "o2", Name: "Foo"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
			// L'id "o1" que portait `applied` a réellement disparu du réel : c'est
			// une vraie dérive (constat), distincte du plan (Changeset) qui, lui,
			// ne doit ni recréer ni retirer "Foo" — c'est ce que vérifient
			// wantKind/wantClass ci-dessus.
			wantDrift: "hors de notion-seed",
		},
		{
			// La classe vient désormais de la table MESURÉE, plus du refus par
			// principe : number → rich_text a été essayé le 2026-09-24 et ne perd
			// rien (7 devient "7"). L'annoncer destructif serait précisément
			// l'affirmation invérifiée que ce produit existe pour supprimer.
			name:      "type de propriété changé : classé par la table mesurée",
			desired:   db("Estimate", state.Property{Type: "rich_text"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "number → rich_text",
		},
		{
			// Hors de la table : personne n'a essayé, donc l'impact est inconnu —
			// et il domine l'en-tête, parce que ne pas savoir mérite plus
			// d'attention que savoir que c'est sûr.
			name:      "type de propriété changé hors de la table : impact inconnu",
			desired:   db("Estimate", state.Property{Type: "people"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassUnknownImpact,
			wantLine:  "number → people",
		},
		{
			name:      "format de number changé : sûr",
			desired:   db("Estimate", state.Property{Type: "number", Format: "euro"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "euro",
		},
		{
			// I1 : color et group sont stockés dans le state et comparés nulle
			// part avant cette correction — vérifié en inversant les deux groupes
			// et en changeant les deux couleurs dans un YAML de test, qui
			// ressortait alors en « Aucun changement ».
			//
			// D1 (mesuré le 2026-09-24) : la couleur d'une option est IMMUABLE côté
			// API — le PATCH répond 400 et fait échouer toute la propriété. Le
			// remède passe par la recréation de l'option, d'où ClassMigration à la
			// place du ClassSafe d'origine.
			name: "option couleur déclarée et différente : migration requise",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Color: "red", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantLine:  "color blue → red",
		},
		{
			// Symétrique du cas précédent : sans `color` dans le YAML, une couleur
			// qui diffère côté réel n'est pas l'affaire de notion-seed — exactement
			// comme une description omise n'écrase jamais celle de Notion.
			name: "option couleur absente du YAML : aucune ligne",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"})),
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			// Le group d'une option déplacée à la main dans Notion doit ressortir
			// à la fois en dérive (constat) et en plan (réconciliation vers le
			// YAML) — même schéma que le renommage d'option testé plus haut.
			name: "dérive : group d'une option changé dans Notion",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Group: "In progress"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  "group In progress → To-do",
			wantDrift: "groupe To-do → In progress",
		},
		{
			// La description n'est pas gardée comme le nom : une database sans
			// `description` dans le YAML ne doit jamais proposer d'écraser celle que
			// porte Notion — sinon chaque plan afficherait un changement fantôme.
			name:    "description omise du YAML : aucune ligne",
			desired: db("Name", state.Property{Type: "title"}),
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Description: "Description existante", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
		},
		{
			name: "description déclarée et différente : les deux valeurs",
			desired: &state.Database{Name: "Tasks", Description: "Nouvelle description", Properties: map[string]state.Property{
				"Name": {Type: "title"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Description: "Ancienne description", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
			wantLine:  `"Ancienne description" → "Nouvelle description"`,
		},
		{
			name:    "propriété hors config : listée, non touchée",
			desired: db("Name", state.Property{Type: "title"}),
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Properties: map[string]state.Property{
				"Name":    {ID: "p1", Type: "title"},
				"Créé le": {ID: "p9", Type: "created_time"},
			}},
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
			// "Créé le" n'est dans aucun `applied` connu : elle est aussi une
			// dérive (apparue hors de notion-seed), en plus d'être hors config.
			wantDrift: `"Créé le"`,
			wantUnman: `"Créé le"`,
		},
		{
			name:    "dérive : option renommée dans Notion",
			desired: db("Statut", status(state.Option{Key: "done", Name: "Fait", Group: "Complete"})),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait", Group: "Complete"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "Terminé", Group: "Complete"})),
			// Le YAML gagne : on replanifie le retour à "Fait". L'assertion cible le
			// texte du renommage, pas seulement "Fait" : une ligne de RETRAIT
			// contiendrait aussi "Fait" et laisserait passer une régression qui
			// confondrait renommage et suppression.
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantDrift: `renommée en "Terminé"`,
		},
		{
			name:      "database orpheline : destruction planifiée",
			applied:   db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:    db("Name", state.Property{ID: "p1", Type: "title"}),
			wantKind:  resources.KindDestroy,
			wantClass: change.ClassDestructive,
			wantLine:  "database.tasks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareDatabase("tasks", tc.desired, tc.applied, tc.actual)

			if got.Changeset.Kind != tc.wantKind {
				t.Errorf("Kind = %v, want %v", got.Changeset.Kind, tc.wantKind)
			}
			if c := WorstClass(got.Changeset.Details); c != tc.wantClass {
				t.Errorf("classe = %v, want %v (détails: %+v)", c, tc.wantClass, got.Changeset.Details)
			}
			if tc.wantLine != "" && !containsSub(detailStrings(got.Changeset.Details), tc.wantLine) {
				t.Errorf("aucune ligne ne contient %q: %+v", tc.wantLine, got.Changeset.Details)
			}
			if tc.wantDrift == "" && len(got.Drift) != 0 {
				t.Errorf("dérive inattendue: %v", got.Drift)
			}
			if tc.wantDrift != "" && !containsSub(got.Drift, tc.wantDrift) {
				t.Errorf("dérive attendue contenant %q, got %v", tc.wantDrift, got.Drift)
			}
			if tc.wantUnman == "" && len(got.Unmanaged) != 0 {
				t.Errorf("hors config inattendu: %v", got.Unmanaged)
			}
			if tc.wantUnman != "" && !containsSub(got.Unmanaged, tc.wantUnman) {
				t.Errorf("hors config attendu contenant %q, got %v", tc.wantUnman, got.Unmanaged)
			}
		})
	}
}

func TestCompareDatabaseSaysNothingWhenRemoteWasNotRead(t *testing.T) {
	// --skip-preflight ne lit rien. Une database déjà importée ne doit surtout
	// pas ressortir en création.
	desired := db("Name", state.Property{Type: "title"})
	applied := db("Name", state.Property{ID: "p1", Type: "title"})

	got := CompareDatabase("tasks", desired, applied, nil)

	if got.Changeset.Kind != resources.KindNone {
		t.Errorf("Kind = %v, want KindNone", got.Changeset.Kind)
	}
	if len(got.Changeset.Details) != 0 {
		t.Errorf("aucun détail attendu sans lecture du réel: %+v", got.Changeset.Details)
	}
}

// Review Focus 4 : un state écrit à la main peut porter une database sans
// `properties`. La map nulle doit se comporter comme vide.
func TestCompareDatabaseHandlesNilProperties(t *testing.T) {
	desired := &state.Database{Name: "Tasks"}
	applied := &state.Database{ID: "db1", Name: "Tasks"}
	actual := &state.Database{ID: "db1", Name: "Tasks"}

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindNone {
		t.Errorf("Kind = %v, want KindNone", got.Changeset.Kind)
	}
}

func TestCompareDatabaseRenamedPropertyIsCreationPlusUnmanaged(t *testing.T) {
	desired := db("Charge", state.Property{Type: "number"})
	applied := db("Estimate", state.Property{ID: "p1", Type: "number"})
	actual := db("Estimate", state.Property{ID: "p1", Type: "number"})

	got := CompareDatabase("tasks", desired, applied, actual)

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Charge"`) {
		t.Errorf("la nouvelle propriété doit être créée: %v", lines)
	}
	if !containsSub(lines, "Estimate") {
		t.Errorf("la ligne doit prévenir que Estimate reste en place: %v", lines)
	}
	if !containsSub(got.Unmanaged, `"Estimate"`) {
		t.Errorf("Estimate doit apparaître hors config: %v", got.Unmanaged)
	}
	if WorstClass(got.Changeset.Details) != change.ClassSafe {
		t.Error("un renommage de propriété ne détruit rien, il duplique")
	}
}

// Correction 1 (ronde de relecture 1) : `claimed` était indexée par nom, donc
// une option renommée par key libérait son ancien nom, et la branche de
// repli par nom croyait ce nom encore occupé — une option déclarée sortait
// du plan sans une ligne. Triplet fourni par la relecture.
func TestCompareDatabaseOptionRenameFreesNameForNewOption(t *testing.T) {
	desired := db("Statut", status(
		state.Option{Key: "a", Name: "Terminé"},
		state.Option{Name: "Fait"},
	))
	applied := db("Statut", status(state.Option{ID: "o1", Key: "a", Name: "Fait"}))
	actual := db("Statut", status(state.Option{ID: "o1", Name: "Fait"}))

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindUpdate {
		t.Errorf("Kind = %v, want KindUpdate", got.Changeset.Kind)
	}
	if c := WorstClass(got.Changeset.Details); c != change.ClassMigration {
		t.Errorf("classe = %v, want ClassMigration (détails: %+v)", c, got.Changeset.Details)
	}

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Fait" → "Terminé"`) {
		t.Errorf("la migration par key doit apparaître: %v", lines)
	}
	if !containsSub(lines, `+ option "Fait"`) {
		t.Errorf(
			"la création de \"Fait\" doit apparaître : sans elle le YAML déclare "+
				"une option qui ne sera jamais créée, sans que le plan ne le montre: %v",
			lines)
	}
}

// Angle mort de la table (ronde de relecture 1) : un renommage par key et un
// retrait sur la même propriété status doivent produire les DEUX lignes, et
// la classe rendue doit rester la plus grave des deux. Le retrait n'est pas
// mesuré à ce point du plan (ClassifyOptionRemoval y reçoit -1), donc c'est
// l'impact inconnu qui domine la migration — pas la réécriture silencieuse
// que la mesure révélera une fois branchée.
func TestCompareDatabaseStatusRenameAndRemovalTogether(t *testing.T) {
	desired := db("Statut", status(state.Option{Key: "a", Name: "Migré"}))
	applied := db("Statut", status(
		state.Option{ID: "o1", Key: "a", Name: "Ancien"},
		state.Option{ID: "o2", Key: "b", Name: "Obsolète"},
	))
	actual := db("Statut", status(
		state.Option{ID: "o1", Name: "Ancien"},
		state.Option{ID: "o2", Name: "Obsolète"},
	))

	got := CompareDatabase("tasks", desired, applied, actual)

	if got.Changeset.Kind != resources.KindUpdate {
		t.Errorf("Kind = %v, want KindUpdate", got.Changeset.Kind)
	}
	if c := WorstClass(got.Changeset.Details); c != change.ClassUnknownImpact {
		t.Errorf("classe = %v, want ClassUnknownImpact (détails: %+v)", c, got.Changeset.Details)
	}

	lines := detailStrings(got.Changeset.Details)
	if !containsSub(lines, `"Ancien" → "Migré"`) {
		t.Errorf("la migration doit apparaître: %v", lines)
	}
	if !containsSub(lines, `"Obsolète"`) {
		t.Errorf("le retrait doit apparaître: %v", lines)
	}
}

// Un retrait d'option doit porter la DEMANDE de mesure, pas la mesure : le
// comparateur reste pur, c'est là que vit la sûreté du produit.
func TestCompareDatabaseRequestsAMeasurementForOptionRemoval(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{{Key: "todo", Name: "À faire"}}},
		},
	}
	applied := state.Database{
		ID: "db-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o1", Key: "todo", Name: "À faire"},
				{ID: "o2", Name: "Annulé"},
			}},
		},
	}
	res := CompareDatabase("tasks", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if res.Changeset.Details[i].Op == "-" {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("aucune ligne de retrait d'option")
	}
	if found.Measure == nil {
		t.Fatal("la ligne de retrait ne porte aucune demande de mesure")
	}
	if found.Measure.Property != "Statut" || found.Measure.Option != "Annulé" ||
		found.Measure.PropertyType != "status" {
		t.Errorf("Measure = %+v", *found.Measure)
	}
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1 tant que rien n'a été mesuré", found.Count)
	}
	if found.Class != change.ClassUnknownImpact {
		t.Errorf("Class = %v, want ClassUnknownImpact avant mesure", found.Class)
	}
}

// Un changement de type porte lui aussi sa demande — le nombre de valeurs non
// vides de la colonne — et sa classe vient de la table mesurée.
func TestCompareDatabaseClassifiesTypeChangeFromTheMeasuredTable(t *testing.T) {
	desired := state.Database{
		Name:       "Clients",
		Properties: map[string]state.Property{"Tags": {Type: "select"}},
	}
	applied := state.Database{
		ID: "db-1", Name: "Clients",
		Properties: map[string]state.Property{"Tags": {ID: "p1", Type: "multi_select"}},
	}
	res := CompareDatabase("clients", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if strings.Contains(res.Changeset.Details[i].Target, `property "Tags"`) {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("aucune ligne de changement de type")
	}
	// multi_select → select : mesuré réécriture silencieuse le 2026-09-24.
	if found.Class != change.ClassSilentRewrite {
		t.Errorf("Class = %v, want ClassSilentRewrite", found.Class)
	}
	if found.Measure == nil || found.Measure.Option != "" {
		t.Errorf("Measure = %+v, want une demande de comptage des valeurs non vides", found.Measure)
	}
}

// Un ajout d'option ne coûte rien : aucune mesure ne doit être demandée, donc
// aucun appel ne sera payé.
func TestCompareDatabaseRequestsNoMeasurementForSafeDetails(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {Type: "status", Options: []state.Option{
				{Key: "todo", Name: "À faire"}, {Key: "new", Name: "Neuve"}}},
		},
	}
	applied := state.Database{
		ID: "db-1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o1", Key: "todo", Name: "À faire"}}},
		},
	}
	res := CompareDatabase("tasks", &desired, &applied, &applied)
	for _, d := range res.Changeset.Details {
		if d.Op == "+" && d.Measure != nil {
			t.Errorf("la ligne %q demande une mesure alors qu'elle ne coûte rien", d.Target)
		}
	}
}

// Aucun détail émis par le comparateur ne doit porter Count == 0.
//
// POURQUOI c'est un bug et pas une lubie : 0 se lit « 0 ligne concernée », donc
// « rien à perdre », donc « sûr ». Or CompareDatabase est PURE — elle ne compte
// rien, elle se contente d'ÉMETTRE des demandes de mesure. Un 0 sorti d'ici
// serait donc une affirmation d'innocuité que personne n'a vérifiée, et c'est
// exactement le défaut que ce produit existe pour rendre impossible. Avant la
// passe de mesure, le seul compte honnête est -1 : « on ne sait pas ».
//
// Ce test existe parce que resources.NewDetail ne suffit pas : un littéral
// `resources.Detail{...}` reste toujours possible, et le champ Count vaut 0 par
// défaut en Go. Le constructeur est la commodité ; ce test est le garde-fou. Il
// balaie un éventail de cas, pas un seul, parce que le trou peut s'ouvrir dans
// n'importe quelle branche de planLines, optionLines ou createLines.
func TestCompareDatabaseNeverEmitsAnUnmeasuredZeroCount(t *testing.T) {
	withColor := func(name, color, group string) state.Property {
		return state.Property{ID: "p1", Type: "status", Options: []state.Option{
			{ID: "o1", Key: "k", Name: name, Color: color, Group: group},
		}}
	}

	cases := []struct {
		name                     string
		desired, applied, actual *state.Database
	}{
		{
			name:    "création complète",
			desired: db("Statut", status(state.Option{Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
		},
		{
			name: "nom et description modifiés",
			desired: &state.Database{Name: "Nouveau", Description: "Nouvelle", Properties: map[string]state.Property{
				"Name": {Type: "title"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Ancien", Description: "Ancienne", Properties: map[string]state.Property{
				"Name": {ID: "p1", Type: "title"},
			}},
		},
		{
			name: "ajout de propriété",
			desired: &state.Database{Name: "Tasks", Properties: map[string]state.Property{
				"Name":     {Type: "title"},
				"Estimate": {Type: "number"},
			}},
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:  db("Name", state.Property{ID: "p1", Type: "title"}),
		},
		{
			name:    "changement de type dans la table mesurée",
			desired: db("Tags", state.Property{Type: "multi_select"}),
			applied: db("Tags", state.Property{ID: "p1", Type: "select"}),
			actual:  db("Tags", state.Property{ID: "p1", Type: "select"}),
		},
		{
			name:    "changement de type hors de la table mesurée",
			desired: db("Estimate", state.Property{Type: "people"}),
			applied: db("Estimate", state.Property{ID: "p1", Type: "number"}),
			actual:  db("Estimate", state.Property{ID: "p1", Type: "number"}),
		},
		{
			name:    "format de number modifié",
			desired: db("Estimate", state.Property{Type: "number", Format: "euro"}),
			applied: db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:  db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
		},
		{
			name: "ajout d'option",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire"},
				state.Option{Name: "Neuve"},
			)),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "À faire"})),
		},
		{
			name:    "retrait d'option de status",
			desired: db("Statut", status(state.Option{Key: "todo", Name: "À faire"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire"},
				state.Option{ID: "o2", Name: "Annulé"},
			)),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire"},
				state.Option{ID: "o2", Name: "Annulé"},
			)),
		},
		{
			name:    "retrait d'option de select",
			desired: db("Tag", sel(state.Option{Key: "a", Name: "A"})),
			applied: db("Tag", sel(
				state.Option{ID: "o1", Key: "a", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
			actual: db("Tag", sel(
				state.Option{ID: "o1", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
		},
		{
			name:    "retrait d'option de multi_select",
			desired: db("Tag", multiSel(state.Option{Key: "a", Name: "A"})),
			applied: db("Tag", multiSel(
				state.Option{ID: "o1", Key: "a", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
			actual: db("Tag", multiSel(
				state.Option{ID: "o1", Name: "A"},
				state.Option{ID: "o2", Name: "B"},
			)),
		},
		{
			name:    "renommage d'option par key",
			desired: db("Statut", status(state.Option{Key: "done", Name: "Terminé"})),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "Fait"})),
		},
		{
			name:    "couleur et groupe d'option modifiés",
			desired: db("Statut", withColor("Fait", "green", "Complete")),
			applied: db("Statut", withColor("Fait", "blue", "To-do")),
			actual:  db("Statut", withColor("Fait", "blue", "To-do")),
		},
		{
			name:    "destruction d'une database orpheline",
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual:  db("Name", state.Property{ID: "p1", Type: "title"}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := CompareDatabase("tasks", tc.desired, tc.applied, tc.actual)
			if len(res.Changeset.Details) == 0 {
				t.Fatal("aucun détail : ce cas ne couvre plus rien, corrigez le triplet")
			}
			for _, d := range res.Changeset.Details {
				if d.Count == 0 {
					t.Errorf(
						"le détail %q %q porte Count = 0, donc « 0 ligne concernée », "+
							"donc « sûr » — alors que rien n'a été mesuré ; want -1",
						d.Op, d.Target)
				}
			}
		})
	}
}

// Un changement de type que la table MESURÉE dit sûr ne doit coûter aucun
// appel : le compte des valeurs non vides ne changerait ni sa classe ni la
// décision de l'utilisateur. Un plan qui ne contient qu'un `number → rich_text`
// paierait sinon un comptage contre l'API pour un nombre qui ne change rien.
func TestCompareDatabaseRequestsNoMeasurementForASafeTypeChange(t *testing.T) {
	desired := state.Database{
		Name:       "Clients",
		Properties: map[string]state.Property{"Estimate": {Type: "rich_text"}},
	}
	applied := state.Database{
		ID: "db-1", Name: "Clients",
		Properties: map[string]state.Property{
			"Estimate": {ID: "p1", Type: "number", Format: "number"}},
	}
	res := CompareDatabase("clients", &desired, &applied, &applied)

	var found *resources.Detail
	for i := range res.Changeset.Details {
		if strings.Contains(res.Changeset.Details[i].Target, `property "Estimate"`) {
			found = &res.Changeset.Details[i]
		}
	}
	if found == nil {
		t.Fatal("aucune ligne de changement de type")
	}
	// number → rich_text : mesuré sûr le 2026-09-24, donc rien à compter.
	if found.Class != change.ClassSafe {
		t.Fatalf("Class = %v, want ClassSafe", found.Class)
	}
	if found.Measure != nil {
		t.Errorf("Measure = %+v, want nil : un changement de type sûr ne coûte aucun appel",
			found.Measure)
	}
	// La ligne reste « non mesurée » : 0 vaudrait « aucune ligne concernée »,
	// une affirmation que personne n'a vérifiée.
	if found.Count != -1 {
		t.Errorf("Count = %d, want -1", found.Count)
	}
}

func detailStrings(ds []resources.Detail) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Op+" "+d.Target+" "+d.Note)
	}
	return out
}

func containsSub(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}
