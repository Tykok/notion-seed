// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/state"
)

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
	if len(p.Changes[0].Lines) != 2 {
		t.Fatalf("lines = %v, want 2 entrées", p.Changes[0].Lines)
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

func TestComputeAllowDataLossUnblocksDestructiveOnly(t *testing.T) {
	// tasks perd une option de select (destructif), statuses perd une option de
	// status (réécriture silencieuse). allow_data_loss couvre les deux
	// ressources ; une seule doit être débloquée.
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
	if !p.Blocked {
		t.Fatal("Blocked = false : la réécriture silencieuse ne doit JAMAIS être débloquée par allow_data_loss")
	}
	joined := strings.Join(p.BlockedReasons, " ")
	if strings.Contains(joined, "database.tasks") {
		t.Errorf("database.tasks est couverte par allow_data_loss: %v", p.BlockedReasons)
	}
	if !strings.Contains(joined, "database.flows") {
		t.Errorf("database.flows doit rester bloquée: %v", p.BlockedReasons)
	}
}

func TestComputePreventDestroyBeatsAllowDataLoss(t *testing.T) {
	cfg := &config.Config{
		Lifecycle: config.Lifecycle{
			PreventDestroy: []string{"database.tasks"},
			AllowDataLoss:  []string{"database.tasks"},
		},
	}
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
}
