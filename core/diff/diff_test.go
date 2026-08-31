package diff

import (
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
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

	p, err := Compute(cfg, nil)
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
	p, _ := Compute(cfg, nil)
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
	p, _ := Compute(cfg, nil)
	if len(p.Changes[0].Lines) != 2 {
		t.Fatalf("lines = %v, want 2 entrées", p.Changes[0].Lines)
	}
}

func TestComputeEmptyConfigProducesEmptyPlan(t *testing.T) {
	p, err := Compute(&config.Config{}, map[string]resources.RemoteState{})
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.ToAdd != 0 || len(p.Changes) != 0 {
		t.Errorf("plan non vide: %+v", p)
	}
}
