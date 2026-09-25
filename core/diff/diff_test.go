// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/state"
)

// Un changement retenu par CompareDatabase (migration inexprimable) doit
// porter son motif jusqu'au Change du plan : c'est lui que cli/apply lit pour
// dire pourquoi il saute la ressource, sans redescendre dans Details.
func TestPlanCarriesWithheldOnTheChange(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{{
			Key: "tasks", Name: "Tasks",
			Properties: map[string]config.Property{
				"Statut": {Type: "status", Options: []config.Option{
					// Même key, nom changé : l'API ne sait pas renommer.
					{Key: "done", Name: "Terminé", Group: "Complete"},
				}},
			},
		}},
	}
	applied := &state.Snapshot{Version: state.Version, Databases: map[string]state.Database{
		"tasks": {ID: "db1", DataSourceID: "ds1", Name: "Tasks",
			Properties: map[string]state.Property{
				"Statut": {ID: "p1", Type: "status", Options: []state.Option{
					{ID: "o-done", Key: "done", Name: "Fait", Color: "green", Group: "Complete"},
				}},
			}},
	}}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{
		ID: "db1", DataSourceID: "ds1", Name: "Tasks",
		Properties: map[string]state.Property{
			"Statut": {ID: "p1", Type: "status", Options: []state.Option{
				{ID: "o-done", Name: "Fait", Color: "green", Group: "Complete"},
			}},
		},
	}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	for _, c := range p.Changes {
		if c.Resource != "database.tasks" {
			continue
		}
		if c.Withheld == "" {
			t.Fatal("Change.Withheld vide : cli/apply ne peut pas dire pourquoi il saute la ressource")
		}
		if c.Target != nil {
			t.Error("Change.Target non nulle sur une ressource retenue")
		}
		return
	}
	t.Fatal("aucun changement pour database.tasks")
}

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

// Une database à deux data sources part à la corbeille avec les deux, mais le
// comptage n'en interroge qu'un : la destruction doit le savoir.
func TestComputeMarksTheUncountedDataSourcesOfADestroy(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", DataSourceID: "ds-1"}},
	}
	for _, tt := range []struct{ sources, want int }{{0, 0}, {1, 0}, {2, 1}, {3, 2}} {
		actual := map[string]Refreshed{"tasks": {
			Database:    state.Database{ID: "db-1", DataSourceID: "ds-1"},
			DataSources: tt.sources,
		}}
		p, err := Compute(cfg, applied, actual)
		if err != nil {
			t.Fatal(err)
		}
		m := p.Changes[0].Details[0].Measure
		if m == nil || !m.AllRows || m.UncountedDataSources != tt.want {
			t.Errorf("%d data sources : Measure = %+v, want %d non compté(s)", tt.sources, m, tt.want)
		}
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

// prevent_destroy ne bloque plus le nettoyage d'une orpheline disparue hors de
// notion-seed : l'entrée de state obsolète est nettoyée dans tous les cas, et
// c'est le rendu de StaleState qui porte la mention, pas un blocage.
func TestComputeMovesProtectedOrphanToStaleState(t *testing.T) {
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
	if p.Blocked {
		t.Errorf("Blocked = true, want false : %v", p.BlockedReasons)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
}

// prevent_destroy ne bloque plus : il est noté sur la ressource, et c'est au
// rendu de le dire fort.
func TestComputeDoesNotBlockADestroyUnderPreventDestroy(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{PreventDestroy: []string{"database.tasks"}}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db-1", Name: "Tasks"}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false : plus rien ne bloque sur une classe : %v", p.BlockedReasons)
	}
	if p.ToDestroy != 1 {
		t.Errorf("ToDestroy = %d, want 1", p.ToDestroy)
	}
	if got := p.Changes[0].Acknowledged; len(got) != 1 || got[0] != "prevent_destroy" {
		t.Errorf("Acknowledged = %v, want [prevent_destroy]", got)
	}
}

// allow_data_loss devient un accusé de lecture : sa présence est notée, son
// absence ne bloque plus rien.
func TestComputeNotesAllowDataLossWithoutBlocking(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{AllowDataLoss: []string{"database.tasks"}}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db-1", Name: "Tasks"}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false")
	}
	if got := p.Changes[0].Acknowledged; len(got) != 1 || got[0] != "allow_data_loss" {
		t.Errorf("Acknowledged = %v, want [allow_data_loss]", got)
	}
}

// Une ressource couverte par les DEUX clés doit les accuser dans un ordre
// stable, et dans cet ordre-là : le rendu les imprime telles quelles, et la
// sortie de `plan` doit rester identique entre deux exécutions.
//
// Sans ce test, l'ordre ne tient qu'à la suite des deux `if` dans absorb :
// les permuter, ou verser les clés depuis une map, changerait la sortie sans
// qu'aucun test ne le remarque.
func TestComputeAcknowledgesPreventDestroyBeforeAllowDataLoss(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{
		PreventDestroy: []string{"database.tasks"},
		AllowDataLoss:  []string{"database.tasks"},
	}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db-1", Name: "Tasks"}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 {
		t.Fatalf("Changes = %d, want 1", len(p.Changes))
	}
	got := p.Changes[0].Acknowledged
	want := []string{"prevent_destroy", "allow_data_loss"}
	if len(got) != len(want) {
		t.Fatalf("Acknowledged = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Acknowledged = %v, want %v (l'ordre fait partie du contrat)", got, want)
			break
		}
	}
}

// Blocked survit pour ce qui n'est PAS une classification de risque : une
// ressource que le state ancre et que Notion ne connaît plus rend le plan
// incalculable, ce qui reste une erreur.
func TestComputeStillBlocksWhenAManagedResourceVanished(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{
		{Key: "tasks", Name: "Tasks", Properties: map[string]config.Property{"Name": {Type: "title"}}},
	}}
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
		t.Error("Blocked = false : une ressource gérée disparue reste une erreur")
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
	got := make([]string, 0, len(p.Changes[0].Details))
	for _, d := range p.Changes[0].Details {
		got = append(got, d.Op+" "+d.Target)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
	// Une création ne coûte rien : elle ne demande aucune mesure, donc aucun
	// appel ne sera payé pour elle. Et aucun détail ne doit affirmer « 0 ligne
	// concernée » sans que personne n'ait mesuré.
	for _, d := range p.Changes[0].Details {
		if d.Measure != nil {
			t.Errorf("la ligne %q demande une mesure alors qu'elle ne coûte rien", d.Target)
		}
		if d.Count != -1 {
			t.Errorf("Count = %d pour %q, want -1 tant que rien n'a été mesuré", d.Count, d.Target)
		}
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
