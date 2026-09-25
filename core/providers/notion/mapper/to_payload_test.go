// SPDX-License-Identifier: GPL-3.0-or-later

package mapper

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tykok/notion-seed/core/state"
)

const testParentPageID = "33333333-3333-4333-8333-333333333333"

// The payload must contain ONLY what the target holds. A group substituted
// here would be a write the plan did not show — the historical "To-do"
// default produced exactly that.
func TestPropertyPayloadWritesOnlyDeclaredGroups(t *testing.T) {
	p := state.Property{
		Type: "status",
		Options: []state.Option{
			{Name: "À faire", Group: "To-do"},
			{Name: "Fait", Group: "Complete"},
		},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["status"].(map[string]any)["options"].([]map[string]any)
	if opts[0]["group"] != "To-do" || opts[1]["group"] != "Complete" {
		t.Errorf("groups = %v, want To-do then Complete", opts)
	}
}

// A status option without a group can no longer reach here (the schema
// rejects it), and the mapper must certainly not invent one: it passes the
// empty value on, and the missing group becomes visible instead of hidden.
func TestPropertyPayloadDoesNotInventAGroup(t *testing.T) {
	p := state.Property{
		Type:    "status",
		Options: []state.Option{{Name: "Orpheline"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["status"].(map[string]any)["options"].([]map[string]any)
	if g, present := opts[0]["group"]; present {
		t.Errorf("group = %v, want absent: the mapper picks no group", g)
	}
}

// The configuration key is an internal identity: the API does not know it and
// must never receive it.
func TestPropertyPayloadNeverSendsTheConfigKey(t *testing.T) {
	p := state.Property{
		Type:    "select",
		Options: []state.Option{{Key: "haute", Name: "Haute", Color: "red"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["select"].(map[string]any)["options"].([]map[string]any)
	if _, present := opts[0]["key"]; present {
		t.Errorf("options = %v, the config key must not go to the API", opts)
	}
	if opts[0]["color"] != "red" {
		t.Errorf("color = %v, want red", opts[0]["color"])
	}
}

// A color missing from the YAML is not sent: notion-seed does not choose on
// the user's behalf, exactly as for the group.
func TestPropertyPayloadOmitsUndeclaredColor(t *testing.T) {
	p := state.Property{
		Type:    "multi_select",
		Options: []state.Option{{Name: "Urgent"}},
	}
	got, err := PropertyPayload(p)
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	opts := got["multi_select"].(map[string]any)["options"].([]map[string]any)
	if _, present := opts[0]["color"]; present {
		t.Errorf("options = %v, want no color", opts)
	}
}

func TestPropertyPayloadCarriesNumberFormat(t *testing.T) {
	got, err := PropertyPayload(state.Property{Type: "number", Format: "euro"})
	if err != nil {
		t.Fatalf("PropertyPayload() error = %v", err)
	}
	if got["number"].(map[string]any)["format"] != "euro" {
		t.Errorf("payload = %v, want format euro", got)
	}
}

func TestPropertyPayloadRejectsUnsupportedType(t *testing.T) {
	_, err := PropertyPayload(state.Property{Type: "formula"})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("error = %v, want ErrUnsupportedType", err)
	}
}

func TestDatabaseCreatePayloadCarriesParentTitleAndProperties(t *testing.T) {
	target := state.Database{
		Name:        "Tasks",
		Description: "Suivi",
		Icon:        "✅",
		Properties: map[string]state.Property{
			"Name":     {Type: "title"},
			"Estimate": {Type: "number", Format: "number"},
		},
	}
	raw, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	parent := body["parent"].(map[string]any)
	if parent["page_id"] != testParentPageID {
		t.Errorf("parent = %v", parent)
	}
	if parent["type"] != "page_id" {
		t.Errorf("parent.type = %v, want page_id", parent["type"])
	}
	// Since API 2025-09-03, the schema lives in the data source, passed on
	// creation under initial_data_source.
	props := body["initial_data_source"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["Estimate"]; !ok {
		t.Errorf("properties = %v, want Estimate", props)
	}
	if body["icon"].(map[string]any)["emoji"] != "✅" {
		t.Errorf("icon = %v", body["icon"])
	}
}

// An empty description must not produce an empty field: the API would write it.
func TestDatabaseCreatePayloadOmitsEmptyDescriptionAndIcon(t *testing.T) {
	target := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Name": {Type: "title"}},
	}
	raw, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err != nil {
		t.Fatalf("DatabaseCreatePayload() error = %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["description"]; present {
		t.Error("description present while the target holds none")
	}
	if _, present := body["icon"]; present {
		t.Error("icon present while the target holds none")
	}
}

// The message must name the database AND the property: without the key, the
// error does not help find the offending file.
func TestDatabaseCreatePayloadNamesKeyAndPropertyOnUnsupportedType(t *testing.T) {
	target := state.Database{
		Name:       "Tasks",
		Properties: map[string]state.Property{"Bizarre": {Type: "formula"}},
	}
	_, err := DatabaseCreatePayload("tasks", target, testParentPageID)
	if err == nil {
		t.Fatal("DatabaseCreatePayload() error = nil, want the type rejected")
	}
	for _, want := range []string{"tasks", "Bizarre", "formula", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message = %q, it must contain %q", err.Error(), want)
		}
	}
}

// Without the id, the existing option would be matched by name on the API
// side: a changed name would become a removal followed by an addition, and
// the rows that held it would lose their value. A new option has none.
func TestOptionsPayloadCarriesExistingIDsAndOmitsThemForNewOnes(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Prio": {Type: "select", Options: []state.Option{
			{ID: "o-haute", Name: "Haute", Color: "red"},
			{Name: "Moyenne", Color: "orange"},
		}},
	}}

	body, err := DataSourceUpdatePayload("tasks", target, []string{"Prio"})
	if err != nil {
		t.Fatalf("DataSourceUpdatePayload() error = %v", err)
	}
	var got struct {
		Properties map[string]struct {
			Select struct {
				Options []map[string]any `json:"options"`
			} `json:"select"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unreadable payload: %v", err)
	}
	opts := got.Properties["Prio"].Select.Options
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2", len(opts))
	}
	if opts[0]["id"] != "o-haute" {
		t.Errorf("options[0][id] = %v, want o-haute — without the id, the API destroys the option", opts[0]["id"])
	}
	if _, present := opts[1]["id"]; present {
		t.Errorf("options[1] carries an id while it is new: %v", opts[1])
	}
}

// Only the plan's properties go out: sending more would write properties the
// plan did not show.
func TestDataSourceUpdatePayloadSendsOnlyTheNamedProperties(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Prio":  {Type: "select"},
		"Notes": {Type: "rich_text"},
	}}
	body, err := DataSourceUpdatePayload("tasks", target, []string{"Prio"})
	if err != nil {
		t.Fatalf("DataSourceUpdatePayload() error = %v", err)
	}
	if strings.Contains(string(body), "Notes") {
		t.Errorf("payload = %s, want no Notes: only the plan's properties go out", body)
	}
}

// The description is rejected by the API on the data source (measured on
// 2026-09-24): only the fields named on the database side go out, never more.
func TestDatabaseUpdatePayloadSendsOnlyTheNamedFields(t *testing.T) {
	target := state.Database{Name: "Tâches", Description: "desc", Icon: "🟢"}
	body, err := DatabaseUpdatePayload("tasks", target, []string{"name", "icon"})
	if err != nil {
		t.Fatalf("DatabaseUpdatePayload() error = %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "Tâches") || !strings.Contains(s, "🟢") {
		t.Errorf("payload = %s, want the title and the icon", s)
	}
	if strings.Contains(s, "description") {
		t.Errorf("payload = %s, want no description: it is not in the plan", s)
	}
}

// Review Focus #4: an unsupported type must name the database AND the
// property, and the error must come up before any call.
func TestDataSourceUpdatePayloadNamesTheUnsupportedProperty(t *testing.T) {
	target := state.Database{Properties: map[string]state.Property{
		"Lien": {Type: "relation"},
	}}
	_, err := DataSourceUpdatePayload("tasks", target, []string{"Lien"})
	if err == nil {
		t.Fatal("error = nil, want an unsupported type error")
	}
	for _, want := range []string{"tasks", "Lien", "  → "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}
