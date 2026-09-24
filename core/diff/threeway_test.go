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

// Une ressource qu'apply ne sait pas encore écrire ne doit pas porter de
// cible : une cible non nulle est une autorisation d'écrire.
func TestCompareDatabaseLeavesTargetNilOnUpdate(t *testing.T) {
	desired := state.Database{
		Name: "Tasks",
		Properties: map[string]state.Property{
			"Name":     {Type: "title"},
			"Estimate": {Type: "number"},
		},
	}
	applied := state.Database{
		ID:         "db-1",
		Name:       "Tasks",
		Properties: map[string]state.Property{"Name": {ID: "title", Type: "title"}},
	}
	res := CompareDatabase("tasks", &desired, &applied, &applied)
	if res.Changeset.Kind != resources.KindUpdate {
		t.Fatalf("Kind = %v, want KindUpdate", res.Changeset.Kind)
	}
	if res.Target != nil {
		t.Errorf("Target = %+v, want nil tant qu'apply n'écrit pas les updates", res.Target)
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
			name:      "type de propriété changé : destructif",
			desired:   db("Estimate", state.Property{Type: "rich_text"}),
			applied:   db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			actual:    db("Estimate", state.Property{ID: "p1", Type: "number", Format: "number"}),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassDestructive,
			wantLine:  "number → rich_text",
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
			name: "option couleur déclarée et différente : ligne sûre",
			desired: db("Statut", status(
				state.Option{Key: "todo", Name: "À faire", Color: "red", Group: "To-do"})),
			applied: db("Statut", status(
				state.Option{ID: "o1", Key: "todo", Name: "À faire", Color: "blue", Group: "To-do"})),
			actual: db("Statut", status(
				state.Option{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSafe,
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
			if c := worstClass(got.Changeset.Details); c != tc.wantClass {
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
	if worstClass(got.Changeset.Details) != change.ClassSafe {
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
	if c := worstClass(got.Changeset.Details); c != change.ClassMigration {
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
	if c := worstClass(got.Changeset.Details); c != change.ClassUnknownImpact {
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
