// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/state"
)

// Une database retirée du YAML et déjà supprimée à la main dans Notion ne doit
// plus être annoncée « à détruire » : il n'y a plus rien à détruire. Ce qui
// reste est une entrée de state obsolète, dont le nettoyage n'écrit rien dans
// Notion.
func TestComputeReportsStaleStateForOrphanAlreadyDeleted(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "introuvable (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.ToDestroy != 0 {
		t.Errorf("ToDestroy = %d, want 0 : la destruction a déjà eu lieu", p.ToDestroy)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false : %v", p.BlockedReasons)
	}
}

// Une database archivée compte comme détruite : dans Notion, détruire une
// database, c'est l'archiver.
func TestComputeReportsStaleStateForArchivedOrphan(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "archivée ou en corbeille"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
}

// Une orpheline toujours présente dans Notion reste une destruction planifiée.
func TestComputeStillPlansDestroyWhenOrphanExists(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db-1", Name: "Tasks"}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.ToDestroy != 1 {
		t.Errorf("ToDestroy = %d, want 1", p.ToDestroy)
	}
	if len(p.StaleState) != 0 {
		t.Errorf("StaleState = %v, want vide", p.StaleState)
	}
}

// Sans refresh (--skip-preflight), on ne sait rien de l'orpheline : ni la
// détruire, ni conclure qu'elle a disparu. Elle tombe sous « Non comparé ».
func TestComputeDoesNotAnnounceDestroyWithoutRefresh(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}

	p, err := Compute(cfg, applied, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ToDestroy != 0 {
		t.Errorf("ToDestroy = %d, want 0 : rien n'a été lu", p.ToDestroy)
	}
	if got := p.NotCompared; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("NotCompared = %v, want [database.tasks]", got)
	}
}

// prevent_destroy protège la ressource la mieux gardée du fichier. La voir
// disparaître hors de notion-seed est le fait le plus grave que le plan puisse
// constater : on bloque au lieu de nettoyer en silence.
func TestComputeBlocksWhenProtectedOrphanVanished(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{PreventDestroy: []string{"database.tasks"}}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "introuvable (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Blocked {
		t.Fatal("Blocked = false, want true")
	}
	if len(p.StaleState) != 0 {
		t.Errorf("StaleState = %v, want vide : rien ne doit être nettoyé sous prevent_destroy", p.StaleState)
	}
	joined := strings.Join(p.BlockedReasons, "\n")
	for _, want := range []string{"prevent_destroy", "hors de notion-seed", "  → "} {
		if !strings.Contains(joined, want) {
			t.Errorf("raisons =\n%s\nil manque %q", joined, want)
		}
	}
}

func TestComputeAllCreatesWhenRemoteIsEmpty(t *testing.T) {
	cfg := &config.Config{
		Version:   1,
		Workspace: config.Workspace{ParentPageID: "page1"},
		Databases: []config.Database{
			{Key: "projects", Name: "Projects", Properties: map[string]config.Property{
				"Name": {Type: "title"},
			}},
			{Key: "tasks", Name: "Tasks", Properties: map[string]config.Property{
				"Name":     {Type: "title"},
				"Estimate": {Type: "number"},
			}},
		},
	}

	p, err := Compute(cfg, nil, nil)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.ToAdd != 2 {
		t.Errorf("ToAdd = %d, want 2", p.ToAdd)
	}
	if p.ToChange != 0 || p.ToDestroy != 0 {
		t.Errorf("ToChange = %d, ToDestroy = %d, want 0 / 0", p.ToChange, p.ToDestroy)
	}
	if p.Blocked {
		t.Error("Blocked = true, want false — des créations sont sûres")
	}
	if len(p.Changes) != 2 {
		t.Fatalf("changes = %d, want 2", len(p.Changes))
	}
	for _, c := range p.Changes {
		if c.Class != ClassSafe {
			t.Errorf("%s: Class = %v, want ClassSafe", c.Resource, c.Class)
		}
	}
}

func TestComputeKeepsConfigOrder(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{
			{Key: "alpha", Name: "Alpha", Properties: map[string]config.Property{"Name": {Type: "title"}}},
			{Key: "beta", Name: "Beta", Properties: map[string]config.Property{"Name": {Type: "title"}}},
		},
	}
	p, _ := Compute(cfg, nil, nil)
	if p.Changes[0].Resource != "database.alpha" || p.Changes[1].Resource != "database.beta" {
		t.Errorf("ordre = %q, %q", p.Changes[0].Resource, p.Changes[1].Resource)
	}
}

func TestComputeListsPropertiesOfCreatedDatabase(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{
			{Key: "tasks", Name: "Tasks", Properties: map[string]config.Property{
				"Name":     {Type: "title"},
				"Estimate": {Type: "number"},
			}},
		},
	}
	p, _ := Compute(cfg, nil, nil)
	// Le nom de la database est annoncé lui aussi : il part dans le payload de
	// création, donc il doit figurer au plan.
	want := []string{
		`+ name "Tasks"`,
		`+ property "Estimate" (number)`,
		`+ property "Name" (title)`,
	}
	if !reflect.DeepEqual(p.Changes[0].Lines, want) {
		t.Fatalf("lines = %v, want %v", p.Changes[0].Lines, want)
	}
}

func TestComputeEmptyConfigProducesEmptyPlan(t *testing.T) {
	p, err := Compute(&config.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.ToAdd != 0 || len(p.Changes) != 0 {
		t.Errorf("plan non vide: %+v", p)
	}
}

func TestComputeReportsDriftAndUnmanaged(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{{
			Key: "tasks", Name: "Tasks",
			Properties: map[string]config.Property{"Name": {Type: "title"}},
		}},
	}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Name": {ID: "p1", Type: "title"},
		}},
	}}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{
		ID: "db1", Name: "Renommée à la main", Properties: map[string]state.Property{
			"Name":    {ID: "p1", Type: "title"},
			"Créé le": {ID: "p9", Type: "created_time"},
		},
	}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if len(p.Drifts) != 1 {
		t.Fatalf("Drifts = %v, want 1 entrée", p.Drifts)
	}
	if len(p.Unmanaged) != 1 {
		t.Fatalf("Unmanaged = %v, want 1 entrée", p.Unmanaged)
	}
	if p.ToChange != 1 {
		t.Errorf("ToChange = %d, want 1 — le nom revient au YAML", p.ToChange)
	}
}

func TestComputeBlocksWhenManagedResourceVanished(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{{
		Key: "tasks", Name: "Tasks",
		Properties: map[string]config.Property{"Name": {Type: "title"}},
	}}}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks"},
	}}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "introuvable (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if !p.Blocked {
		t.Error("Blocked = false, want true — l'identité gérée a disparu")
	}
	if !strings.Contains(strings.Join(p.BlockedReasons, " "), "introuvable") {
		t.Errorf("BlockedReasons = %v, doit nommer la raison", p.BlockedReasons)
	}
}

// C2 : --skip-preflight passe un `actual` nil — refreshManaged n'a jamais
// tourné. CompareDatabase rend alors un résultat vide pour database.tasks
// (« on ne sait rien, donc on ne dit rien »), mais Compute doit quand même
// nommer la ressource : un Plan vide de partout ne doit jamais être confondu
// avec une conformité constatée.
func TestComputeReportsNotComparedWhenActualWasNotRead(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{{
		Key: "tasks", Name: "Tasks",
		Properties: map[string]config.Property{"Name": {Type: "title"}},
	}}}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks"},
	}}

	p, err := Compute(cfg, applied, nil)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.Blocked {
		t.Error("Blocked = true, want false — --skip-preflight n'est pas une erreur")
	}
	if len(p.Changes) != 0 || len(p.Unmanaged) != 0 {
		t.Errorf("Changes = %v, Unmanaged = %v, want les deux vides", p.Changes, p.Unmanaged)
	}
	if len(p.NotCompared) != 1 || p.NotCompared[0] != "database.tasks" {
		t.Errorf("NotCompared = %v, want [database.tasks]", p.NotCompared)
	}
}

// Avant ce commit, allow_data_loss ne débloquait que le destructif ordinaire :
// une réécriture silencieuse restait bloquée quoi qu'il arrive, quand bien
// même elle figurait dans allow_data_loss. Depuis ce commit, aucune classe ne
// bloque plus le plan par elle-même — notion-seed mesure le coût d'un
// changement et le dit, il ne le refuse plus sur la foi de sa classe. tasks
// perd une option de select, flows perd une option de status : les deux
// passent désormais, qu'allow_data_loss les couvre ou non.
func TestComputeDoesNotBlockOnOptionRemovalClassAlone(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{
			{Key: "tasks", Name: "Tasks", Properties: map[string]config.Property{
				"Tag": {Type: "select", Options: []config.Option{{Key: "a", Name: "A"}}},
			}},
			{Key: "flows", Name: "Flows", Properties: map[string]config.Property{
				"Statut": {Type: "status", Options: []config.Option{
					{Key: "todo", Name: "À faire", Group: "To-do"},
				}},
			}},
		},
		Lifecycle: config.Lifecycle{AllowDataLoss: []string{"database.tasks", "database.flows"}},
	}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Tag": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o1", Key: "a", Name: "A"}, {ID: "o2", Key: "b", Name: "B"},
			}},
		}},
		"flows": {ID: "db2", Name: "Flows", Properties: map[string]state.Property{
			"Statut": {ID: "p2", Type: "status", Options: []state.Option{
				{ID: "o3", Key: "todo", Name: "À faire", Group: "To-do"},
				{ID: "o4", Key: "ko", Name: "Annulé", Group: "Complete"},
			}},
		}},
	}}
	actual := map[string]Refreshed{
		"tasks": {Database: state.Database{ID: "db1", Name: "Tasks", Properties: map[string]state.Property{
			"Tag": {ID: "p1", Type: "select", Options: []state.Option{
				{ID: "o1", Name: "A"}, {ID: "o2", Name: "B"},
			}},
		}}},
		"flows": {Database: state.Database{ID: "db2", Name: "Flows", Properties: map[string]state.Property{
			"Statut": {ID: "p2", Type: "status", Options: []state.Option{
				{ID: "o3", Name: "À faire", Group: "To-do"},
				{ID: "o4", Name: "Annulé", Group: "Complete"},
			}},
		}}},
	}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false : %v", p.BlockedReasons)
	}
	if p.ToChange != 2 {
		t.Errorf("ToChange = %d, want 2 : les deux retraits d'option sont mesurés, pas refusés", p.ToChange)
	}
}

// Deux sous-cas : le premier couvre la ressource par prevent_destroy ET
// allow_data_loss à la fois, ce qui ne distingue rien — avec allow_data_loss
// qui couvre déjà la ressource, le case destructif de `absorb` ne matcherait
// de toute façon jamais, quel que soit l'ordre des `case` dans le switch, donc
// ce sous-cas seul passerait même si prevent_destroy et destructif étaient
// permutés. Le second sous-cas couvre la ressource par prevent_destroy SEUL :
// sous l'ordre inversé, le case destructif matcherait à la place (puisque
// allow_data_loss ne couvre pas la ressource) et produirait le message
// « changement destructif … allow_data_loss » sans jamais nommer
// prevent_destroy — c'est ce sous-cas qui distingue vraiment les deux.
func TestComputePreventDestroyBeatsAllowDataLoss(t *testing.T) {
	tests := []struct {
		name      string
		lifecycle config.Lifecycle
	}{
		{
			name: "prevent_destroy et allow_data_loss couvrent la même ressource",
			lifecycle: config.Lifecycle{
				PreventDestroy: []string{"database.tasks"},
				AllowDataLoss:  []string{"database.tasks"},
			},
		},
		{
			name: "prevent_destroy seul, sans allow_data_loss",
			lifecycle: config.Lifecycle{
				PreventDestroy: []string{"database.tasks"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Lifecycle: tt.lifecycle}
			applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
				"tasks": {ID: "db1", Name: "Tasks"},
			}}
			actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db1", Name: "Tasks"}}}

			p, err := Compute(cfg, applied, actual)
			if err != nil {
				t.Fatalf("Compute() error = %v", err)
			}
			if !p.Blocked {
				t.Error("prevent_destroy doit bloquer la destruction malgré allow_data_loss")
			}
			if !strings.Contains(strings.Join(p.BlockedReasons, " "), "prevent_destroy") {
				t.Errorf("la raison doit nommer prevent_destroy: %v", p.BlockedReasons)
			}
		})
	}
}
