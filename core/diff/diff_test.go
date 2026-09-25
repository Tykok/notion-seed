// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/state"
)

// A change withheld by CompareDatabase (migration not expressible) must carry
// its reason up to the plan's Change: it is what cli/apply reads to say why it
// skips the resource, without going down into Details.
func TestPlanCarriesWithheldOnTheChange(t *testing.T) {
	cfg := &config.Config{
		Databases: []config.Database{{
			Key: "tasks", Name: "Tasks",
			Properties: map[string]config.Property{
				"Statut": {Type: "status", Options: []config.Option{
					// Same key, changed name: the API cannot rename.
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
			t.Fatal("Change.Withheld empty: cli/apply cannot say why it skips the resource")
		}
		if c.Target != nil {
			t.Error("Change.Target non-nil on a withheld resource")
		}
		return
	}
	t.Fatal("no change for database.tasks")
}

// A database removed from the YAML and already deleted by hand in Notion must
// no longer be announced "to destroy": there is nothing left to destroy. What
// remains is a stale state entry, whose cleanup writes nothing to Notion.
func TestComputeReportsStaleStateForOrphanAlreadyDeleted(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "not found (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.ToDestroy != 0 {
		t.Errorf("ToDestroy = %d, want 0: the destruction already happened", p.ToDestroy)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false: %v", p.BlockedReasons)
	}
}

// An archived database counts as destroyed: in Notion, destroying a database
// means archiving it.
func TestComputeReportsStaleStateForArchivedOrphan(t *testing.T) {
	cfg := &config.Config{}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "archived or in the trash"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
}

// An orphan still present in Notion stays a planned destruction.
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
		t.Errorf("StaleState = %v, want empty", p.StaleState)
	}
}

// A database with two data sources goes to the trash with both, but the count
// queries only one: the destruction must know it.
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
			t.Errorf("%d data sources: Measure = %+v, want %d not counted", tt.sources, m, tt.want)
		}
	}
}

// Without a refresh (--skip-preflight), nothing is known about the orphan:
// neither destroy it, nor conclude it has vanished. It falls under "Not
// compared".
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
		t.Errorf("ToDestroy = %d, want 0: nothing was read", p.ToDestroy)
	}
	if got := p.NotCompared; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("NotCompared = %v, want [database.tasks]", got)
	}
}

// acknowledge_destroy never blocks the cleanup of an orphan that vanished
// outside notion-seed: the stale state entry is cleaned in every case, and the
// rendering of StaleState carries the notice, not a block.
func TestComputeMovesAcknowledgedOrphanToStaleState(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{AcknowledgeDestroy: []string{"database.tasks"}}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "not found (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked {
		t.Errorf("Blocked = true, want false: %v", p.BlockedReasons)
	}
	if got := p.StaleState; len(got) != 1 || got[0] != "database.tasks" {
		t.Errorf("StaleState = %v, want [database.tasks]", got)
	}
}

// acknowledge_destroy blocks nothing: it is recorded on the resource, and it
// is up to the rendering to say it loudly.
func TestComputeDoesNotBlockAnAcknowledgedDestroy(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{AcknowledgeDestroy: []string{"database.tasks"}}}
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
		t.Errorf("Blocked = true, want false: nothing blocks on a class any more: %v", p.BlockedReasons)
	}
	if p.ToDestroy != 1 {
		t.Errorf("ToDestroy = %d, want 1", p.ToDestroy)
	}
	if got := p.Changes[0].Acknowledged; len(got) != 1 || got[0] != "acknowledge_destroy" {
		t.Errorf("Acknowledged = %v, want [acknowledge_destroy]", got)
	}
}

// acknowledge_data_loss is an acknowledgement: its presence is recorded, its
// absence blocks nothing.
func TestComputeNotesAcknowledgedDataLossWithoutBlocking(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{AcknowledgeDataLoss: []string{"database.tasks"}}}
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
	if got := p.Changes[0].Acknowledged; len(got) != 1 || got[0] != "acknowledge_data_loss" {
		t.Errorf("Acknowledged = %v, want [acknowledge_data_loss]", got)
	}
}

// A deprecated key is named as the user wrote it: the plan line must match
// their YAML, the load warning says to rename it.
func TestComputeNamesADeprecatedLifecycleKeyAsWritten(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{DeprecatedPreventDestroy: []string{"database.tasks"}}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Database: state.Database{ID: "db-1", Name: "Tasks"}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Changes[0].Acknowledged; len(got) != 1 || got[0] != "prevent_destroy" {
		t.Errorf("Acknowledged = %v, want [prevent_destroy]", got)
	}
}

// A resource covered by BOTH keys must acknowledge them in a stable order, and
// in that order: the rendering prints them as they are, and the output of
// `plan` must stay identical between two runs.
//
// Without this test, the order only depends on the sequence of the two `if`s
// in absorb: swapping them, or pouring the keys from a map, would change the
// output without any test noticing.
func TestComputeAcknowledgesDestroyBeforeDataLoss(t *testing.T) {
	cfg := &config.Config{Lifecycle: config.Lifecycle{
		AcknowledgeDestroy:  []string{"database.tasks"},
		AcknowledgeDataLoss: []string{"database.tasks"},
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
	want := []string{"acknowledge_destroy", "acknowledge_data_loss"}
	if len(got) != len(want) {
		t.Fatalf("Acknowledged = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Acknowledged = %v, want %v (the order is part of the contract)", got, want)
			break
		}
	}
}

// Blocked survives for what is NOT a risk classification: a resource the
// state anchors and that Notion no longer knows makes the plan impossible to
// compute, which stays an error.
func TestComputeStillBlocksWhenAManagedResourceVanished(t *testing.T) {
	cfg := &config.Config{Databases: []config.Database{
		{Key: "tasks", Name: "Tasks", Properties: map[string]config.Property{"Name": {Type: "title"}}},
	}}
	applied := &state.Snapshot{
		Version:   state.Version,
		Databases: map[string]state.Database{"tasks": {ID: "db-1", Name: "Tasks"}},
	}
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "not found (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Blocked {
		t.Error("Blocked = false: a vanished managed resource stays an error")
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
		t.Error("Blocked = true, want false — creations are safe")
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
		t.Errorf("order = %q, %q", p.Changes[0].Resource, p.Changes[1].Resource)
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
	// The database name is announced too: it goes in the creation payload, so
	// it must appear in the plan.
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
	// A creation costs nothing: it requires no measurement, so no call will be
	// paid for it. And no detail must assert "0 rows affected" without anyone
	// having measured.
	for _, d := range p.Changes[0].Details {
		if d.Measure != nil {
			t.Errorf("line %q requests a measurement while it costs nothing", d.Target)
		}
		if d.Count != -1 {
			t.Errorf("Count = %d for %q, want -1 as long as nothing was measured", d.Count, d.Target)
		}
	}
}

func TestComputeEmptyConfigProducesEmptyPlan(t *testing.T) {
	p, err := Compute(&config.Config{}, nil, nil)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if p.ToAdd != 0 || len(p.Changes) != 0 {
		t.Errorf("non-empty plan: %+v", p)
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
		ID: "db1", Name: "Renamed by hand", Properties: map[string]state.Property{
			"Name":    {ID: "p1", Type: "title"},
			"Créé le": {ID: "p9", Type: "created_time"},
		},
	}}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if len(p.Drifts) != 1 {
		t.Fatalf("Drifts = %v, want 1 entry", p.Drifts)
	}
	if len(p.Unmanaged) != 1 {
		t.Fatalf("Unmanaged = %v, want 1 entry", p.Unmanaged)
	}
	if p.ToChange != 1 {
		t.Errorf("ToChange = %d, want 1 — the name goes back to the YAML", p.ToChange)
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
	actual := map[string]Refreshed{"tasks": {Missing: true, Reason: "not found (404)"}}

	p, err := Compute(cfg, applied, actual)
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if !p.Blocked {
		t.Error("Blocked = false, want true — the managed identity vanished")
	}
	if !strings.Contains(strings.Join(p.BlockedReasons, " "), "not found") {
		t.Errorf("BlockedReasons = %v, must name the reason", p.BlockedReasons)
	}
}

// C2: --skip-preflight passes a nil `actual` — refreshManaged never ran.
// CompareDatabase then returns an empty result for database.tasks ("we know
// nothing, so we say nothing"), but Compute must still name the resource: a
// Plan empty everywhere must never be mistaken for an observed match.
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
		t.Error("Blocked = true, want false — --skip-preflight is not an error")
	}
	if len(p.Changes) != 0 || len(p.Unmanaged) != 0 {
		t.Errorf("Changes = %v, Unmanaged = %v, want both empty", p.Changes, p.Unmanaged)
	}
	if len(p.NotCompared) != 1 || p.NotCompared[0] != "database.tasks" {
		t.Errorf("NotCompared = %v, want [database.tasks]", p.NotCompared)
	}
}

// Before this commit, allow_data_loss (now acknowledge_data_loss) only cleared the ordinary destructive
// block: a silent rewrite stayed blocked no matter what, even when it was
// listed there. Since this commit, no class blocks the plan by
// itself any more — notion-seed measures the cost of a change and says it, it
// no longer refuses it on the strength of its class. tasks loses a select
// option, flows loses a status option: both now go through, whether
// acknowledge_data_loss covers them or not.
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
		Lifecycle: config.Lifecycle{AcknowledgeDataLoss: []string{"database.tasks", "database.flows"}},
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
		t.Errorf("Blocked = true, want false: %v", p.BlockedReasons)
	}
	if p.ToChange != 2 {
		t.Errorf("ToChange = %d, want 2: both option removals are measured, not rejected", p.ToChange)
	}
}
