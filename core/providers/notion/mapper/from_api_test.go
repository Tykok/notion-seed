package mapper

import "testing"

const dbBody = `{
  "object": "database",
  "id": "55555555-5555-4555-8555-555555555555",
  "archived": false,
  "in_trash": false,
  "data_sources": [{"id": "66666666-6666-4666-8666-666666666666", "name": "spike-projects"}],
  "parent": {"page_id": "44444444-4444-4444-8444-444444444444", "type": "page_id"}
}`

const dsBody = `{
  "object": "data_source",
  "id": "66666666-6666-4666-8666-666666666666",
  "title": [{"plain_text": "spike-projects"}],
  "description": [{"plain_text": "Suivi des projets internes"}],
  "parent": {"database_id": "55555555-5555-4555-8555-555555555555", "type": "database_id"},
  "properties": {
    "Name": {"id": "title", "name": "Name", "type": "title", "title": {}},
    "Budget": {"id": "Q_bo", "name": "Budget", "type": "number", "number": {"format": "euro"}},
    "Priority": {
      "id": "S%5ENS", "name": "Priority", "type": "select",
      "select": {"options": [
        {"id": "77777777-7777-4777-8777-777777777777", "name": "Low", "color": "gray"},
        {"id": "8fab7efc-0000-0000-0000-000000000000", "name": "High", "color": "red"}
      ]}
    },
    "Status": {
      "id": "f%3Cyc", "name": "Status", "type": "status",
      "status": {
        "options": [
          {"id": "88888888-8888-4888-8888-888888888888", "name": "To-Do", "color": "gray"},
          {"id": "5529b100-0000-0000-0000-000000000000", "name": "Building", "color": "blue"}
        ],
        "groups": [
          {"id": "6ac28dc1-0000-0000-0000-000000000000", "name": "To-do", "color": "gray",
           "option_ids": ["88888888-8888-4888-8888-888888888888"]},
          {"id": "95b7387d-0000-0000-0000-000000000000", "name": "In progress", "color": "blue",
           "option_ids": ["5529b100-0000-0000-0000-000000000000"]}
        ]
      }
    }
  }
}`

func TestRemoteDatabaseFromJSON(t *testing.T) {
	got, err := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	if err != nil {
		t.Fatalf("RemoteDatabaseFromJSON() error = %v", err)
	}
	if !got.Exists() {
		t.Error("Exists() = false, want true")
	}
	if got.ID != "55555555-5555-4555-8555-555555555555" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.DataSourceID != "66666666-6666-4666-8666-666666666666" {
		t.Errorf("DataSourceID = %q", got.DataSourceID)
	}
	if got.Name != "spike-projects" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "Suivi des projets internes" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Archived {
		t.Error("Archived = true, want false")
	}
}

// Les ids de propriété sont indispensables : ils distinguent un renommage
// d'une suppression suivie d'une création.
func TestRemoteDatabaseFromJSONKeepsPropertyIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	if p := got.Properties["Budget"]; p.ID != "Q_bo" || p.NumberFormat != "euro" {
		t.Errorf("Budget = %+v, want ID Q_bo et format euro", p)
	}
	if p := got.Properties["Status"]; p.ID != "f%3Cyc" {
		t.Errorf("Status.ID = %q, want %q", p.ID, "f%3Cyc")
	}
}

// Le group n'est pas porté par l'option dans la réponse : il faut le
// reconstruire depuis groups[].option_ids. Sans ça, un diff sur le groupe
// serait impossible.
func TestRemoteDatabaseFromJSONResolvesOptionGroupsFromOptionIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	status := got.Properties["Status"]
	byName := map[string]string{}
	for _, o := range status.Options {
		byName[o.Name] = o.Group
	}
	if byName["To-Do"] != "To-do" {
		t.Errorf("group de To-Do = %q, want %q", byName["To-Do"], "To-do")
	}
	if byName["Building"] != "In progress" {
		t.Errorf("group de Building = %q, want %q", byName["Building"], "In progress")
	}
}

func TestRemoteDatabaseFromJSONKeepsOptionIDs(t *testing.T) {
	got, _ := RemoteDatabaseFromJSON([]byte(dbBody), []byte(dsBody))
	sel := got.Properties["Priority"]
	if len(sel.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(sel.Options))
	}
	if sel.Options[0].ID != "77777777-7777-4777-8777-777777777777" {
		t.Errorf("Options[0].ID = %q", sel.Options[0].ID)
	}
}
