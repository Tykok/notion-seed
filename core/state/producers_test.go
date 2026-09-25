// SPDX-License-Identifier: GPL-3.0-or-later

package state

import (
	"testing"

	"github.com/tykok/notion-seed/core/config"
	"github.com/tykok/notion-seed/core/providers/notion/resources"
)

func TestFromConfigCarriesKeysAndNeverIDs(t *testing.T) {
	db := config.Database{
		Key: "tasks", Name: "Tasks", Description: "d", Icon: "🗂",
		Properties: map[string]config.Property{
			"Estimate": {Type: "number", Format: "number"},
			"Statut": {Type: "status", Options: []config.Option{
				{Key: "todo", Name: "À faire", Group: "To-do"},
				{Name: "Fait", Group: "Complete"},
			}},
		},
	}
	got := FromConfig(db)

	if got.ID != "" || got.DataSourceID != "" {
		t.Errorf("the config knows no id: %+v", got)
	}
	if got.Name != "Tasks" || got.Description != "d" || got.Icon != "🗂" {
		t.Errorf("attributes = %+v", got)
	}
	if f := got.Properties["Estimate"].Format; f != "number" {
		t.Errorf("Format = %q, want number", f)
	}
	opts := got.Properties["Statut"].Options
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2", len(opts))
	}
	if opts[0].Key != "todo" || opts[0].Name != "À faire" {
		t.Errorf("option 0 = %+v", opts[0])
	}
	if opts[1].Key != "" {
		t.Errorf("option 1 declares no key, Key = %q", opts[1].Key)
	}
}

func TestFromRemoteCarriesIDsAndNeverKeys(t *testing.T) {
	rd := resources.RemoteDatabase{
		ID: "db1", DataSourceID: "ds1", Name: "Tasks", Found: true,
		Properties: map[string]resources.RemoteProperty{
			"Statut": {ID: "p1", Type: "status", Options: []resources.RemoteOption{
				{ID: "o1", Name: "À faire", Color: "blue", Group: "To-do"},
			}},
			"Estimate": {ID: "p2", Type: "number", NumberFormat: "number"},
		},
	}
	got := FromRemote(rd)

	if got.ID != "db1" || got.DataSourceID != "ds1" {
		t.Errorf("ids = %+v", got)
	}
	if got.Properties["Estimate"].Format != "number" {
		t.Error("NumberFormat must land in Format")
	}
	opt := got.Properties["Statut"].Options[0]
	if opt.ID != "o1" || opt.Color != "blue" || opt.Group != "To-do" {
		t.Errorf("option = %+v", opt)
	}
	if opt.Key != "" {
		t.Errorf("the actual state knows no key, Key = %q", opt.Key)
	}
}

func TestJoinOptionKeysMatchesOnNameAndCountsOrphans(t *testing.T) {
	actual := Database{Properties: map[string]Property{
		"Statut": {Type: "status", Options: []Option{
			{ID: "o1", Name: "À faire"},
			{ID: "o2", Name: "Imprévu"},
		}},
	}}
	desired := Database{Properties: map[string]Property{
		"Statut": {Type: "status", Options: []Option{
			{Key: "todo", Name: "À faire"},
		}},
	}}

	got, orphans := JoinOptionKeys(actual, desired)

	opts := got.Properties["Statut"].Options
	if opts[0].Key != "todo" {
		t.Errorf("option joined by name: Key = %q, want todo", opts[0].Key)
	}
	if opts[0].ID != "o1" {
		t.Errorf("the join must not lose the id: %+v", opts[0])
	}
	if opts[1].Key != "" {
		t.Errorf("without a name match, no invented key: %+v", opts[1])
	}
	if orphans != 1 {
		t.Errorf("orphans = %d, want 1", orphans)
	}
}

func TestJoinOptionKeysLeavesActualUntouched(t *testing.T) {
	actual := Database{Properties: map[string]Property{
		"Statut": {Type: "status", Options: []Option{{ID: "o1", Name: "À faire"}}},
	}}
	desired := Database{Properties: map[string]Property{
		"Statut": {Type: "status", Options: []Option{{Key: "todo", Name: "À faire"}}},
	}}

	_, _ = JoinOptionKeys(actual, desired)

	if k := actual.Properties["Statut"].Options[0].Key; k != "" {
		t.Errorf("the input was mutated: Key = %q", k)
	}
}
