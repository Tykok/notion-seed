// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/change"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
	"github.com/tykok/notion-seed/core/state"
)

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
			name:      "option renommée sans key : retrait plus ajout",
			desired:   db("Statut", status(state.Option{Name: "Terminé", Group: "Complete"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "Fait", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSilentRewrite,
			wantLine:  `"Fait"`,
		},
		{
			name:      "option de status retirée : réécriture silencieuse",
			desired:   db("Statut", status(state.Option{Key: "todo", Name: "À faire", Group: "To-do"})),
			applied:   db("Statut", status(state.Option{ID: "o1", Key: "todo", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Key: "ko", Name: "Annulé", Group: "Complete"})),
			actual:    db("Statut", status(state.Option{ID: "o1", Name: "À faire", Group: "To-do"}, state.Option{ID: "o2", Name: "Annulé", Group: "Complete"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassSilentRewrite,
			wantLine:  `"Annulé"`,
		},
		{
			name:      "option de select retirée : destructif",
			desired:   db("Tag", sel(state.Option{Key: "a", Name: "A"})),
			applied:   db("Tag", sel(state.Option{ID: "o1", Key: "a", Name: "A"}, state.Option{ID: "o2", Key: "b", Name: "B"})),
			actual:    db("Tag", sel(state.Option{ID: "o1", Name: "A"}, state.Option{ID: "o2", Name: "B"})),
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassDestructive,
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
			name:    "propriété hors config : listée, non touchée",
			desired: db("Name", state.Property{Type: "title"}),
			applied: db("Name", state.Property{ID: "p1", Type: "title"}),
			actual: &state.Database{Name: "Tasks", Properties: map[string]state.Property{
				"Name":    {ID: "p1", Type: "title"},
				"Créé le": {ID: "p9", Type: "created_time"},
			}},
			wantKind:  resources.KindNone,
			wantClass: change.ClassSafe,
			wantUnman: `"Créé le"`,
		},
		{
			name:    "dérive : option renommée dans Notion",
			desired: db("Statut", status(state.Option{Key: "done", Name: "Fait", Group: "Complete"})),
			applied: db("Statut", status(state.Option{ID: "o1", Key: "done", Name: "Fait", Group: "Complete"})),
			actual:  db("Statut", status(state.Option{ID: "o1", Name: "Terminé", Group: "Complete"})),
			// Le YAML gagne : on replanifie le retour à "Fait".
			wantKind:  resources.KindUpdate,
			wantClass: change.ClassMigration,
			wantDrift: `"Fait"`,
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
